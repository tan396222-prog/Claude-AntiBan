package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sylvinus/agent-vm/internal/cage"
)

func TestGuestConnectionRequiresSuccessfulMatchingManifest(t *testing.T) {
	required := cage.RequiredGuestManifest()
	for _, name := range []string{"current", "missing", "old-revision", "old-protocol", "broken"} {
		t.Run(name, func(t *testing.T) {
			manifest := required
			if name == "old-revision" {
				manifest.Revision = "previous"
			}
			if name == "old-protocol" {
				manifest.Protocol++
			}
			encoded, _ := json.Marshal(manifest)
			if name == "missing" {
				encoded = []byte("null")
			}
			if name == "broken" {
				encoded = []byte("{")
			}
			credentials, current, err := parseGuestConnection(append([]byte("{\"token\":\"guest-test\"}\npassword\n"), encoded...))
			if name == "current" {
				if err != nil || current == nil || credentials.Token != "guest-test" || credentials.VNCPassword != "password" {
					t.Fatal(current, err)
				}
			} else if !errors.Is(err, errGuestUpdateRequired) {
				t.Fatalf("stale guest accepted: %v", err)
			}
		})
	}
}

func grantedToolUpdateServer(t *testing.T) (*Server, *cage.EgressGate) {
	t.Helper()
	s, gate := interfaceMonitorServer(t)
	grantTestGate(t, gate)
	s.ready = true
	required := cage.RequiredGuestManifest()
	s.guestManifest = &required
	s.setJobForTest(Job{ID: "tools", Kind: "environment-tools", Running: true})
	s.publishGate(gate.Status())
	return s, gate
}

func grantTestGate(t *testing.T, gate *cage.EgressGate) {
	t.Helper()
	state := gate.Status()
	report := cage.SafetyReport{Policy: cage.SafetyPolicyVersion, ID: "tool-update-test", RunID: state.RunID, CheckID: state.CheckID, Configuration: state.Configuration, CreatedAt: time.Now()}
	for _, id := range []string{"egress_policy", "proxy_transport", "dns_transport", "guest_settings", "browser_settings", "filesystem_boundary", "management_boundary", "fail_closed", "udp_policy", "ipv6_policy", "exit_consistency"} {
		report.Checks = append(report.Checks, cage.SafetyCheck{ID: id, Title: id, Critical: true, State: cage.CheckPassed, Evidence: "synthetic isolated fixture", CheckedAt: report.CreatedAt})
	}
	if err := gate.Grant(report); err != nil {
		t.Fatal(err)
	}
}

func TestToolUpdateLeaseHonorsPauseStopAndConfigurationAtRenewal(t *testing.T) {
	for _, jobKind := range []string{"environment-tools", "claude-desktop-install"} {
		t.Run(jobKind, func(t *testing.T) {
			for _, condition := range []string{"allowed", "paused", "stop", "configuration-changed"} {
				t.Run(condition, func(t *testing.T) {
					s, gate := grantedToolUpdateServer(t)
					s.setJobForTest(Job{ID: "tools", Kind: jobKind, Running: true})
					client, err := s.gateClient()
					if err != nil {
						t.Fatal(err)
					}
					before := gate.Status()
					// Windows can grant and renew within the same wall-clock tick.
					// Advance past the grant tick before requiring a later deadline.
					for !time.Now().Add(cage.GateLease).After(before.Until) {
						time.Sleep(time.Millisecond)
					}
					switch condition {
					case "paused":
						if err := s.pauseNetworkNow("test user pause"); err != nil {
							t.Fatal(err)
						}
					case "stop":
						s.setJobForTest(Job{ID: "stop", Kind: "stop", Running: true})
					case "configuration-changed":
						cfg, err := loadConfig(s.configPath)
						if err != nil {
							t.Fatal(err)
						}
						cfg.Timezone = "Etc/UTC"
						if cfg.Fingerprint() == before.Configuration {
							cfg.Timezone = "Asia/Tokyo"
						}
						if err := writePrivate(s.configPath, cfg); err != nil {
							t.Fatal(err)
						}
					}
					s.finishSafetyMonitor(context.Background(), client, before, "192.0.2.1", "")
					after := gate.Status()
					switch condition {
					case "allowed":
						if after.Mode != cage.GateOpen || !after.Until.After(before.Until) {
							t.Fatal("tool update lease was not renewed")
						}
					case "paused", "configuration-changed":
						if after.Mode != cage.GateClosed {
							t.Fatal("denied network was renewed")
						}
					case "stop":
						if after.Until != before.Until {
							t.Fatal("stopping job allowed a lease renewal")
						}
					}
				})
			}
		})
	}
}

func TestToolUpdateDoesNotRestorePausedOrExpiredNetworking(t *testing.T) {
	for _, paused := range []bool{true, false} {
		s, gate := grantedToolUpdateServer(t)
		if paused {
			if err := s.pauseNetworkNow("manual"); err != nil {
				t.Fatal(err)
			}
		} else {
			gate.Close("expired")
		}
		s.mu.Lock()
		allowed := s.toolsNetworkAllowedLocked()
		s.mu.Unlock()
		if !allowed {
			t.Fatal("business network pause blocked maintenance")
		}
		if gate.Status().Mode != cage.GateClosed {
			t.Fatal("tool update restored closed network")
		}
	}
}

func TestEnvironmentVersionRoutesRequireHostSession(t *testing.T) {
	s := testServer(t)
	for _, path := range []string{"/api/environment/versions", "/api/environment/update", "/api/environment/tools"} {
		method := "POST"
		if strings.HasSuffix(path, "versions") {
			method = "GET"
		}
		if w := request(s, method, path, s.origin, "{}", false); w.Code != 401 {
			t.Fatalf("untrusted version action accepted: %d", w.Code)
		}
	}
	w := request(s, "GET", "/api/environment/versions", s.origin, "", true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"stopped"`) {
		t.Fatal(w.Code, w.Body.String())
	}
}
