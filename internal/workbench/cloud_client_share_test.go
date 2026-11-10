package workbench

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"github.com/sylvinus/agent-vm/internal/remotehost"
)

// Exercise the real HTTP Upgrade and file protocol with a deliberately
// untrusted cloud peer. The session transport itself is already tested over SSH.
func TestNativeCloudShareGrantAndRevocation(t *testing.T) {
	root, profile, home := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	if err := os.WriteFile(isolatedGitConfig(t), []byte("[safe]\n bareRepository = explicit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	token := randomToken()
	peers := make(chan net.Conn, 1)
	var lie atomic.Bool
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		if r.URL.Path != "/api/projects/client-share" || len(id) != 32 || r.URL.RawQuery != "id="+id || strings.Contains(fmt.Sprint(r.Header), token) {
			t.Errorf("cloud received unexpected route/credentials")
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodPost:
			if r.Header.Get("Upgrade") != "cage-sftp-v1" || r.ContentLength != 0 {
				t.Error("unexpected SFTP handshake")
				return
			}
			conn, rw, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			fmt.Fprint(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: cage-sftp-v1\r\n\r\n")
			rw.Flush()
			peers <- conn
		case http.MethodGet:
			path := "/run/cage-client-projects/" + id
			if lie.Load() {
				path = root
			}
			jsonReply(w, map[string]string{"workspace": path})
		}
	}))
	defer cloud.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	session := &remotehost.Session{Origin: cloud.URL, Context: ctx, Transport: cloud.Client().Transport, Manifest: remotehost.PublicManifest{Capabilities: map[string]bool{"client_project_share": true}}}
	g := newRemoteGateway(profile)
	g.desktop = &desktopGateway{generation: "selected", active: &desktopTarget{mode: "remote", ctx: ctx, session: session}}
	s := &Server{profile: profile, token: token, remote: g}
	request := func(method, route, body, generation string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/client/"+route, strings.NewReader(body))
		r.Header.Set(connectionHeader, generation)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.clientRequest(w, r)
		return w
	}
	body := func(secret string) string {
		data, _ := json.Marshal(map[string]any{"path": root, "readonly": false, "token": secret})
		return string(data)
	}
	if w := request("POST", "project-shares", body(""), "selected"); w.Code != http.StatusForbidden {
		t.Fatal("browser without native authorization", w.Code)
	}
	if w := request("POST", "project-shares", body(token), "stale"); w.Code != http.StatusConflict {
		t.Fatal("stale native picker", w.Code)
	}
	w := request("POST", "project-shares", body(token), "selected")
	var grant ClientProjectShare
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &grant) != nil {
		t.Fatal("grant:", w.Code, w.Body.String())
	}
	conn := <-peers
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	peer, err := sftp.NewClientPipe(conn, conn)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	f, err := peer.Create("/source.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write([]byte("written in cloud")); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "source.txt")); err != nil || string(data) != "written in cloud" {
		t.Fatal("missing local write", err)
	}
	if err := exerciseProjectGitWrites(peer, ""); err != nil {
		t.Fatal("cloud grant blocked project metadata writes", err)
	}
	if w := request("GET", "project-shares", "", "selected"); w.Code != 200 || !strings.Contains(w.Body.String(), grant.ID) {
		t.Fatal("missing grant", w)
	}
	if w := request("DELETE", "project-shares/"+grant.ID, "", "selected"); w.Code != 200 {
		t.Fatal("revoke:", w.Code)
	}
	if _, err = f.Write([]byte("post revoke")); err == nil {
		t.Fatal("old file handle usable after revoke")
	}
	w = request("GET", "project-shares", "", "selected")
	if strings.Contains(w.Body.String(), grant.ID) {
		t.Fatal("revoked grant listed")
	}
	lie.Store(true)
	if w = request("POST", "project-shares", body(token), "selected"); w.Code == 200 {
		t.Fatal("cloud selected an arbitrary guest path")
	}
	conn = <-peers
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var buf [1]byte
	if _, err = conn.Read(buf[:]); err != io.EOF {
		t.Fatal("unconfirmed grant kept alive", err)
	}
	lie.Store(false)
	if w = request("POST", "project-shares", body(token), "selected"); w.Code != 200 {
		t.Fatal("re-grant", w.Code, w.Body.String())
	}
	afterDisconnect := <-peers
	defer afterDisconnect.Close()
	cancel()
	g.drainClosedClientShares()
	afterDisconnect.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err = afterDisconnect.Read(buf[:]); err != io.EOF {
		t.Fatal("disconnected SFTP tunnel still accessible", err)
	}
	if len(g.clientShares) != 0 {
		t.Fatal("disconnected grants retained")
	}
}
