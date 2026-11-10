//go:build !windows

package workbench

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sylvinus/agent-vm/internal/host"
	"github.com/sylvinus/agent-vm/internal/mobilelink"
	"github.com/sylvinus/agent-vm/internal/remotelink"
)

// Opt-in host for the independently driven Android App. The driver imports
// connection.json, checks host-pin.txt, sends "initial mobile prompt", waits
// for approval-pending, approves Bash once, then sends "follow-up". It writes
// ui-saw-continuation only after the WebView displays "isolated continuation".
func TestMobileCLIAndroidApp(t *testing.T) {
	handoff := os.Getenv("CAGE_TEST_ANDROID_HANDOFF")
	if handoff == "" {
		t.Skip("requires a running Android App and an isolated HTTPS relay")
	}
	info, err := os.Stat(handoff)
	if err != nil || !filepath.IsAbs(handoff) || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		t.Fatal("handoff must be an absolute private QA directory")
	}
	relayOrigin := os.Getenv("CAGE_TEST_ANDROID_RELAY")
	u, err := url.Parse(relayOrigin)
	if err != nil || u.Scheme != "https" || !net.ParseIP(u.Hostname()).IsLoopback() {
		t.Fatal("this test requires a loopback HTTPS relay")
	}
	read := func(name string, private bool) string {
		t.Helper()
		file := os.Getenv(name)
		if !filepath.IsAbs(file) || (private && host.CheckPrivateFile(file) != nil) {
			t.Fatal("invalid QA file for " + name)
		}
		data, err := os.ReadFile(file)
		if err != nil || len(data) == 0 || len(data) > 4096 {
			t.Fatal("cannot read QA file for " + name)
		}
		return strings.TrimSpace(string(data))
	}
	write := func(name string, data []byte) {
		t.Helper()
		file, err := os.OpenFile(filepath.Join(handoff, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, err = file.Write(data)
		if err = errors.Join(err, file.Close()); err != nil {
			t.Fatal(err)
		}
	}
	admin, phonePin := read("CAGE_TEST_ANDROID_ADMIN_FILE", true), read("CAGE_TEST_ANDROID_PIN_FILE", false)
	s, bridge, project, desktop, resumed := newMobileCLIWorkbench(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	id, err := remotelink.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	var secret [32]byte
	_, _ = rand.Read(secret[:])
	lookup := base64.RawURLEncoding.EncodeToString(secret[:])
	room, err := mobilelink.CreateDiscoverableRoom(ctx, relayOrigin, admin, lookup)
	if err != nil {
		t.Fatal(err)
	}
	pairing, err := json.Marshal(map[string]any{"version": 1, "relay": relayOrigin, "lookup_token": lookup, "host_pin": id.Pin})
	if err != nil {
		t.Fatal(err)
	}
	write("host-pin.txt", []byte(id.Pin+"\n"))
	write("target-project.txt", []byte(project+"\n"))
	write("connection.json", pairing)
	t.Cleanup(func() { _ = os.Remove(filepath.Join(handoff, "connection.json")) })
	clear(pairing)
	done := make(chan struct{})
	var serveErr error
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			serveErr = mobilelink.ServeHost(ctx, relayOrigin, room.ID, room.HostToken, id, phonePin, bridge)
			if ctx.Err() != nil || (serveErr != nil && !mobilelink.Retryable(serveErr)) {
				return
			}
			room, serveErr = mobilelink.CreateDiscoverableRoom(ctx, relayOrigin, admin, lookup)
			if serveErr != nil {
				return
			}
		}
	}()
	defer func() { cancel(); <-done }()
	t.Log("Android fixture ready; waiting for native UI actions")
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	var sessionID string
	pending, wrote, continued := false, false, false
	for {
		select {
		case <-ctx.Done():
			t.Fatal("timed out waiting for Android UI")
		case <-done:
			t.Fatalf("QA relay connection ended: %v", serveErr)
		case <-tick.C:
		}
		list, err := s.mobileCLI.summaries(s.profile, map[string]bool{project: true}, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(list) == 0 {
			continue
		}
		if len(list) != 1 || (sessionID != "" && list[0].ID != sessionID) {
			t.Fatal("Android actions must use the same single session")
		}
		sessionID = list[0].ID
		s.mobileCLI.mu.Lock()
		record, err := s.mobileCLI.record(s.profile, sessionID)
		var view mobileSession
		if err == nil {
			view = record.snapshot()
		}
		s.mobileCLI.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if len(view.Turns) == 0 {
			continue
		}
		first := view.Turns[0]
		if first.Prompt != "initial mobile prompt" || len(view.Turns) > 2 {
			t.Fatal("unexpected Android prompts")
		}
		marker := filepath.Join(project, "mobile-approved.txt")
		if !pending && first.Approval != nil {
			if first.Approval.Tool != "Bash" || !strings.Contains(string(first.Approval.Input), "mobile-approved.txt") {
				t.Fatal("unexpected pending tool request")
			}
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("project marker existed before approval")
			}
			write("approval-pending", []byte("Bash pending; project marker absent\n"))
			t.Log("Android prompt reached CLI; approval pending and marker absent")
			pending = true
		}
		if !wrote && first.Status == "completed" {
			body, err := os.ReadFile(marker)
			if !pending || err != nil || string(body) != "approved" || first.Text != "isolated tool completed" {
				t.Fatal("approved CLI turn did not produce expected file and reply")
			}
			t.Log("CLI wrote exact marker bytes after approval")
			wrote = true
		}
		if !continued && len(view.Turns) == 2 && view.Turns[1].Status == "completed" {
			if !wrote || view.Turns[1].Prompt != "follow-up" || view.Turns[1].Text != "isolated continuation" || !resumed.Load() || s.workspace() != desktop {
				t.Fatal("resumed CLI history or original desktop project did not match")
			}
			write("host-verified", []byte("same session and CLI history confirmed\n"))
			t.Log("second message retained CLI history and desktop project")
			continued = true
		}
		if continued {
			if data, err := os.ReadFile(filepath.Join(handoff, "ui-saw-continuation")); err == nil && strings.TrimSpace(string(data)) == "isolated continuation" {
				return
			}
		}
		if turn := view.Turns[len(view.Turns)-1]; turn.Status == "failed" || turn.Status == "interrupted" || turn.Status == "stopped" {
			t.Fatalf("CLI turn ended in %s: %s", turn.Status, turn.Error)
		}
	}
}
