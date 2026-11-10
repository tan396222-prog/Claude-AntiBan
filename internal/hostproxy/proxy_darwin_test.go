//go:build darwin && cgo

package hostproxy

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMacPACResolutionUsesNativeCodeWithoutChangingSystemSettings(t *testing.T) {
	// Compile the production CFNetwork bridge in an isolated harness. This lets
	// real PAC evaluation run without replacing the developer's system proxy.
	source, err := os.ReadFile("proxy_darwin.go")
	if err != nil {
		t.Fatal(err)
	}
	_, sourceC, ok := strings.Cut(string(source), "/*")
	if !ok {
		t.Fatal("missing C bridge")
	}
	sourceC, _, ok = strings.Cut(sourceC, "*/")
	if !ok {
		t.Fatal("missing C bridge end")
	}
	var lines []string
	for _, line := range strings.Split(sourceC, "\n") {
		if !strings.HasPrefix(line, "#cgo ") {
			lines = append(lines, line)
		}
	}
	sourceC = strings.Join(lines, "\n")
	sourceC += `
#include <stdio.h>
int main(int argc, char **argv) {
    CFStringRef pacText = CFStringCreateWithCString(NULL, argv[1], kCFStringEncodingUTF8);
    CFURLRef pacURL = CFURLCreateWithString(NULL, pacText, NULL);
    const void *keys[] = { kCFProxyTypeKey, kCFProxyAutoConfigurationURLKey };
    const void *values[] = { kCFProxyTypeAutoConfigurationURL, pacURL };
    CFDictionaryRef proxy = CFDictionaryCreate(NULL, keys, values, 2, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    const void *items[] = {proxy};
    CFArrayRef proxies = CFArrayCreate(NULL, items, 1, &kCFTypeArrayCallBacks);
    CFURLRef target = CFURLCreateWithString(NULL, CFSTR("https://cloud.debian.org/image"), NULL);
    CageProxy result = cageResolveArray(proxies, target, 1);
    if (result.failed) return 2;
    puts(result.url ? result.url : "DIRECT");
    free(result.url);
    CFRelease(target); CFRelease(proxies); CFRelease(proxy); CFRelease(pacURL); CFRelease(pacText);
    return 0;
}`
	program := filepath.Join(t.TempDir(), "pac-check")
	compile := exec.Command("clang", "-x", "c", "-", "-framework", "CoreFoundation", "-framework", "CFNetwork", "-o", program)
	compile.Stdin = strings.NewReader(sourceC)
	if output, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile native PAC harness: %v\n%s", err, output)
	}
	for _, test := range []struct {
		name, script, want string
		failure            bool
	}{
		{"http", `function FindProxyForURL(url,host){return "PROXY 127.0.0.1:7896";}`, "http://127.0.0.1:7896", false},
		{"direct", `function FindProxyForURL(url,host){return "DIRECT";}`, "DIRECT", false},
		{"socks", `function FindProxyForURL(url,host){return "SOCKS 127.0.0.1:7896";}`, "socks5://127.0.0.1:7896", false},
		{"broken-script", `this is not a PAC script`, "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
				w.Write([]byte(test.script))
			}))
			defer server.Close()
			output, err := exec.CommandContext(t.Context(), program, server.URL+"/proxy.pac").CombinedOutput()
			if test.failure {
				if err == nil {
					t.Fatalf("broken PAC became direct: %s", output)
				}
				return
			}
			if err != nil || !bytes.Equal(bytes.TrimSpace(output), []byte(test.want)) {
				t.Fatalf("result=%q err=%v want=%q", output, err, test.want)
			}
		})
	}
}
