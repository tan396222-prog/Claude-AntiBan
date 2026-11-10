package workbench

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/sylvinus/agent-vm/internal/guestos"
	"github.com/sylvinus/agent-vm/internal/hostimage"
)

func TestHostClonePreviewReusesPendingChoiceAndExistingDisk(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("fixture uses the Apple Silicon installation kit")
	}
	s := testServer(t)
	s.exe = filepath.Join(s.profile, "bin", "cage")
	for _, name := range []string{"prepare-macos-media.sh", "provision-macos-workbench.sh", "project/cage-sshfs", "native-browser/macos-native-browser.mjs", "cage-finish-initial-setup"} {
		path := filepath.Join(filepath.Dir(s.exe), "guest-os", name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	plan := hostimage.Plan{
		Host:    hostimage.Identity{System: "macos", Version: "26.5", Build: "25F71", Arch: "arm64"},
		Version: "26.5", Build: "25F71", Format: "ipsw", Bytes: 1 << 30,
		URL: "https://updates.cdn-apple.com/fixture.ipsw", Digest: "sha256:" + strings.Repeat("a", 64),
	}
	install := guestos.Installation{Version: 1, ID: uuid.New(), System: "macos", Source: plan}
	if err := guestos.SaveInstallation(s.profile, install); err != nil {
		t.Fatal(err)
	}
	for _, started := range []bool{false, true} {
		if started {
			if err := os.MkdirAll(filepath.Join(s.profile, "state", "lima", "mac"), 0700); err != nil {
				t.Fatal(err)
			}
		}
		r := httptest.NewRequest(http.MethodGet, "/api/environment/guest-systems/clone", nil)
		r.AddCookie(&http.Cookie{Name: s.cookieName() + "-desktop", Value: trustedViewProof(s.token)})
		w := httptest.NewRecorder()
		s.hostClone(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("preview: %d %s", w.Code, w.Body.String())
		}
		var result struct {
			Plan                hostimage.Plan `json:"plan"`
			MinimumFreeGiB      int            `json:"minimum_free_gib"`
			InstallationStarted bool           `json:"installation_started"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		minimum := 33
		if started {
			minimum = 1
		}
		if result.Plan != plan || result.MinimumFreeGiB != minimum || result.InstallationStarted != started {
			t.Fatalf("pending source or remaining disk budget changed: %+v", result)
		}
	}
}

func TestPendingLinuxCloneRetryReturnsToInstallerAdmission(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("this case verifies rejection on a different host family")
	}
	s := testServer(t)
	install := guestos.Installation{
		Version: 1, ID: uuid.New(), System: "ubuntu-22.04",
		Source: hostimage.Plan{
			Host:    hostimage.Identity{System: "ubuntu-22.04", Version: "22.04", Arch: "amd64"},
			Version: "22.04", Format: "qcow2", Path: filepath.Join(s.profile, "ubuntu.img"),
			Digest: "sha256:" + strings.Repeat("a", 64),
		},
	}
	if err := guestos.SaveInstallation(s.profile, install); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{}`, `{"guest_system":"ubuntu-22.04"}`} {
		r := httptest.NewRequest(http.MethodPost, "/api/start", strings.NewReader(body))
		r.AddCookie(&http.Cookie{Name: s.cookieName() + "-desktop", Value: trustedViewProof(s.token)})
		w := httptest.NewRecorder()
		s.start(w, r)
		if w.Code == http.StatusOK || s.jobs.snapshot().Running {
			t.Fatal("pending clone bypassed installer admission through the generic start path")
		}
	}
}
