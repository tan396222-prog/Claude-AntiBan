package hostimage

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sylvinus/agent-vm/internal/cage"
)

func TestMacSourceReadsIdentityWithoutUserFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "system.ipsw")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	manifest, _ := archive.Create("BuildManifest.plist")
	fmt.Fprint(manifest, `<plist><dict><key>BuildIdentities</key><array><dict><key>ProductVersion</key><string>wrong</string></dict>`)
	// Universal IPSWs place their root identity after a large hardware list.
	fmt.Fprint(manifest, strings.Repeat(" ", 17<<20))
	fmt.Fprint(manifest, `</array><key>ProductVersion</key><string>26.5</string><key>ProductBuildVersion</key><string>25F71</string></dict></plist>`)
	if err = archive.Close(); err != nil {
		t.Fatal(err)
	}
	file.Close()
	host := Identity{System: "macos", Version: "26.5", Build: "25F71", Arch: "arm64"}
	plan, ok := localMacImage(host, dir)
	if !ok || plan.Path != path {
		t.Fatalf("source not found: %+v", plan)
	}
	prepared, err := Prepare(t.Context(), filepath.Join(dir, "cache"), plan, io.Discard, nil)
	if err != nil || !strings.HasPrefix(prepared.Digest, "sha256:") {
		t.Fatalf("prepare: %+v %v", prepared, err)
	}
	host.Build = "25F84"
	if _, ok := localMacImage(host, dir); ok {
		t.Fatal("accepted different macOS build")
	}
}

func TestSourceRejectsUntrustedURLsAndUnsupportedSystems(t *testing.T) {
	base := Plan{Host: Identity{System: "macos", Version: "26.5", Build: "25F71", Arch: "arm64"}, Version: "26.5", Build: "25F71", Format: "ipsw", Digest: "sha256:" + strings.Repeat("a", 64), URL: "https://updates.cdn-apple.com/system.ipsw"}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, url := range []string{"http://updates.cdn-apple.com/a", "https://updates.cdn-apple.com.evil.test/a", "https://user@updates.cdn-apple.com/a", "https://updates.cdn-apple.com:443/a"} {
		plan := base
		plan.URL = url
		if err := plan.Validate(); err == nil {
			t.Errorf("accepted %q", url)
		}
	}
	base.Host.System = "arch-linux"
	if err := base.Validate(); err == nil {
		t.Fatal("accepted unsupported source")
	}
}

func TestMacCloneRequiresGuestFileSystemSupport(t *testing.T) {
	for _, version := range []string{"14.7.6", "15.4", "26.3", "26.4", "26.5"} {
		plan := Plan{Host: Identity{System: "macos", Version: version, Build: "fixture", Arch: "arm64"}, Version: version,
			Build: "fixture", Format: "ipsw", Path: filepath.Join(t.TempDir(), "system.ipsw")}
		err := plan.Validate()
		allowed := version == "26.4" || version == "26.5"
		if (err == nil) != allowed {
			t.Fatalf("version %s: %v", version, err)
		}
	}
}

func TestSourceKeyAndSpaceAllowCompletedConcurrentDownload(t *testing.T) {
	cache := t.TempDir()
	plan := Plan{Host: Identity{System: "windows-11", Version: "25H2", Arch: "amd64", Edition: "Professional", Language: "en-US"}, Version: "25H2", Build: "26200.1", Format: "esd", URL: "http://dl.delivery.mp.microsoft.com/example.esd", Digest: "sha256:" + strings.Repeat("a", 64), Bytes: 10}
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plan.CachedPath(cache)+".partial", []byte("123456"), 0600); err != nil {
		t.Fatal(err)
	}
	if remaining := plan.RemainingBytes(cache); remaining != 4 {
		t.Fatalf("remaining=%d", remaining)
	}
	ready := plan
	ready.Path = plan.CachedPath(cache)
	ready.Modified = 123
	if ready.Key() != plan.Key() {
		t.Fatal("a completed cache invalidated the selected image")
	}
	if remaining := ready.RemainingBytes(cache); remaining != 0 {
		t.Fatalf("ready remaining=%d", remaining)
	}
	plan.Digest = ""
	if err := plan.Validate(); err == nil {
		t.Fatal("accepted HTTP media without the HTTPS catalog digest")
	}
}

func TestWindowsCatalogKeepsFeatureReleaseAndEdition(t *testing.T) {
	var catalog windowsMediaCatalog
	data := `<PublishedMedia><Files><File><FileName>26300.9457.26h2_release.esd</FileName><LanguageCode>zh-cn</LanguageCode><Edition>Professional</Edition><Architecture>x64</Architecture><Size>100</Size><Sha256>` + strings.Repeat("a", 64) + `</Sha256><FilePath>http://dl.delivery.mp.microsoft.com/windows.esd</FilePath></File></Files></PublishedMedia>`
	if err := xml.Unmarshal([]byte(data), &catalog); err != nil {
		t.Fatal(err)
	}
	host := Identity{System: "windows-11", Version: "26H2", Build: "26300.9711", Edition: "Professional", Language: "zh-CN", Arch: "amd64"}
	plan, ok := selectWindowsMedia(catalog, host)
	if !ok || plan.Build != "26300.9457" || plan.Version != "26H2" {
		t.Fatalf("catalog match: %+v %v", plan, ok)
	}
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}
	host.Build = "26200.9711"
	host.Version = "25H2"
	if _, ok := selectWindowsMedia(catalog, host); ok {
		t.Fatal("silently changed the Windows feature release")
	}
	host.Build = "26300.9711"
	host.Edition = "Enterprise"
	if _, ok := selectWindowsMedia(catalog, host); ok {
		t.Fatal("silently changed the Windows edition")
	}
}

func TestDownloadResumesAndRejectsWrongRange(t *testing.T) {
	for _, wrong := range []bool{false, true} {
		t.Run(fmt.Sprint(wrong), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Range") != "bytes=3-" {
					t.Error("did not request resume")
				}
				start := 3
				if wrong {
					start = 2
				}
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-5/6", start))
				w.WriteHeader(http.StatusPartialContent)
				fmt.Fprint(w, "def")
			}))
			defer server.Close()
			file, err := os.CreateTemp(t.TempDir(), "partial")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			file.WriteString("abc")
			var progress bytes.Buffer
			err = download(t.Context(), Plan{Host: Identity{System: "windows-11"}, URL: server.URL, Bytes: 6}, file, 3, &progress, nil)
			if wrong {
				if err == nil {
					t.Fatal("accepted wrong range")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(file.Name())
			if string(data) != "abcdef" {
				t.Fatalf("got %q", data)
			}
			if !strings.Contains(progress.String(), "CAGE_GUEST_BYTES windows-11 download 6 6") {
				t.Fatalf("missing actual resumed byte progress: %s", progress.String())
			}
		})
	}
}

func TestPrepareRejectsModifiedSourceAndPreservesIt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "system.esd")
	os.WriteFile(path, []byte("original"), 0600)
	info, _ := os.Stat(path)
	digest := sha256.Sum256([]byte("original"))
	plan := Plan{Host: Identity{System: "windows-11", Version: "25H2", Build: "26200.9", Edition: "Professional", Language: "en-US", Arch: "amd64"}, Version: "25H2", Build: "26200.8", Format: "esd", Path: path, Bytes: 8, Modified: info.ModTime().UnixNano(), Digest: "sha256:" + hex.EncodeToString(digest[:])}
	os.WriteFile(path, []byte("tampered"), 0600)
	if _, err := Prepare(t.Context(), filepath.Join(dir, "cache"), plan, io.Discard, nil); err == nil {
		t.Fatal("accepted changed source")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "tampered" {
		t.Fatal("changed user's source")
	}
}

func TestPrepareRecoversOversizedPartialWithoutDiscardingIt(t *testing.T) {
	cache := t.TempDir()
	content := []byte("valid image")
	digest := sha256.Sum256(content)
	plan := Plan{
		Host:    Identity{System: "windows-11", Version: "25H2", Build: "26200.9", Edition: "Professional", Language: "en-US", Arch: "amd64"},
		Version: "25H2", Build: "26200.8", Format: "esd", Bytes: int64(len(content)),
		URL: "http://dl.delivery.mp.microsoft.com/fixture.esd", Digest: "sha256:" + hex.EncodeToString(digest[:]),
	}
	partial := plan.CachedPath(cache) + ".partial"
	bad := []byte("oversized incomplete response")
	if err := os.WriteFile(partial, bad, 0600); err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Range") != "" {
			t.Error("retried using the oversized prefix")
		}
		_, _ = w.Write(content)
	}))
	defer server.Close()
	transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return new(net.Dialer).DialContext(ctx, network, server.Listener.Addr().String())
	}}
	defer transport.CloseIdleConnections()
	for attempt := 0; attempt < 2; attempt++ {
		ready, err := Prepare(t.Context(), cache, plan, io.Discard, transport)
		if err != nil || ready.Digest != plan.Digest || ready.Path != plan.CachedPath(cache) {
			t.Fatalf("retry did not prepare the original image: %+v %v", ready, err)
		}
	}
	retained, err := filepath.Glob(partial + ".invalid-*")
	if err != nil || len(retained) != 1 || requests != 1 {
		t.Fatalf("retained=%v requests=%d error=%v", retained, requests, err)
	}
	data, err := os.ReadFile(retained[0])
	if err != nil || !bytes.Equal(data, bad) {
		t.Fatal("discarded the failed response", err)
	}
}

// Opt-in integration uses a caller-created private directory. It never boots
// a VM or reads a user's personal data. Media downloads require a second opt-in.
func TestLiveHostSource(t *testing.T) {
	dir := os.Getenv("CAGE_TEST_HOST_SOURCE")
	if dir == "" {
		t.Skip("set CAGE_TEST_HOST_SOURCE to a private test directory")
	}
	if !filepath.IsAbs(dir) {
		t.Fatal("absolute test directory required")
	}
	host, err := Host(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if IsLinux(host.System) {
		t.Skip("Linux live source uses bundled template through the workbench")
	}
	var transport *http.Transport
	if config := os.Getenv("CAGE_TEST_HOST_CONFIG"); config != "" {
		cfg, err := cage.Load(config)
		if err != nil {
			t.Fatal(err)
		}
		egress, err := cage.NewEgress(cfg)
		if err != nil {
			t.Fatal(err)
		}
		transport = &http.Transport{Proxy: nil, DialContext: egress.DialContext}
		defer transport.CloseIdleConnections()
	}
	var roundTripper http.RoundTripper
	if transport != nil {
		roundTripper = transport
	}
	plan, err := Resolve(t.Context(), host, dir, dir, nil, roundTripper)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.MarshalIndent(plan, "", "  ")
	t.Log(runtime.GOOS, string(data))
	if err = os.WriteFile(filepath.Join(dir, "plan.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("CAGE_TEST_HOST_SOURCE_DOWNLOAD") != "1" {
		return
	}
	plan, err = Prepare(t.Context(), dir, plan, os.Stdout, roundTripper)
	if err != nil {
		t.Fatal(err)
	}
	data, _ = json.MarshalIndent(plan, "", "  ")
	if err = os.WriteFile(filepath.Join(dir, "plan.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}
