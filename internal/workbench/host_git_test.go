package workbench

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func isolatedGitConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "gitconfig")
	t.Setenv("GIT_CONFIG_GLOBAL", path)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_COUNT", "0")
	t.Setenv("GIT_CONFIG_PARAMETERS", "")
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.WriteFile(path, []byte("[user]\n\tname = Cage Test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHostGitRequiresExplicitAuthorizedConsent(t *testing.T) {
	path := isolatedGitConfig(t)
	s := testServer(t)
	original, _ := os.ReadFile(path)
	w := request(s, "GET", "/api/host/git", "", "", true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"can_enable":true`) {
		t.Fatalf("expected missing protection: %d %s", w.Code, w.Body.String())
	}
	for _, tc := range []struct {
		origin, body string
		auth         bool
		code         int
	}{
		{s.origin, `{"confirm":true}`, false, 401},
		{"https://example.com", `{"confirm":true}`, true, 403},
		{s.origin, `{}`, true, 400},
		{s.origin, `{"confirm":false}`, true, 400},
	} {
		w = request(s, "POST", "/api/host/git", tc.origin, tc.body, tc.auth)
		if w.Code != tc.code {
			t.Fatalf("unauthorized consent: %d %s", w.Code, w.Body.String())
		}
	}
	unchanged, _ := os.ReadFile(path)
	if string(unchanged) != string(original) {
		t.Fatal("read or rejected request modified global configuration")
	}
	// The lifecycle gate runs before proxy activation, downloads, or VM work.
	if err := s.startEnvironment(true); err == nil || !strings.Contains(err.Error(), "Git 安全保护") {
		t.Fatalf("first startup did not stop at host preflight: %v", err)
	}
	w = request(s, "POST", "/api/host/git", s.origin, `{"confirm":true}`, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"ready":true`) {
		t.Fatalf("explicit enable: %d %s", w.Code, w.Body.String())
	}
	saved, _ := os.ReadFile(path)
	if !strings.Contains(string(saved), "name = Cage Test") || !strings.Contains(string(saved), "bareRepository = explicit") {
		t.Fatalf("existing settings lost or protection absent: %s", saved)
	}
	w = request(s, "GET", "/api/host/git", "", "", true)
	if !strings.Contains(w.Body.String(), `"ready":true`) {
		t.Fatal("protection not read back")
	}
}

func TestHostGitWriteFailureRemainsBlocked(t *testing.T) {
	path := isolatedGitConfig(t)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	s := testServer(t)
	w := request(s, "POST", "/api/host/git", s.origin, `{"confirm":true}`, true)
	if w.Code == 200 {
		t.Fatal("unwritable Git settings accepted")
	}
	w = request(s, "GET", "/api/host/git", "", "", true)
	if strings.Contains(w.Body.String(), `"ready":true`) {
		t.Fatal("failed write allowed startup")
	}
}
