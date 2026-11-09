package appupdate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/sylvinus/agent-vm/internal/host"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixtureRelease(v string, pre bool) release {
	return release{Tag: "v" + v, Prerelease: pre, Assets: []asset{{Name: "Cage-" + v + "-macos-arm64.dmg", URL: "https://github.com/" + repository + "/releases/download/v" + v + "/Cage-" + v + "-macos-arm64.dmg", Size: 3, Digest: "sha256:" + strings.Repeat("a", 64), State: "uploaded"}}}
}
func TestReleaseSelectionPreservesChannelsAndNumericPrereleases(t *testing.T) {
	rs := []release{fixtureRelease("0.2.0-alpha.9", true), fixtureRelease("0.2.0-alpha.10", true), fixtureRelease("0.1.0", false)}
	c, err := selectPlatformRelease(rs, "0.2.0-alpha.1", "darwin", "arm64")
	if err != nil || c == nil || c.Version != "0.2.0-alpha.10" {
		t.Fatalf("selected %#v: %v", c, err)
	}
	rs = append(rs, fixtureRelease("0.2.0", false), fixtureRelease("0.3.0-alpha.1", true))
	c, err = selectPlatformRelease(rs, "0.1.0", "darwin", "arm64")
	if err != nil || c == nil || c.Version != "0.2.0" {
		t.Fatalf("stable channel %#v: %v", c, err)
	}
	if c, err = selectPlatformRelease(rs, "0.3.0", "darwin", "arm64"); err != nil || c != nil {
		t.Fatalf("downgrade: %#v %v", c, err)
	}
}
func TestRejectReleaseAssetOutsideRepositoryOrOversize(t *testing.T) {
	for _, mutate := range []func(*release){
		func(r *release) { r.Assets[0].URL = "https://evil.example/Cage.dmg" },
		func(r *release) { r.Assets[0].URL = "https://github.com/Other/App/releases/download/v0.2.0/Cage.dmg" },
		func(r *release) { r.Assets[0].Size = maxArchive + 1 },
		func(r *release) { r.Tag = "v../../malicious" },
		func(r *release) { r.Draft = true },
		func(r *release) { r.Assets[0].Digest = "" },
	} {
		r := fixtureRelease("0.2.0", false)
		mutate(&r)
		if c, err := selectPlatformRelease([]release{r}, "0.1.0", "darwin", "arm64"); err != nil || c != nil {
			t.Fatalf("accepted %#v: %v", c, err)
		}
	}
}
func TestRedirectPolicyRejectsCredentialsAndHostLookalikes(t *testing.T) {
	for _, raw := range []string{"http://github.com/" + repository + "/releases/download/v1/a", "https://github.com.evil.example/a", "https://github.com@evil.example/a", "https://github.com:8443/" + repository + "/releases/download/v1/a", "https://evil.githubusercontent.com/a", "https://release-assets.githubusercontent.com.evil.example/a", "file:///tmp/Cage.app"} {
		u, _ := url.Parse(raw)
		if allowedURL(u, true) {
			t.Fatal("accepted", raw)
		}
	}
	u, _ := url.Parse("https://release-assets.githubusercontent.com/github-production-release-asset/a?signature=private")
	if !allowedURL(u, true) || allowedURL(u, false) {
		t.Fatal("CDN must be accepted only after a trusted asset redirect")
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func responseClient(body string, status int) *http.Client {
	return &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), ContentLength: -1, Request: r}, nil
	})}
}
func TestDownloadVerifiesSizeDigestAndPrivateFile(t *testing.T) {
	content := "Cage archive"
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
	a := fixtureRelease("0.2.0", false).Assets[0]
	a.Size = int64(len(content))
	for _, tc := range []struct {
		name, body, hash string
		ok               bool
	}{{"valid", content, digest, true}, {"hash", content, strings.Repeat("a", 64), false}, {"oversize", content + "x", digest, false}, {"short", content[:3], digest, false}} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "Cage.dmg")
			var received int64
			err := download(context.Background(), responseClient(tc.body, 200), a, tc.hash, path, func(n int64) { received = n })
			if tc.ok {
				b, e := os.ReadFile(path)
				if err != nil || e != nil || string(b) != content || received != a.Size {
					t.Fatalf("download %q %v %v %d", b, err, e, received)
				}
				if host.CheckPrivateFile(path) != nil {
					t.Fatal("public file")
				}
			} else {
				if err == nil {
					t.Fatal("invalid download passed")
				}
				if _, e := os.Stat(path); !os.IsNotExist(e) {
					t.Fatal("invalid archive retained")
				}
			}
		})
	}
}
func TestChecksumListMustAgreeAndBelongToSameRelease(t *testing.T) {
	r := fixtureRelease("0.2.0", false)
	a := r.Assets[0]
	c := candidate{Version: "0.2.0", Archive: a, Checksums: asset{Name: "SHA256SUMS", URL: "https://github.com/" + repository + "/releases/download/v0.2.0/SHA256SUMS"}}
	valid := strings.Repeat("a", 64) + "  " + a.Name + "\n"
	if _, err := releaseDigest(context.Background(), responseClient(valid, 200), c); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{strings.Repeat("b", 64) + "  " + a.Name, valid + valid, "invalid"} {
		if _, err := releaseDigest(context.Background(), responseClient(body, 200), c); err == nil {
			t.Fatal("invalid list passed")
		}
	}
	c.Checksums.URL = "https://github.com/" + repository + "/releases/download/v0.1.0/SHA256SUMS"
	if _, err := releaseDigest(context.Background(), responseClient(valid, 200), c); err == nil {
		t.Fatal("cross-version list passed")
	}
}
func TestPrivateReleaseAndMissingProxyDoNotReportCurrent(t *testing.T) {
	if _, err := findRelease(context.Background(), responseClient(`{"message":"Not Found"}`, 404), "0.1.0"); err == nil || !strings.Contains(err.Error(), "尚未公开") {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if code := Run(filepath.Join(t.TempDir(), "config.json"), []string{"check"}, &output); code == 0 || strings.Contains(output.String(), `"phase":"current"`) {
		t.Fatal(code, output.String())
	}
}
