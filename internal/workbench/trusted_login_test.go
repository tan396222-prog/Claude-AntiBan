package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestTrustedLocalLifecycle(t *testing.T) {
	profile, devices := t.TempDir(), t.TempDir()
	a, err := loadLocalAccount(profile)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.trustedLogin("create", "", devices); err != nil {
		t.Fatal(err)
	}
	original := bytes.Clone(a.key)
	a.lock()
	a, err = loadLocalAccount(profile)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.trustedLogin("resume", "", t.TempDir()); err == nil {
		t.Fatal("untrusted OS profile opened workspace")
	}
	if err = a.trustedLogin("resume", "", devices); err != nil || !bytes.Equal(original, a.key) {
		t.Fatal("restart lost workspace", err)
	}
	if err = a.trustedLogin("protect", "123", devices); err == nil {
		t.Fatal("accepted short password")
	}
	if err = a.trustedLogin("protect", "first-password", devices); err != nil {
		t.Fatal(err)
	}
	if err = a.trustedLogin("create", "", devices); err == nil {
		t.Fatal("overwrote password account")
	}
	a.lock()
	if err = a.trustedLogin("resume", "", devices); err == nil {
		t.Fatal("stale device grant")
	}
	if err = a.unlock("first-password"); err != nil || !bytes.Equal(original, a.key) {
		t.Fatal("password conversion lost workspace", err)
	}
	if err = a.trustedLogin("enable", "first-password", devices); err != nil {
		t.Fatal(err)
	}
	a.lock()
	if err = a.trustedLogin("resume", "", devices); err != nil || !bytes.Equal(original, a.key) {
		t.Fatal("password account failed automatic unlock", err)
	}
	if err = a.lockAccess(false); err != nil {
		t.Fatal(err)
	}
	if err = a.trustedLogin("resume", "", devices); err == nil {
		t.Fatal("ignored explicit lock")
	}
	if err = a.unlock("first-password"); err != nil {
		t.Fatal(err)
	}
	if err = a.lockAccess(true); err != nil {
		t.Fatal(err)
	}
	a, err = loadLocalAccount(profile)
	if err != nil {
		t.Fatal(err)
	}
	if a.view().TrustedLogin || a.trustedLogin("resume", "", devices) == nil {
		t.Fatal("logout retained trust")
	}
}

func TestTrustedLocalLauncherBoundary(t *testing.T) {
	s := testServer(t)
	if w := request(s, "POST", "/api/desktop-local/trusted-login", s.origin, `{"action":"resume"}`, true); w.Code != http.StatusUnauthorized {
		t.Fatalf("ordinary session reached launcher action: %d", w.Code)
	}
	if w := request(s, "POST", "/api/account/trusted-login", s.origin, `{}`, true); w.Code != http.StatusNotFound {
		t.Fatalf("ordinary account route accepted trusted action: %d", w.Code)
	}
}

func TestTrustedLocalUnlockIsPrivateToDesktop(t *testing.T) {
	s := testServer(t)
	a, err := loadLocalAccount(s.profile)
	if err != nil {
		t.Fatal(err)
	}
	s.account = a
	if err = a.trustedLogin("create", "", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if !a.viewFor(true).Unlocked || a.viewFor(false).Unlocked {
		t.Fatal("trusted login escaped desktop view")
	}
	if response := request(s, "GET", "/api/favorites", "", "", true); response.Code != http.StatusLocked {
		t.Fatalf("session cookie alone opened favorites: %d", response.Code)
	}
	if response := request(s, "POST", "/api/account/profile", s.origin, `{"username":"Different"}`, true); response.Code != http.StatusLocked {
		t.Fatalf("session cookie alone changed profile: %d", response.Code)
	}
	req, _ := http.NewRequest("GET", s.origin, nil)
	req.AddCookie(&http.Cookie{Name: s.cookieName() + "-desktop", Value: trustedViewProof(s.token)})
	if !s.hasTrustedView(req) {
		t.Fatal("local launcher proof was refused")
	}
}

func TestTrustedLocalDesktopEntryAndWorkload(t *testing.T) {
	for _, env := range []string{"LOCALAPPDATA", "XDG_CONFIG_HOME", "HOME"} {
		t.Setenv(env, t.TempDir())
	}
	workload := testServer(t)
	var err error
	workload.account, err = loadLocalAccount(workload.profile)
	if err != nil {
		t.Fatal(err)
	}
	workload.instanceID = randomToken()
	server := httptest.NewUnstartedServer(workload.handler())
	port := server.Listener.Addr().(*net.TCPAddr).Port
	workload.origin = "http://localhost:" + strconv.Itoa(port)
	server.Start()
	defer server.Close()
	record := controlFile{Port: port, PID: os.Getpid(), Token: workload.token}
	if err = writePrivate(filepath.Join(workload.profile, "control.json"), record); err != nil {
		t.Fatal(err)
	}
	c, err := openController(workload.profile)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	local := &desktopLocal{profile: workload.profile}
	session, err := local.localSession(ctx, ctx, c, desktopLocalReply{Identity: workload.identity, InstanceID: workload.instanceID})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	gateway := testServer(t)
	gateway.remote = newDesktopGateway(gateway.profile, local)
	d := gateway.remote.desktop
	d.active = &desktopTarget{id: d.generation, mode: "local", ctx: ctx, session: session}
	body, _ := json.Marshal(trustedLoginRequest{Action: "create", ConnectionID: d.generation})
	w := request(gateway, "POST", "/api/client/trusted-login", gateway.origin, string(body), true)
	if w.Code != http.StatusOK {
		t.Fatalf("trust creation through gateway: %d %s", w.Code, w.Body.String())
	}
	read := gatewayRequest(gateway, "/api/account", d.generation)
	var view accountView
	if read.Code != 200 || json.Unmarshal(read.Body.Bytes(), &view) != nil || !view.Unlocked || !view.TrustedLocal {
		t.Fatalf("local gateway cannot see unlocked account: %s", read.Body.String())
	}
	workload.account.lock()
	body, _ = json.Marshal(trustedLoginRequest{Action: "resume", ConnectionID: d.generation})
	w = request(gateway, "POST", "/api/client/trusted-login", gateway.origin, string(body), true)
	if w.Code != http.StatusOK || !workload.account.view().Unlocked {
		t.Fatalf("local resume failed: %d %s", w.Code, w.Body.String())
	}
}
