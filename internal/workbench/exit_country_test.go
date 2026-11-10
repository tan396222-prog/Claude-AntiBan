package workbench

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sylvinus/agent-vm/internal/cage"
)

func TestLocationObservationRevokesUnsafeExitAndIgnoresPreviousRoute(t *testing.T) {
	for _, condition := range []string{"mainland", "listed IP", "unknown", "previous route"} {
		t.Run(condition, func(t *testing.T) {
			s, gate := grantedToolUpdateServer(t)
			cfg, _ := loadConfig(s.configPath)
			loc := Location{IP: "8.8.8.8", CountryCode: "CN", CheckedAt: time.Now()}
			s.locations = map[string]Location{"8.8.8.8": {IP: "8.8.8.8", CountryCode: "US", CheckedAt: time.Now()}}
			var probeErr error
			switch condition {
			case "listed IP":
				loc.IP, loc.CountryCode = "223.5.5.5", "US"
			case "unknown":
				loc.CountryCode, probeErr = "", errors.New("location unavailable")
			case "previous route":
				cfg.ProxyRevision = "previous-route"
			}
			if err := s.finishLocationProbe(cfg, loc, probeErr); err != nil {
				t.Fatal(err)
			}
			want := cage.GateClosed
			if condition == "previous route" {
				want = cage.GateOpen
			}
			if gate.Status().Mode != want {
				t.Fatalf("gate mode=%s want=%s", gate.Status().Mode, want)
			}
			if condition == "unknown" || condition == "mainland" {
				if _, exists := s.locations["8.8.8.8"]; exists {
					t.Fatal("stale country evidence survived a new rejection")
				}
			}
		})
	}
}

func TestExitCountryCannotBeSkippedByStaticOrUnrestrictedMode(t *testing.T) {
	for _, policy := range []cage.ExitPolicy{{Mode: "static", IP: "8.8.8.8"}, {Mode: "dynamic", Scope: "any"}} {
		t.Run(policy.Mode, func(t *testing.T) {
			s := testServer(t)
			var calls atomic.Int32
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			defer proxy.Close()
			cfg := cage.DefaultConfig()
			cfg.ProxyURL, cfg.ExitPolicy = proxy.URL, policy
			if state, _ := s.checkExitPolicy(context.Background(), cfg, "8.8.8.8"); state != cage.CheckUnknown || calls.Load() == 0 {
				t.Fatal("unavailable country lookup did not block or bypassed proxy")
			}
			s.locations = map[string]Location{"8.8.8.8": {IP: "8.8.8.8", CountryCode: "CN", CheckedAt: time.Now()}}
			if state, _ := s.checkExitPolicy(context.Background(), cfg, "8.8.8.8"); state != cage.CheckFailed {
				t.Fatal("mainland geolocation accepted")
			}
			s.locations["8.8.8.8"] = Location{IP: "8.8.8.8", CountryCode: "US", CheckedAt: time.Now()}
			if state, _ := s.checkExitPolicy(context.Background(), cfg, "8.8.8.8"); state != cage.CheckPassed {
				t.Fatal("verified non-mainland exit rejected")
			}
		})
	}
}
