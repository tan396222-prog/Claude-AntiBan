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

func TestUbuntuGuestChoicesMatchSupportedHostArchitectures(t *testing.T) {
	for _, host := range []struct {
		platform, arch string
		available      bool
	}{
		{"darwin", "arm64", true}, {"windows", "amd64", true}, {"linux", "amd64", true},
		{"darwin", "amd64", false}, {"windows", "arm64", false}, {"linux", "arm64", false},
	} {
		if got := linuxGuestSystems(host.platform, host.arch); (len(got) == 1 && got[0] == guestos.Ubuntu2204) != host.available {
			t.Fatalf("%s/%s: %v", host.platform, host.arch, got)
		}
	}
}

func TestUbuntuCatalogPlanPreservesArchitectureAndResumeChoice(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		plan, err := linuxGuestImagePlan(t.Context(), t.TempDir(), guestos.Ubuntu2204, arch)
		if err != nil || plan.Host.Arch != arch || !strings.Contains(plan.URL, "-"+arch+".img") || plan.Bytes <= 0 {
			t.Fatalf("%s: %+v %v", arch, plan, err)
		}
	}
	s := testServer(t)
	plan, err := linuxGuestImagePlan(t.Context(), s.profile, guestos.Ubuntu2204, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	install := guestos.Installation{Version: 1, ID: uuid.New(), System: guestos.Ubuntu2204, Source: plan, SourceKind: guestos.LinuxImageSource}
	if err := guestos.SaveInstallation(s.profile, install); err != nil {
		t.Fatal(err)
	}
	view, err := s.guestInstallView()
	if err != nil || view.Pending == nil || view.Pending.SourceKind != guestos.LinuxImageSource || !view.Reselect {
		t.Fatalf("cross-host resume: %+v %v", view, err)
	}
	if err := os.MkdirAll(filepath.Join(s.profile, "state", "lima", "base-building"), 0700); err != nil {
		t.Fatal(err)
	}
	view, err = s.guestInstallView()
	if err != nil || view.Reselect {
		t.Fatalf("started disk can be reselected: %+v %v", view, err)
	}
}

func TestUbuntuSelectionUsesInstallerAndRequiresTrustedDesktop(t *testing.T) {
	for _, trusted := range []bool{false, true} {
		s := testServer(t)
		// Keep accepted work from touching a real VM or network in this admission test.
		s.op.Lock()
		r := httptest.NewRequest(http.MethodPost, "/api/start", strings.NewReader(`{"guest_system":"ubuntu-22.04"}`))
		r.Header.Set("Content-Type", "application/json")
		if trusted {
			r.AddCookie(&http.Cookie{Name: s.cookieName() + "-desktop", Value: trustedViewProof(s.token)})
		}
		w := httptest.NewRecorder()
		s.start(w, r)
		job := s.jobs.snapshot()
		s.jobs.cancelRunning()
		s.op.Unlock()
		s.jobs.workers.Wait()
		if trusted {
			var response Job
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Kind != "guest-install" || job.Kind != "guest-install" {
				t.Fatalf("Ubuntu went through generic Debian startup: %d %s", w.Code, w.Body.String())
			}
		} else if w.Code == http.StatusOK {
			t.Fatal("untrusted page started installation")
		}
	}
}

func TestUbuntuBoundSystemCannotBeStartedAsDebian(t *testing.T) {
	s := testServer(t)
	plan, err := linuxGuestImagePlan(t.Context(), s.profile, guestos.Ubuntu2204, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	plan.Path = filepath.Join(s.profile, "ubuntu.img")
	if err := hostimage.WriteLinuxTemplate(filepath.Join(s.profile, "state"), plan); err != nil {
		t.Fatal(err)
	}
	w := request(s, http.MethodPost, "/api/start", s.origin, `{"guest_system":"debian-13"}`, true)
	if w.Code != http.StatusConflict || s.jobs.snapshot().ID != "" {
		t.Fatalf("changed existing Ubuntu: %d %s", w.Code, w.Body.String())
	}
}
