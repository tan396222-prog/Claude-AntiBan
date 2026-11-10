package workbench

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrowserPreferencesPatchAndFailure(t *testing.T) {
	s := testServer(t)
	s.ready = true
	s.preferences.ClaudeTerminalCommand = "claude --continue"
	s.preferences.Workspace = t.TempDir()
	var homes []string
	s.browserControl = browserClientForTest(t, s, browserTransport(func(r *http.Request) (*http.Response, error) {
		var input browserInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		if r.URL.Path != "/homepage" {
			t.Fatalf("unexpected lifecycle operation %s", r.URL.Path)
		}
		homes = append(homes, input.URL)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Header: http.Header{}}, nil
	}))
	response := request(s, "POST", "/api/browser/preferences", s.origin, `{"browser_home_url":"https://example.test/new"}`, true)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	if s.preferences.BrowserHomeURL != "https://example.test/new" || s.preferences.ClaudeTerminalCommand != "claude --continue" {
		t.Fatal("unrelated preferences changed")
	}
	if !s.ready {
		t.Fatal("browser preference stopped environment")
	}
	before := s.preferences
	for _, body := range []string{`{"browser_home_url":"file:///etc/passwd"}`, `{"browser_home_url":"https://user:secret@example.test"}`, `{"browser_home_url":"javascript:alert(1)"}`} {
		if w := request(s, "POST", "/api/browser/preferences", s.origin, body, true); w.Code != 400 {
			t.Fatal("invalid URL accepted", w.Code)
		}
	}
	if len(homes) != 1 {
		t.Fatal("invalid URL reached guest")
	}
	if err := os.Remove(filepath.Join(s.profile, "workbench.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(s.profile, "workbench.json"), 0700); err != nil {
		t.Fatal(err)
	}
	response = request(s, "POST", "/api/browser/preferences", s.origin, `{"browser_home_url":"https://example.test/rejected"}`, true)
	if response.Code == 200 || s.preferences != before {
		t.Fatal("failed write committed preferences")
	}
	if strings.Join(homes, ",") != "https://example.test/new,https://example.test/rejected,https://example.test/new" {
		t.Fatal("guest homepage was not rolled back", homes)
	}
	s.account.lock()
	if w := request(s, "GET", "/api/browser/preferences", "", "", true); w.Code != 423 {
		t.Fatal("locked preferences accessible")
	}
}

func TestLocalExtensionImportCopiesProgramOnly(t *testing.T) {
	root := t.TempDir()
	profile := filepath.Join(root, "Default")
	id := strings.Repeat("a", 32)
	directory := filepath.Join(profile, "Extensions", id, "1.0_0")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "Local State"), `{"profile":{"info_cache":{"Default":{"name":"工作配置"}}}}`)
	write(filepath.Join(profile, "Secure Preferences"), `{"extensions":{"settings":{"`+id+`":{"state":1,"path":"`+id+`/1.0_0"}}}}`)
	write(filepath.Join(directory, "manifest.json"), `{"manifest_version":3,"name":"Test extension","version":"1.0","permissions":["storage"],"content_scripts":[{"matches":["https://example.test/*"],"js":["main.js"]}]}`)
	write(filepath.Join(directory, "main.js"), `globalThis.testExtension=true`)
	write(filepath.Join(profile, "Cookies"), "PRIVATE_COOKIE_FIXTURE")
	write(filepath.Join(profile, "Preferences"), `{"unrelated_secret":"PRIVATE_PROFILE_FIXTURE"}`)
	profiles := scanLocalBrowserProfiles([]localBrowserRoot{{"Edge", root}})
	if len(profiles) != 1 || profiles[0].Name != "工作配置" || len(profiles[0].Extensions) != 1 {
		t.Fatalf("scan: %+v", profiles)
	}
	extension := profiles[0].Extensions[0]
	if !extension.Enabled || !extension.StateKnown || !extension.Compatible || len(extension.HostPermissions) != 1 {
		t.Fatal("import metadata", extension)
	}
	encoded, _ := json.Marshal(profiles)
	if bytes.Contains(encoded, []byte(root)) || bytes.Contains(encoded, []byte("PRIVATE_")) {
		t.Fatal("inventory exposed filesystem path or profile data")
	}
	data, err := packageLocalExtension(extension)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if len(archive.File) != 2 {
		t.Fatalf("unexpected packaged files: %d", len(archive.File))
	}
	for _, file := range archive.File {
		if file.Name != "manifest.json" && file.Name != "main.js" {
			t.Fatal("unexpected file", file.Name)
		}
	}
}

func TestImportedExtensionStates(t *testing.T) {
	enabled, disabled := 1, 0
	for _, tc := range []struct {
		state          *int
		reasons        string
		enabled, known bool
	}{
		{nil, `[]`, true, true}, {nil, `["1"]`, false, true}, {nil, `0`, true, true}, {nil, `8`, false, true},
		{&enabled, ``, true, true}, {&disabled, ``, false, true}, {nil, ``, false, false}, {&disabled, `[]`, false, true},
	} {
		actual, known := importedExtensionState(tc.state, json.RawMessage(tc.reasons))
		if actual != tc.enabled || known != tc.known {
			t.Fatalf("state %v reasons %q = %v,%v", tc.state, tc.reasons, actual, known)
		}
	}
}
