package workbench

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/managedproxy"
)

const primaryLocationJSON = `{"success":true,"ip":"8.8.8.8","country":"United States","country_code":"US","region":"California","city":"Mountain View","timezone":{"id":"America/Los_Angeles"}}`
const backupLocationJSON = `{"ip":"8.8.8.8","country":"United States","country_code":"US","region":"California","region_code":"CA","city":"Mountain View","timezone":"America/Los_Angeles"}`

func locationResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestLocationFallbackUsesMeasuredIP(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"connection interrupted", 0, ""},
		{"timed out", -1, ""},
		{"rate limited", 429, ""},
		{"unavailable", 503, ""},
		{"malformed", 200, "not json"},
		{"rejected", 200, strings.Replace(primaryLocationJSON, `"success":true`, `"success":false`, 1)},
		{"wrong IP", 200, strings.Replace(primaryLocationJSON, "8.8.8.8", "9.9.9.9", 1)},
		{"missing country", 200, strings.Replace(primaryLocationJSON, `"US"`, `""`, 1)},
		{"invalid timezone", 200, strings.Replace(primaryLocationJSON, "America/Los_Angeles", "Not/AZone", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var hosts []string
			client := &http.Client{Timeout: 50 * time.Millisecond, Transport: browserTransport(func(r *http.Request) (*http.Response, error) {
				hosts = append(hosts, r.URL.Host)
				if r.URL.Scheme != "https" || r.Method != "GET" || r.Header.Get("User-Agent") != "Cage-network-check/1" {
					t.Fatal("lookup changed its fixed HTTPS request contract")
				}
				switch r.URL.Host {
				case "ipwho.is":
					if r.URL.Path != "/8.8.8.8" {
						t.Fatal("primary did not query measured IP")
					}
					if tc.status == 0 {
						return nil, errors.New("private transport detail")
					}
					if tc.status == -1 {
						<-r.Context().Done()
						return nil, r.Context().Err()
					}
					return locationResponse(tc.status, tc.body), nil
				case "api.ip.sb":
					if r.URL.Path != "/geoip/8.8.8.8" {
						t.Fatal("fallback did not query measured IP")
					}
					return locationResponse(200, backupLocationJSON), nil
				default:
					t.Fatal("unexpected location provider")
					return nil, errors.New("unexpected host")
				}
			})}
			loc, err := lookupLocation(context.Background(), client, net.ParseIP("8.8.8.8"))
			if err != nil || loc.IP != "8.8.8.8" || loc.CountryCode != "US" || loc.RegionCode != "CA" || loc.Timezone != "America/Los_Angeles" || loc.SuggestedLocale != "en_US.UTF-8" || loc.CheckedAt.IsZero() {
				t.Fatalf("fallback location: %+v, %v", loc, err)
			}
			if strings.Join(hosts, ",") != "ipwho.is,api.ip.sb" {
				t.Fatalf("incorrect fallback order: %v", hosts)
			}
		})
	}
}

func TestLocationFallbackStaysOnConfiguredProxy(t *testing.T) {
	targets := make(chan string, 2)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			t.Error("location query did not establish a proxy tunnel")
		}
		targets <- r.Host
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer proxy.Close()
	cfg := cage.DefaultConfig()
	cfg.ProxyURL = proxy.URL
	if _, err := probeLocationForIP(context.Background(), cfg, "8.8.8.8"); err == nil {
		t.Fatal("rejected proxy requests produced a location")
	}
	for _, want := range []string{"ipwho.is:443", "api.ip.sb:443"} {
		select {
		case target := <-targets:
			if target != want {
				t.Fatal("unexpected proxy destination", target)
			}
		default:
			t.Fatal("location provider bypassed configured proxy", want)
		}
	}
}

func TestSuccessfulPrimaryLocationDoesNotQueryBackup(t *testing.T) {
	client := &http.Client{Transport: browserTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "ipwho.is" {
			t.Fatal("backup queried despite valid primary")
		}
		return locationResponse(200, primaryLocationJSON), nil
	})}
	if _, err := lookupLocation(context.Background(), client, net.ParseIP("8.8.8.8")); err != nil {
		t.Fatal(err)
	}
}

func TestLocationRejectsInvalidBackupAndKeepsErrorsPrivate(t *testing.T) {
	for _, body := range []string{
		strings.Replace(backupLocationJSON, "8.8.8.8", "9.9.9.9", 1),
		strings.Replace(backupLocationJSON, "America/Los_Angeles", "Local", 1),
		strings.Replace(backupLocationJSON, "America/Los_Angeles", "", 1),
		strings.Replace(backupLocationJSON, `"US"`, `""`, 1),
	} {
		client := &http.Client{Transport: browserTransport(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host == "ipwho.is" {
				return nil, errors.New("private-secret")
			}
			return locationResponse(200, body), nil
		})}
		loc, err := lookupLocation(context.Background(), client, net.ParseIP("8.8.8.8"))
		if err == nil || loc != (Location{}) || strings.Contains(err.Error(), "private-secret") || !strings.Contains(err.Error(), "位置查询均失败") {
			t.Fatalf("invalid backup accepted or error leaked: %+v, %v", loc, err)
		}
	}
}

func TestLocationCancellationStopsFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	client := &http.Client{Transport: browserTransport(func(*http.Request) (*http.Response, error) {
		calls++
		cancel()
		return nil, context.Canceled
	})}
	_, err := lookupLocation(ctx, client, net.ParseIP("8.8.8.8"))
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("canceled lookup continued to fallback", calls, err)
	}
}

func TestLocationProvidersLiveIntegration(t *testing.T) {
	path := os.Getenv("CAGE_TEST_CONFIG")
	if path == "" {
		t.Skip("explicit proxy profile required")
	}
	cfg, err := cage.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if core := os.Getenv("CAGE_TEST_CORE"); core != "" {
		cfg = isolatedLocationProxy(t, path, core, cfg)
	}
	egress, err := cage.NewEgress(cfg)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{Proxy: nil, DialContext: egress.DialContext}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}
	t.Run("measured exit", func(t *testing.T) {
		loc, err := probeLocation(context.Background(), cfg)
		if err != nil || loc.IP == "" || loc.Timezone == "" {
			t.Fatal("measured-exit location lookup failed", err)
		}
	})
	for _, policy := range []cage.ExitPolicy{
		{Mode: "static", Scope: "country"},
		{Mode: "dynamic", Scope: "any"},
	} {
		t.Run("manual settings "+policy.Mode, func(t *testing.T) {
			manual := cfg
			manual.ExitPolicy = policy
			manual.Timezone, manual.Locale, manual.BrowserLanguage = "Asia/Shanghai", "zh_CN.UTF-8", "zh-CN"
			loc, err := probeEnvironmentLocation(context.Background(), manual, Preferences{})
			if err != nil || loc.IP == "" || loc.Timezone != "" || loc.CountryCode != "" {
				t.Fatal("manual settings required geolocation or lost measured exit", err)
			}
			if manual.Timezone != "Asia/Shanghai" || manual.Locale != "zh_CN.UTF-8" || manual.BrowserLanguage != "zh-CN" {
				t.Fatal("manual settings changed during probe")
			}
		})
	}
	t.Run("manual settings retain country verification", func(t *testing.T) {
		restricted := cfg
		restricted.ExitPolicy = cage.ExitPolicy{Mode: "dynamic", Scope: "country", CountryCode: "US"}
		loc, err := probeEnvironmentLocation(context.Background(), restricted, Preferences{})
		if err != nil || loc.CountryCode == "" || loc.Timezone == "" {
			t.Fatal("manual settings skipped required country lookup", err)
		}
	})
	t.Run("manual settings persist through validated save", func(t *testing.T) {
		s := testServer(t)
		store, err := managedproxy.Load(filepath.Dir(path))
		if err != nil {
			t.Fatal("cannot load test source")
		}
		endpoint, _ := url.Parse(cfg.ProxyURL)
		store.Port, _ = strconv.Atoi(endpoint.Port())
		store.Auth, _ = endpoint.User.Password()
		if err := store.Save(s.profile); err != nil {
			t.Fatal("cannot save isolated proxy source")
		}
		before := cfg
		before.ExitPolicy = cage.ExitPolicy{Mode: "dynamic", Scope: "any"}
		if err := writePrivate(s.configPath, before); err != nil {
			t.Fatal(err)
		}
		if err := before.RequireManagedProxy(s.profile); err != nil {
			t.Fatal("test proxy source is not applied")
		}
		after := before
		after.Timezone, after.Locale, after.BrowserLanguage = "Asia/Tokyo", "ja_JP.UTF-8", "ja-JP"
		preferences := Preferences{Keyboard: "mac"}
		if err := s.apply(savedProfile{Config: after, Preferences: preferences}, true); err != nil {
			t.Fatal("validated manual settings save failed", err)
		}
		saved, err := cage.Load(s.configPath)
		if err != nil || saved.Timezone != after.Timezone || saved.Locale != after.Locale || saved.BrowserLanguage != after.BrowserLanguage || saved.ExitPolicy != before.ExitPolicy {
			t.Fatal("manual settings were not persisted or exit rule changed")
		}
		if s.preferences.AutoTimezone || s.preferences.AutoLanguage {
			t.Fatal("manual mode was not applied")
		}
	})
	for _, provider := range []struct {
		name, url string
		primary   bool
	}{
		{"primary", "https://ipwho.is/8.8.8.8?fields=success,ip,country,country_code,region,region_code,city,timezone.id", true},
		{"backup", "https://api.ip.sb/geoip/8.8.8.8", false},
	} {
		t.Run(provider.name, func(t *testing.T) {
			if _, err := queryLocation(context.Background(), client, provider.url, provider.primary, net.ParseIP("8.8.8.8")); err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Run("forced primary failure", func(t *testing.T) {
		fallbackClient := *client
		fallbackClient.Transport = browserTransport(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host == "ipwho.is" {
				return nil, errors.New("simulated primary outage")
			}
			return transport.RoundTrip(r)
		})
		if _, err := lookupLocation(context.Background(), &fallbackClient, net.ParseIP("8.8.8.8")); err != nil {
			t.Fatal("real backup through selected proxy failed", err)
		}
	})
}

// Run the generated config with a fresh port, credentials and directory. The
// daily core and its node selection are never stopped or reconfigured.
func isolatedLocationProxy(t *testing.T, configPath, core string, cfg cage.Config) cage.Config {
	t.Helper()
	store, err := managedproxy.Load(filepath.Dir(configPath))
	if err != nil {
		t.Fatal("cannot load test proxy source")
	}
	if node, ok := store.Find(store.Selected); ok {
		server, _ := node.Config["server"].(string)
		t.Logf("Selected test node requires domain bootstrap: %t", net.ParseIP(server) == nil)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	store.Port = listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	store.Auth = managedproxy.Token()
	profile := t.TempDir()
	wire, err := store.Runtime(profile)
	if err != nil {
		t.Fatal("cannot generate isolated core config", err)
	}
	name, err := managedproxy.PhysicalInterface(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wire, err = managedproxy.BindInterface(wire, name)
	if err != nil {
		t.Fatal(err)
	}
	socketDir := filepath.Dir(managedproxy.Socket(profile))
	if err := os.MkdirAll(socketDir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	file := filepath.Join(managedproxy.Dir(profile), "config.yaml")
	if err := managedproxy.Write(file, wire); err != nil {
		t.Fatal(err)
	}
	log, err := os.OpenFile(filepath.Join(profile, "core.log"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	validate := exec.Command(core, "-t", "-d", managedproxy.Dir(profile), "-f", file)
	validate.Stdout, validate.Stderr = log, log
	if err := validate.Run(); err != nil {
		t.Fatal("bundled core rejected generated config")
	}
	cmd := exec.Command(core, "-d", managedproxy.Dir(profile), "-f", file)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal("cannot start isolated core")
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-done })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		if managedproxy.Running(ctx, profile, store) {
			cfg.ProxyURL = store.Endpoint()
			return cfg
		}
		select {
		case <-ctx.Done():
			t.Fatal("isolated proxy did not become ready")
		case <-time.After(50 * time.Millisecond):
		}
	}
}
