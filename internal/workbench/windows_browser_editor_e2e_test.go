package workbench

import (
	"context"
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/sylvinus/agent-vm/internal/guestos"
)

// Uses the existing, explicitly paired QA profile. Project IDs and grants come
// from the production live-share methods and remain private to this process.
func TestWindowsBrowserEditorNativeE2E(t *testing.T) {
	profile, artifacts := os.Getenv("CAGE_TEST_WINDOWS_WORKBENCH_PROFILE"), os.Getenv("CAGE_TEST_WINDOWS_BROWSER_ARTIFACTS")
	if runtime.GOOS != "windows" || profile == "" || artifacts == "" || os.Getenv("CAGE_TEST_WINDOWS_BROWSER_NATIVE") != "1" {
		t.Skip("requires the existing paired Windows QA profile and explicit native browser opt-in")
	}
	binding, err := guestos.LoadWindowsProfile(profile)
	if err != nil || binding == nil {
		t.Fatal("paired QA profile unavailable", err)
	}
	s := &Server{profile: profile, configPath: filepath.Join(profile, "env.json"), windowsGuest: binding, ready: true}
	s.lifetimeContext()
	defer s.lifeCancel()
	defer s.closeProjectShares()
	defer s.closeWindowsBrowser()
	ctx, cancel := context.WithTimeout(s.lifetimeContext(), 5*time.Minute)
	defer cancel()
	if err = os.MkdirAll(artifacts, 0700); err != nil {
		t.Fatal(err)
	}
	projectA, projectB := t.TempDir(), t.TempDir()
	for _, project := range []string{projectA, projectB} {
		// VS Code web defaults to one-second auto-save. Disable it only in
		// these newly created QA workspaces to exercise a real dirty buffer.
		if err = os.Mkdir(filepath.Join(project, ".vscode"), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(project, ".vscode", "settings.json"), []byte("{\"files.autoSave\":\"off\"}\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(project, "first.txt"), []byte("Native editor original\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err = s.ensureProjectShare(project); err != nil {
			t.Fatal("production live share failed", err)
		}
	}
	a, err := s.windowsProjectForHost(projectA)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.windowsProjectForHost(projectB)
	if err != nil {
		t.Fatal(err)
	}
	client := s.nativeBrowserClient()
	call := func(route string, input any, output any) {
		t.Helper()
		method := "POST"
		if route == "/frame" || route == "/tabs" {
			method = "GET"
		}
		data, err := client.request(ctx, method, route, input)
		if err != nil {
			t.Fatal("native browser operation failed", route, err)
		}
		if output != nil && json.Unmarshal(data, output) != nil {
			t.Fatal("native browser response invalid", route)
		}
	}
	frame := func(name string) windowsWindowFrame {
		t.Helper()
		var value windowsWindowFrame
		call("/frame", nil, &value)
		image, err := nativeFrameImage(value)
		if err != nil {
			t.Fatal("real native frame invalid", err)
		}
		file, err := os.Create(filepath.Join(artifacts, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(file, image)
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			t.Fatal("native frame evidence write failed")
		}
		return value
	}
	chord := func(id, value string, ctrl, shift bool) {
		t.Helper()
		f := frame("editor-input-target")
		if f.ID != id {
			t.Fatal("native editor input target changed")
		}
		symbol := uint32(value[0])
		if value == "Enter" {
			symbol = 0xff0d
		}
		if value == "End" {
			symbol = 0xff57
		}
		if value == "Home" {
			symbol = 0xff50
		}
		if value == "Delete" {
			symbol = 0xffff
		}
		emit := func(key uint32, down bool) {
			call("/input", map[string]any{"kind": "key", "target": f.Target, "keysym": key, "down": down}, nil)
		}
		if ctrl {
			emit(0xffe3, true)
		}
		if shift {
			emit(0xffe1, true)
		}
		emit(symbol, true)
		emit(symbol, false)
		if shift {
			emit(0xffe1, false)
		}
		if ctrl {
			emit(0xffe3, false)
		}
	}
	key := func(id, value string, ctrl bool) { chord(id, value, ctrl, false) }
	insert := func(id, value string) { call("/insert-text", map[string]any{"id": id, "text": value}, nil) }
	var first, second struct {
		ID string `json:"id"`
	}
	open := func(project *windowsProjectBinding, output any) {
		call("/editor", map[string]string{"project_id": project.ID, "grant": project.Grant}, output)
	}
	open(a, &first)
	defer func() {
		_, _ = client.request(context.Background(), "POST", "/close", map[string]string{"id": first.ID})
	}()
	time.Sleep(1500 * time.Millisecond)
	opened := frame("editor-01-open")
	for _, overlay := range opened.Overlays {
		// The captured startup restore bubble is an owned Chrome popup. Its
		// close control is visible in the upper-right part of this QA frame.
		if overlay.X > opened.Width/2 && overlay.Y == 0 && overlay.Width < opened.Width && overlay.Height < opened.Height/2 {
			for _, mask := range []int{1, 0} {
				call("/input", map[string]any{"kind": "pointer", "target": opened.Target, "x": opened.Width - 68, "y": 56, "mask": mask}, nil)
			}
			break
		}
	}
	opened = frame("editor-01-startup-ready")
	if os.Getenv("CAGE_TEST_WINDOWS_EDITOR_PREFERENCES_ONLY") == "1" {
		// Read the actual web User store, including after a server restart and
		// in a second fresh workspace origin. This branch writes only QA data.
		time.Sleep(2 * time.Second)
		frame("prefs-01-default")
		user := func(id, name string) {
			chord(id, "p", true, true)
			time.Sleep(200 * time.Millisecond)
			insert(id, "Preferences: Open User Settings (JSON)")
			time.Sleep(200 * time.Millisecond)
			key(id, "Enter", false)
			time.Sleep(500 * time.Millisecond)
			f := frame(name)
			// The official settings editor is a modal. Focus its visible
			// editor rather than assuming the command retained native focus.
			for _, mask := range []int{1, 0} {
				call("/input", map[string]any{"kind": "pointer", "target": f.Target, "x": f.Width / 3, "y": f.Height / 4, "mask": mask}, nil)
			}
		}
		user(first.ID, "prefs-02-a-empty")
		key(first.ID, "a", true)
		insert(first.ID, "{\"security.workspace.trust.enabled\":true,\"editor.fontSize\":19}\n")
		// Native typing can leave Monaco's automatically paired characters
		// after the final newline. Remove that last line with real keys.
		key(first.ID, "End", true)
		chord(first.ID, "Home", false, true)
		key(first.ID, "Delete", false)
		key(first.ID, "s", true)
		time.Sleep(500 * time.Millisecond)
		frame("prefs-03-a-saved")
		var restarted struct {
			ID string `json:"id"`
		}
		call("/editor-restart", map[string]string{"project_id": a.ID, "grant": a.Grant}, &restarted)
		if restarted.ID != first.ID {
			t.Fatal("editor preference restart replaced the owned tab")
		}
		time.Sleep(2 * time.Second)
		frame("prefs-04-a-restarted")
		user(first.ID, "prefs-05-a-read-back")
		open(b, &second)
		defer func() {
			_, _ = client.request(context.Background(), "POST", "/close", map[string]string{"id": second.ID})
		}()
		time.Sleep(2 * time.Second)
		user(second.ID, "prefs-06-b-read-back")
		t.Log("preference-only native UI evidence captured; inspect actual A saved/restarted User JSON and fresh B User JSON")
		return
	}
	// Focus the visible Explorer tree before using editor shortcuts.
	// The official workbench initially places focus in its chat pane.
	for _, mask := range []int{1, 0} {
		call("/input", map[string]any{"kind": "pointer", "target": opened.Target, "x": 300, "y": 260, "mask": mask}, nil)
	}
	time.Sleep(250 * time.Millisecond)
	key(first.ID, "p", true)
	time.Sleep(200 * time.Millisecond)
	frame("editor-01a-quick-open")
	insert(first.ID, "first.txt")
	time.Sleep(200 * time.Millisecond)
	key(first.ID, "Enter", false)
	time.Sleep(400 * time.Millisecond)
	frame("editor-02-file")
	key(first.ID, "a", true)
	insert(first.ID, "Native editor saved\n")
	key(first.ID, "s", true)
	deadline := time.Now().Add(4 * time.Second)
	for {
		data, readErr := os.ReadFile(filepath.Join(projectA, "first.txt"))
		if readErr == nil && string(data) == "Native editor saved\n" {
			break
		}
		if time.Now().After(deadline) {
			frame("editor-save-failed")
			t.Fatal("actual editor save did not reach the granted host file")
		}
		time.Sleep(100 * time.Millisecond)
	}
	insert(first.ID, "CAGE_UNSAVED_NATIVE_BUFFER")
	frame("editor-03-unsaved")
	open(b, &second)
	defer func() {
		_, _ = client.request(context.Background(), "POST", "/close", map[string]string{"id": second.ID})
	}()
	var returned struct {
		ID string `json:"id"`
	}
	open(a, &returned)
	if returned.ID != first.ID {
		t.Fatal("switching projects replaced the existing editor tab")
	}
	frame("editor-04-returned-unsaved")
	if data, _ := os.ReadFile(filepath.Join(projectA, "first.txt")); string(data) != "Native editor saved\n" {
		t.Fatal("unsaved buffer unexpectedly changed the host file")
	}
	call("/split", map[string]string{"id": first.ID, "left": first.ID, "right": second.ID}, nil)
	frame("editor-05-split")
	s.mu.Lock()
	share := s.projectShares[projectB]
	s.mu.Unlock()
	share.close()
	deadline = time.Now().Add(4 * time.Second)
	for {
		_, err = client.request(ctx, "GET", "/frame", nil)
		if err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("split still returned a frame containing the revoked editor")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if _, err = client.request(ctx, "POST", "/editor", map[string]string{"project_id": b.ID, "grant": b.Grant}); err == nil {
		t.Fatal("a retired real project grant reopened the editor")
	}
	call("/split", map[string]string{"id": first.ID}, nil)
	// This origin belongs only to the freshly created QA project. Exercise a
	// browser User preference through the official UI, separate from Cage's
	// low-priority startup default; no existing system/user profile is edited.
	chord(first.ID, "p", true, true)
	time.Sleep(200 * time.Millisecond)
	insert(first.ID, "Preferences: Open User Settings (JSON)")
	time.Sleep(200 * time.Millisecond)
	key(first.ID, "Enter", false)
	time.Sleep(400 * time.Millisecond)
	frame("editor-06-own-user-settings")
	key(first.ID, "a", true)
	insert(first.ID, "{\"security.workspace.trust.enabled\":true}\n")
	key(first.ID, "s", true)
	time.Sleep(300 * time.Millisecond)
	call("/editor-restart", map[string]string{"project_id": a.ID, "grant": a.Grant}, &returned)
	time.Sleep(1000 * time.Millisecond)
	frame("editor-07-explicit-user-true")
	if returned.ID != first.ID {
		t.Fatal("explicit editor restart replaced the owned tab")
	}
	t.Log("real native editor: production share, file open/save, same tab and dirty buffer retained across project switch, split and revoked grant denied; inspect PNGs for default unrestricted mode and explicit browser User=true after restart")
}
