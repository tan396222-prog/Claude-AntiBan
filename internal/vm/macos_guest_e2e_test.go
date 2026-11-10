//go:build darwin && arm64 && cgo && e2e && !no_vz

package vm

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"al.essio.dev/pkg/shellescape"
	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/guestos"
	"github.com/sylvinus/agent-vm/internal/managedproxy"
)

// Run the signed test binary with CAGE_TEST_MACOS_IPSW pointing to an Apple
// restore image. CAGE_TEST_CLAUDE_DMG optionally exercises the official app.
func TestMacOSGuest(t *testing.T) {
	image := os.Getenv("CAGE_TEST_MACOS_IPSW")
	if image == "" {
		t.Skip("set CAGE_TEST_MACOS_IPSW to a local Apple restore image")
	}
	if !filepath.IsAbs(image) {
		t.Fatal("CAGE_TEST_MACOS_IPSW must be absolute")
	}
	if info, err := os.Stat(image); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("invalid restore image %q: %v", image, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	profile, err := os.MkdirTemp("/tmp", "cgmac-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if t.Failed() && os.Getenv("CAGE_TEST_KEEP_FAILED_VM") == "1" {
			t.Log("Retained this test's private VM for diagnosis:", profile)
			return
		}
		if err := os.RemoveAll(profile); err != nil {
			t.Error(err)
		}
	})
	configPath := filepath.Join(profile, "config.json")
	state := filepath.Join(profile, "state")
	t.Setenv(cage.ConfigEnv, configPath)
	t.Setenv("AGENT_VM_STATE_DIR", state)
	t.Setenv("LIMA_HOME", filepath.Join(state, "lima"))
	t.Setenv("LIMA_CACHE_HOME", filepath.Join(profile, "cache"))
	t.Setenv("AGENT_VM_UNSAFE_OPEN_NETWORK", "")
	t.Setenv("AGENT_VM_UNSAFE_WRITABLE_GIT", "")
	t.Setenv("AGENT_VM_UNSAFE_DISABLE_SECURITY_PROMPTS", "")
	// Use a private, unstarted proxy configuration. Installation uses local
	// media and SSH. No part of this test requires external guest traffic.
	proxy, err := managedproxy.Load(profile)
	if err != nil {
		t.Fatal(err)
	}
	if err := proxy.Import("offline", "", []byte("proxies: [{name: offline, type: socks5, server: 127.0.0.1, port: 9}]"), "offline", 0, ""); err != nil {
		t.Fatal(err)
	}
	proxy.Selected = proxy.Sources[0].Nodes[0].ID
	proxy.Revision = "offline"
	if err := proxy.Save(profile); err != nil {
		t.Fatal(err)
	}
	cfg := cage.DefaultConfig()
	cfg.ProxyURL, cfg.ProxyRevision = proxy.Endpoint(), proxy.Revision
	writeJSON := func(path string, value any) {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeJSON(configPath, cfg)
	writeJSON(cage.ResourceAllocationPath(configPath), cage.ResourceAllocation{MemoryGiB: 4, CPUs: 2})
	l, err := NewLima(filepath.Join(state, "lima"), state)
	if err != nil {
		t.Fatal(err)
	}
	const name = "mac"
	if err := l.CreateMacOSFromIPSW(ctx, name, image, Settings{CPUs: 2, MemoryGiB: 4, DiskGiB: 64}, os.Stderr); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := l.Stop(cleanup, name); err != nil {
			t.Error(err)
		}
	})
	t.Log("Restoring a new macOS test VM from local media")
	if err := l.Start(ctx, name, os.Stderr); err != nil {
		t.Fatal(err)
	}
	run := func(script string, input io.Reader) string {
		t.Helper()
		var out bytes.Buffer
		code, err := l.Shell(ctx, name, ShellOpts{Args: []string{"/bin/sh", "-ec", script}, Stdin: input, Stdout: &out, Stderr: &out})
		if err != nil || code != 0 {
			t.Fatalf("guest command failed (%d): %v\n%s", code, err, out.String())
		}
		return strings.TrimSpace(out.String())
	}
	t.Log(run("test \"$(uname -s)\" = Darwin && /usr/bin/sw_vers && test \"$HOME\" = /Users/cageprovision", nil))
	run("cd \"$HOME\" && /usr/bin/osacompile -l JavaScript -o cage-qa.scpt -e '6*7' && test \"$(/usr/bin/osascript cage-qa.scpt)\" = 42", nil)
	serialKey, err := guestos.MacOSInstallHostKey(profile)
	if err != nil {
		t.Fatal(err)
	}
	guestKey := run("cat /etc/ssh/ssh_host_ed25519_key.pub", nil)
	if !strings.HasPrefix(guestKey+" ", strings.TrimSpace(string(serialKey))+" ") {
		t.Fatal("VZ serial key differs from the restored guest identity")
	}
	if kit := os.Getenv("CAGE_TEST_MACOS_AUTOMATION_KIT"); kit != "" {
		// Minimal first-run experiment: the production account receipt and session
		// configuration run in a newly restored VM, with no daily guest data.
		tar := exec.CommandContext(ctx, "/usr/bin/tar", "--no-mac-metadata", "-cf", "-", "-C", kit, ".")
		pipe, err := tar.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := tar.Start(); err != nil {
			t.Fatal(err)
		}
		run(`mkdir "$HOME/automation-kit"; cd "$HOME/automation-kit"; tar -xf -`, pipe)
		if err := tar.Wait(); err != nil {
			t.Fatal(err)
		}
		password := "Aa7!" + uuid.NewString()
		run(`umask 077; cat > "$HOME/automation-input"`, strings.NewReader(password+"\n"))
		admin := func(script string) string {
			return run("sudo -S -p '' /bin/bash -ec "+shellescape.Quote(script)+` < "$HOME/password"`, nil)
		}
		installation := uuid.NewString()
		admin(`echo macos > /var/db/cage-guest-os; chmod 600 /var/db/cage-guest-os; chown -R root:wheel /Users/cageprovision/automation-kit;
printf '%s\n' "$(cat /Users/cageprovision/automation-input)" | sysadminctl -addUser cage -fullName cage -password - -home /Users/cage;
createhomedir -c -u cage >/dev/null;
/Users/cageprovision/automation-kit/cage-finish-initial-setup prepare ` + installation + ` < /Users/cageprovision/automation-input;
/bin/bash /Users/cageprovision/automation-kit/configure-macos-session.sh cage ` + installation + ` < /Users/cageprovision/automation-input`)
		run(`test "$(sudo -S -p '' defaults read /Library/Preferences/com.apple.loginwindow autoLoginUser < "$HOME/password")" = cage`, nil)
		if err := l.Stop(ctx, name); err != nil {
			t.Fatal(err)
		}
		if err := l.Start(ctx, name, os.Stderr); err != nil {
			t.Fatal(err)
		}
		loggedIn := false
		for i := 0; i < 60; i++ {
			if run(`stat -f '%Su' /dev/console`, nil) == "cage" {
				loggedIn = true
				break
			}
			time.Sleep(time.Second)
		}
		if !loggedIn {
			t.Fatal("guest did not automatically log in")
		}
		admin(`! pgrep -x 'Setup Assistant' >/dev/null; uid=$(id -u cage); launchctl print gui/$uid >/dev/null;
/Users/cageprovision/automation-kit/cage-finish-initial-setup;
! id -Gn cage | grep -qw admin; ! id -Gn cagesetup | grep -qw admin;
test "$(stat -f %OLp /etc/kcpassword)" = 600`)
		t.Log("Fresh guest: automatic graphical login, skipped optional Setup Assistant, standard account and retired temporary administrator verified")
		return
	}
	if kit, media := os.Getenv("CAGE_TEST_MACOS_WORKBENCH_KIT"), os.Getenv("CAGE_TEST_MACOS_WORKBENCH_MEDIA"); kit != "" && media != "" {
		// Exercise the shipped provisioning script in this disposable fresh VM.
		tar := exec.CommandContext(ctx, "/usr/bin/tar", "--no-mac-metadata", "-cf", "-", "-C", kit, ".", "-C", media, "node.tgz", "editor.tgz", "playwright.tgz", "chrome.dmg", "claude.dmg", "cli", "macfuse.pkg")
		pipe, err := tar.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		tar.Stderr = os.Stderr
		if err := tar.Start(); err != nil {
			t.Fatal(err)
		}
		run(`mkdir "$HOME/kit"; cd "$HOME/kit"; tar -xf -; mv macfuse.pkg project/macfuse.pkg`, pipe)
		if err := tar.Wait(); err != nil {
			t.Fatal(err)
		}
		password := "Aa7!" + uuid.NewString()
		run(`umask 077; cat > "$HOME/login-input"`, strings.NewReader(password+"\n"))
		admin := func(script string) string {
			return run("sudo -S -p '' /bin/bash -ec "+shellescape.Quote(script)+` < "$HOME/password"`, nil)
		}
		stage := admin(`mktemp -d /private/var/tmp/cage-install.XXXXXXXX`)
		admin(`mv /Users/cageprovision/kit ` + stage + `/kit; chown -R root:wheel ` + stage + `; cd /; /bin/bash ` + stage + `/kit/provision-macos-workbench.sh ` + stage + `/kit ` + uuid.NewString() + ` cageprovision < /Users/cageprovision/login-input`)
		run(`/usr/bin/dscl /Local/Default -authonly cage >/dev/null`, strings.NewReader(password+"\n"))
		admin(`test "$(dscl . -read /Users/cage NFSHomeDirectory)" = 'NFSHomeDirectory: /Users/cage'; if id -Gn cage | grep -qw admin; then exit 1; fi; dscl . -read /Users/cageprovision IsHidden | grep -q ": 1$"; /usr/local/libexec/cage-finish-initial-setup`)
		admin(`if id -Gn cagesetup | grep -qw admin; then exit 1; fi; dscl . -read /Users/cagesetup UserShell | grep -q /usr/bin/false`)
		t.Log("Fresh cage account, user-selected password, ordinary privileges and temporary administrator retirement verified")
	}
	verifyMacOSGuestNetwork(t, ctx, filepath.Join(state, "lima", name), run)
	verifyMacOSProjectTransfer(t, ctx, l, name)
	if err := l.Stop(ctx, name); err != nil {
		t.Fatal(err)
	}
	if err := l.Start(ctx, name, os.Stderr); err != nil {
		t.Fatal(err)
	}
	run("test \"$(/usr/bin/osascript \"$HOME/cage-qa.scpt\")\" = 42", nil)
	t.Log("Native compilation, closed external connection and persistent disk verified")
	if dmg := os.Getenv("CAGE_TEST_CLAUDE_DMG"); dmg != "" {
		h, err := os.ReadFile("../../scripts/guest-os/claude-desktop-macos.sh")
		if err != nil {
			t.Fatal(err)
		}
		run("umask 077; cat > \"$HOME/claude-desktop-qa.sh\"", bytes.NewReader(h))
		f, err := os.Open(dmg)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		run("umask 077; cat > \"$HOME/Claude.dmg\"", f)
		// Lima generates a random password inside this new VM. Use it only
		// for this guest's privileged setup, without transferring it to the host.
		admin := func(script string) string {
			t.Helper()
			return run("sudo -S -p '' -- /bin/sh -ec "+shellescape.Quote(script)+` < "$HOME/password"`, nil)
		}
		admin("umask 022; echo macos > /var/db/cage-guest-os")
		t.Log(admin("/bin/bash /Users/cageprovision/claude-desktop-qa.sh install /Users/cageprovision/Claude.dmg"))
		t.Log(run("/bin/bash \"$HOME/claude-desktop-qa.sh\" inspect", nil))
		if helper := os.Getenv("CAGE_TEST_CLAUDE_WINDOW"); helper != "" {
			t.Log(run("/bin/bash \"$HOME/claude-desktop-qa.sh\" launch", nil))
			f, err := os.Open(helper)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			run(`umask 077; cat > "$HOME/window-helper"`, f)
			admin("mkdir -p /usr/local/libexec && install -o root -g wheel -m 755 /Users/cageprovision/window-helper /usr/local/libexec/cage-claude-window")
			var frame, stderr bytes.Buffer
			code, err := l.Shell(ctx, name, ShellOpts{
				Args:   []string{"/bin/sh", "-ec", `sudo -S -p '' /bin/launchctl asuser "$(id -u)" sudo -nu "$(id -un)" -- /usr/local/libexec/cage-claude-window < "$HOME/password"`},
				Stdout: &frame, Stderr: &stderr,
			})
			if err != nil || code != 0 {
				t.Fatalf("single-window capture (%d): %v %s", code, err, stderr.String())
			}
			img, err := png.DecodeConfig(&frame)
			if err != nil || img.Width <= 0 || img.Height <= 0 {
				t.Fatalf("invalid window PNG: %v", err)
			}
			t.Logf("Verified Claude window PNG, %d × %d", img.Width, img.Height)
		}
	}
}

// A failed request alone could mean that the guest network is misconfigured.
// Check that each guest probe reached the closed gate for this instance.
func verifyMacOSGuestNetwork(t *testing.T, ctx context.Context, instance string, run func(string, io.Reader) string) {
	t.Helper()
	client, err := cage.OpenGateClient(instance)
	if err != nil {
		t.Fatal(err)
	}
	status := func() cage.GateStatus {
		t.Helper()
		s, err := client.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if s.Mode != cage.GateClosed {
			t.Fatalf("expected a closed network gate, got %q", s.Mode)
		}
		return s
	}
	check := func(label, script string, denied func(cage.GateCounters) uint64) {
		t.Helper()
		before := status()
		run(script, nil)
		deadline := time.Now().Add(3 * time.Second)
		for {
			after := status()
			if after.RunID != before.RunID || after.Epoch != before.Epoch {
				t.Fatalf("network gate changed during %s probe", label)
			}
			if n := denied(after.Counters); n > denied(before.Counters) {
				t.Logf("Closed gate denied guest %s (%d → %d)", label, denied(before.Counters), n)
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("host gate did not record the guest %s probe", label)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	check("TCP", "if /usr/bin/nc -G 2 -z 1.1.1.1 443; then exit 1; fi", func(c cage.GateCounters) uint64 { return c.TCPDenied })
	// Sending a UDP datagram can succeed locally even when the host drops it.
	check("UDP", "printf cage | /usr/bin/nc -u -w 1 1.1.1.1 3478", func(c cage.GateCounters) uint64 { return c.UDPDenied })
	check("DNS", `resolver=$(awk '/^nameserver[[:space:]]/{print $2; exit}' /etc/resolv.conf)
test -n "$resolver"
response=$(/usr/bin/dig @"$resolver" +time=2 +tries=1 +noall +comments +answer "cage-check-$(uuidgen).example.com." A) || :
if printf '%s\n' "$response" | grep -Eq 'status: NOERROR|[[:space:]]IN[[:space:]]+A[[:space:]]'; then exit 1; fi`, func(c cage.GateCounters) uint64 { return c.DNSDenied })

	if _, err := client.BeginCheck(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := client.Close(ctx, "guest network test finished"); err != nil {
			t.Error(err)
		}
	}()
	probe := func(path, want string) {
		t.Helper()
		response := run("curl --noproxy '*' -sS --max-time 15 -o /dev/null -w '%{http_code}' http://"+cage.DiagnosticHost+path, nil)
		if response != want {
			t.Fatalf("guest diagnostic %s: got %q, want %q", path, response, want)
		}
	}
	probe("/", "200")
	before, err := client.SetFault(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	probe("/egress", "503")
	probe("/dns", "503")
	after, err := client.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Counters.FaultTCP <= before.Counters.FaultTCP || after.Counters.FaultDNS <= before.Counters.FaultDNS {
		t.Fatal("missing proxy fault observations for guest TCP or DNS")
	}
	t.Log("Guest diagnostic service reachable; both fixed probes reported injected proxy failure")
}
