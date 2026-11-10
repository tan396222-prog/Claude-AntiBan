//go:build !windows

package workbench

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"al.essio.dev/pkg/shellescape"
	"github.com/google/uuid"
	"github.com/sylvinus/agent-vm/internal/cliconversation"
)

func TestMobileCLIStandardGuestSettings(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	// Use the setup script's actual managed policy and a normal user's display
	// preferences. The fake helper stops at the checked launch boundary.
	probe := `
import json, os, sys, tempfile, uuid
from pathlib import Path
from unittest.mock import patch
ns = {"__name__": "identity_test"}
exec(compile(sys.stdin.read(), "mobile_cli_identity.py", "exec"), ns)
setup = Path("../../agent-vm.setup.sh").read_text()
policy = json.loads(setup.split("managed-settings.json > /dev/null\n", 1)[1].split("\nJSON", 1)[0])
with tempfile.TemporaryDirectory() as directory, patch.dict(os.environ, {"HOME": directory, "CAGE_CLAUDE_SUBJECT": "a" * 64}, clear=True):
    home = Path(directory)
    os.environ["ProgramFiles"] = str(home / "Program Files")
    managed = Path(os.environ["ProgramFiles"]) / "ClaudeCode" if os.name == "nt" else home / "managed"
    managed.mkdir(parents=True)
    (managed / "managed-settings.json").write_text(json.dumps(policy))
    config = home / ".claude"
    config.mkdir()
    (config / "settings.json").write_text(json.dumps({"theme": "dark", "skipDangerousModePermissionPrompt": True}))
    original_path = Path
    ns["Path"] = lambda value: managed if str(value) == "/etc/claude-code" else original_path(value)
    ns["Path"].home = lambda: home
    sid = str(uuid.uuid4())
    def helper_main(args, *, mobile_settings_fd, verify_mobile):
        verify_mobile({"plan": "pro"})
        return 0
    helper = {"profile": lambda account: account, "cli_environment": lambda base: dict(os.environ), "main": helper_main}
    def turn(resume):
        with tempfile.TemporaryFile() as snapshot, patch.object(ns["runpy"], "run_path", return_value=helper):
            args = ["--setting-sources", "user", "--resume" if resume else "--session-id", sid]
            ns["official_turn"]("unused", "default", args, snapshot, sid, resume)
    turn(False)
    turn(True)
    binding = json.loads((home / ".local/share/cage/mobile-cli-identities" / sid).read_text())
    assert binding[1], "standard Cage settings must support continuation"
    (managed / "managed-settings.json").write_text(json.dumps({**policy, "channelsEnabled": False}))
    turn(True)
    (managed / "managed-settings.json").write_text(json.dumps({**policy, "tui": "alternate"}))
    try:
        turn(True)
    except ns["Changed"]:
        pass
    else:
        raise AssertionError("managed policy missing from session binding")
    (managed / "managed-settings.json").write_text(json.dumps({**policy, "env": {"ANTHROPIC_BASE_URL": "https://other.invalid"}}))
    try:
        turn(True)
    except ns["Unverified"]:
        pass
    else:
        raise AssertionError("accepted managed upstream override")
    (managed / "managed-settings.json").write_text(json.dumps(policy))
    fragments = managed / "managed-settings.d"
    fragments.mkdir()
    (fragments / "routing.json").write_text(json.dumps({"apiKeyHelper": "changed"}))
    try:
        turn(True)
    except ns["Unverified"]:
        pass
    else:
        raise AssertionError("accepted managed credential helper")
`
	cmd := exec.Command(python, "-I", "-c", probe)
	cmd.Stdin = strings.NewReader(mobileCLIIdentity)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("standard guest identity: %v\n%s", err, output)
	}
}

// Exercise an actual saved transcript across two isolated API destinations.
// CCS is optional; point CAGE_TEST_CCS at a real CCS install for that subtest.
func TestMobileCLIHistoryStaysAtBoundProvider(t *testing.T) {
	binary := os.Getenv("CAGE_TEST_CLAUDE_CLI")
	if !filepath.IsAbs(binary) {
		t.Skip("set CAGE_TEST_CLAUDE_CLI to an absolute official CLI binary")
	}
	for _, mode := range []string{"environment", "user-settings", "ccs", "ccs-bearer", "official-default", "official-managed"} {
		t.Run(mode, func(t *testing.T) {
			official := strings.HasPrefix(mode, "official-")
			home, project := t.TempDir(), t.TempDir()
			bin := filepath.Join(home, "bin")
			if err := os.Mkdir(bin, 0700); err != nil {
				t.Fatal(err)
			}
			launch := shellescape.Quote(binary) + " --bare"
			if mode == "ccs-bearer" || official {
				if runtime.GOOS != "darwin" {
					t.Skip("native auth fixture requires macOS sandbox-exec to deny keychain and personal-home reads")
				}
				realHome, err := os.UserHomeDir()
				if err != nil {
					t.Fatal(err)
				}
				realBinary, err := filepath.EvalSymlinks(binary)
				if err != nil {
					t.Fatal(err)
				}
				quote := func(s string) string { v, _ := json.Marshal(s); return string(v) }
				policy := "(version 1)(allow default)(deny mach-lookup)(deny file-read* (subpath " + quote(realHome) +
					") (subpath \"/Library/Keychains\"))(allow file-read* (literal " + quote(realBinary) +
					"))(deny network-outbound)(allow network-outbound (remote ip \"localhost:*\"))"
				launch = "/usr/bin/sandbox-exec -p " + shellescape.Quote(policy) + " " + shellescape.Quote(realBinary)
			}
			shim := "#!/bin/sh\n" + `if [ -n "$CAGE_TEST_ROUTE_SOURCE" ]; then
case " $* " in *" auth status "*) ` + launch + ` "$@"
r=$?
/bin/cp "$CAGE_TEST_ROUTE_SOURCE" "$CAGE_TEST_ROUTE_TARGET" || exit 91
exit "$r";; esac
fi
` + "exec " + launch + " \"$@\"\n"
			if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(shim), 0700); err != nil {
				t.Fatal(err)
			}
			path := bin + ":/usr/bin:/bin"
			prefix := []string{"claude"}
			configDir := filepath.Join(home, "config")
			settings := filepath.Join(configDir, "settings.json")
			save := func(path string, value any) {
				t.Helper()
				data, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if strings.HasPrefix(mode, "ccs") {
				ccs := os.Getenv("CAGE_TEST_CCS")
				if !filepath.IsAbs(ccs) {
					t.Skip("set CAGE_TEST_CCS to the real ccs executable")
				}
				path = bin + ":" + filepath.Dir(ccs) + ":/usr/bin:/bin"
				if err := os.Symlink(ccs, filepath.Join(bin, "ccs")); err != nil {
					t.Fatal(err)
				}
				prefix = []string{"ccs", "cage-fixture"}
				settings = filepath.Join(home, ".ccs", "cage-fixture.settings.json")
				save(filepath.Join(home, ".ccs", "config.json"), map[string]any{"profiles": map[string]string{"cage-fixture": settings}})
			}
			var resumed atomic.Bool
			a := mobileTestModel(t, &resumed)
			defer a.Close()
			proxyURL := "http://127.0.0.1:1"
			var certFile string
			var writeSubject, writePlan func(string)
			if official {
				proxyURL, certFile = mobileOfficialTestProxy(t, a)
				slot := "default"
				configDir = filepath.Join(home, ".claude")
				if mode == "official-managed" {
					slot = "1234567890abcdef12345678"
					configDir = filepath.Join(home, ".local/share/cage/claude-accounts", slot, "config")
				}
				settings = filepath.Join(configDir, "settings.json")
				email, organization := "synthetic@example.invalid", uuid.NewString()
				prefix = []string{"env", fmt.Sprintf("CAGE_CLAUDE_SUBJECT=%x", sha256.Sum256([]byte(email+"\n"+organization))),
					"/usr/local/bin/cage-claude-account", "run", slot}
				writeSubject = func(address string) {
					state := map[string]any{"hasCompletedOnboarding": true, "oauthAccount": map[string]string{
						"accountUuid": uuid.NewString(), "emailAddress": address, "organizationUuid": organization}}
					save(filepath.Join(home, ".claude.json"), state)
					save(filepath.Join(configDir, ".claude.json"), state)
				}
				writeSubject(email)
				writePlan = func(plan string) {
					save(filepath.Join(configDir, ".credentials.json"), map[string]any{"claudeAiOauth": map[string]any{
						"accessToken": "sk-ant-oat01-fake-subscription", "refreshToken": "fake", "expiresAt": time.Now().Add(time.Hour).UnixMilli(),
						"scopes": []string{"user:inference", "user:profile"}, "subscriptionType": plan}})
				}
				writePlan("pro")
			}
			var reachedB atomic.Bool
			b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path == "/v1/messages" {
					reachedB.Store(true)
				}
				http.Error(w, "unexpected provider", http.StatusBadRequest)
			}))
			defer b.Close()
			base, key := a.URL, "sk-ant-test-account-a"
			dynamicAuth := false
			swapAfterAuth := false
			swapSource := filepath.Join(home, "alternate.json")
			save(swapSource, map[string]any{"env": map[string]string{"ANTHROPIC_BASE_URL": b.URL}})
			runner := cliconversation.Runner{Command: func(ctx context.Context, workspace string, args []string) (*exec.Cmd, error) {
				cmdline := mobileIdentityCommand(prefix, args)
				if official {
					// The fixture runs the same helper source that Cage installs inside its VM.
					helper, err := filepath.Abs("../cage/claude-account.py")
					if err != nil {
						return nil, err
					}
					for i := range cmdline {
						if cmdline[i] == "/usr/local/bin/cage-claude-account" {
							cmdline[i] = helper
						}
					}
				}
				cmd := exec.CommandContext(ctx, cmdline[0], cmdline[1:]...)
				cmd.Dir = workspace
				cmd.Env = []string{"HOME=" + home, "CCS_HOME=" + home, "PATH=" + path, "CLAUDE_CONFIG_DIR=" + configDir,
					"DISABLE_AUTOUPDATER=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
					"HTTP_PROXY=" + proxyURL, "HTTPS_PROXY=" + proxyURL, "NO_PROXY=127.0.0.1,localhost"}
				if swapAfterAuth {
					cmd.Env = append(cmd.Env, "CAGE_TEST_ROUTE_SOURCE="+swapSource, "CAGE_TEST_ROUTE_TARGET="+settings)
				}
				if official {
					// The sandbox permits only loopback. The fake TLS server stands in for the official hostname.
					cmd.Env = append(cmd.Env, "NODE_EXTRA_CA_CERTS="+certFile, "SSL_CERT_FILE="+certFile)
					env := map[string]string{}
					if base != a.URL {
						env["ANTHROPIC_BASE_URL"] = base
					}
					if key != "sk-ant-test-account-a" {
						env["ANTHROPIC_AUTH_TOKEN"] = key
					}
					cfg := map[string]any{"env": env, "theme": "dark", "skipDangerousModePermissionPrompt": true}
					if dynamicAuth {
						cfg["apiKeyHelper"] = "printf 'fake'"
					}
					save(settings, cfg)
				} else if mode == "environment" {
					cmd.Env = append(cmd.Env, "ANTHROPIC_API_KEY="+key, "ANTHROPIC_BASE_URL="+base)
				} else {
					credential := "ANTHROPIC_API_KEY"
					if mode == "ccs-bearer" {
						credential = "ANTHROPIC_AUTH_TOKEN"
					}
					config := map[string]any{"env": map[string]string{credential: key, "ANTHROPIC_BASE_URL": base, "ANTHROPIC_MODEL": "sonnet"}}
					if dynamicAuth {
						config["apiKeyHelper"] = "printf 'sk-ant-synthetic-helper'"
					}
					save(settings, config)
				}
				return cmd, nil
			}}
			ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
			defer cancel()
			id := uuid.NewString()
			run := func(prompt string, resume bool) (cliconversation.Result, error) {
				return runner.Run(ctx, cliconversation.Turn{Workspace: project, SessionID: id, Prompt: prompt, Resume: resume}, nil)
			}
			if result, err := run("initial mobile prompt", false); err != nil || !result.Started || result.SingleTurn {
				t.Fatalf("first turn: %#v %v", result, err)
			}
			verify := func() ([]byte, error) {
				cmd, err := runner.Command(ctx, project, []string{"--cage-verify", "--setting-sources", "user", "--resume", id})
				if err != nil {
					return nil, err
				}
				return cmd.Output()
			}
			if out, err := verify(); err != nil || !json.Valid(out) || !strings.Contains(string(out), `"config":`) || resumed.Load() {
				t.Fatalf("identity-only adoption check ran a turn or failed: %s %v", out, err)
			}
			swapAfterAuth = mode != "environment"
			if _, err := run("follow-up", true); err != nil || !resumed.Load() || reachedB.Load() {
				t.Fatalf("same upstream did not resume: %v", err)
			}
			if swapAfterAuth {
				raw, err := os.ReadFile(settings)
				if err != nil || !strings.Contains(string(raw), b.URL) {
					t.Fatal("test did not replace the source after auth status")
				}
				swapAfterAuth = false
			}
			base = b.URL
			if _, err := verify(); err == nil || reachedB.Load() {
				t.Fatal("adoption check accepted a changed provider")
			}
			if result, err := run("changed endpoint", true); err == nil || result.Started || reachedB.Load() {
				t.Fatalf("new endpoint reached: started=%t upstream=%t err=%v", result.Started, reachedB.Load(), err)
			}
			base = a.URL
			key = "sk-ant-test-account-b"
			if result, err := run("changed credential", true); err == nil || result.Started {
				t.Fatalf("new credential accepted: %#v %v", result, err)
			}
			binding := filepath.Join(home, ".local/share/cage/mobile-cli-identities", id)
			state, err := os.ReadFile(binding)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(state), a.URL) || strings.Contains(string(state), "sk-ant") {
				t.Fatal("binding stored raw connection details")
			}
			if official {
				key, dynamicAuth = "sk-ant-test-account-a", true
				if result, err := run("dynamic credential helper", true); err == nil || result.Started {
					t.Fatalf("accepted an external credential helper: %#v %v", result, err)
				}
				dynamicAuth = false
				writeSubject("other@example.invalid")
				if result, err := run("changed subscriber", true); err == nil || result.Started {
					t.Fatalf("accepted a different subscription subject: %#v %v", result, err)
				}
			}
			if err := os.Remove(binding); err != nil {
				t.Fatal(err)
			}
			base, key = b.URL, "sk-ant-test-account-a"
			if result, err := run("old session missing proof", true); err == nil || result.Started || reachedB.Load() {
				t.Fatalf("unbound history reached a provider: %#v %v", result, err)
			}
			if mode == "user-settings" {
				id, base, dynamicAuth = uuid.NewString(), a.URL, true
				if result, err := run("initial mobile prompt", false); err != nil || !result.Started || !result.SingleTurn {
					t.Fatalf("unverifiable provider failed its first turn: %#v %v", result, err)
				}
				base, dynamicAuth = b.URL, false
				if result, err := run("upgrade from unknown backend", true); err == nil || result.Started || reachedB.Load() {
					t.Fatalf("history from unverifiable provider escaped: %#v %v", result, err)
				}
			}
			if mode == "official-default" {
				id, base = uuid.NewString(), a.URL
				writeSubject("synthetic@example.invalid")
				writePlan("team")
				if result, err := run("initial mobile prompt", false); err != nil || !result.Started || !result.SingleTurn {
					t.Fatalf("server-managed subscription did not report its one-turn limit: %#v %v", result, err)
				}
				writePlan("pro")
				if result, err := run("downgraded plan", true); err == nil || result.Started {
					t.Fatalf("history from unknown policy resumed: %#v %v", result, err)
				}
			}
		})
	}
}

// Intercept only the official API hostname in the network-isolated fixture.
// No request from the real CLI is forwarded to an Internet host.
func mobileOfficialTestProxy(t *testing.T, model *httptest.Server) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"api.anthropic.com"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certFile := filepath.Join(t.TempDir(), "fixture-ca.pem")
	if err = os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	tlsModel := httptest.NewUnstartedServer(model.Config.Handler)
	tlsModel.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	tlsModel.StartTLS()
	t.Cleanup(tlsModel.Close)
	p := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodConnect || req.Host != "api.anthropic.com:443" {
			http.Error(w, "unknown test destination", http.StatusForbidden)
			return
		}
		upstream, err := net.Dial("tcp", tlsModel.Listener.Addr().String())
		if err != nil {
			http.Error(w, "fixture unavailable", http.StatusBadGateway)
			return
		}
		defer upstream.Close()
		client, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer client.Close()
		if _, err = io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		go func() { _, _ = io.Copy(upstream, client); _ = upstream.Close() }()
		_, _ = io.Copy(client, upstream)
	}))
	t.Cleanup(p.Close)
	return p.URL, certFile
}
