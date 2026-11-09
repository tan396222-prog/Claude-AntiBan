package managedproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// Run from beside the bundled mihomo with CAGE_LINUX_INTEGRATION=1. A local
// upstream proxy proves routing and fail-closed behavior without external I/O.
func TestLinuxBundledProxyLifecycle(t *testing.T) {
	if os.Getenv("CAGE_LINUX_INTEGRATION") != "1" {
		t.Skip("requires a bundled Linux mihomo and host network interfaces")
	}
	profile := t.TempDir()
	s, err := Load(profile)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s.Port = listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	var targetRequests, proxyRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetRequests.Add(1)
		w.Header().Set("Connection", "close")
		_, _ = io.WriteString(w, "local-target-response")
	}))
	defer target.Close()
	targetAddress := strings.TrimPrefix(target.URL, "http://")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyRequests.Add(1)
		if r.Method != http.MethodConnect || r.Host != targetAddress {
			t.Errorf("upstream received unexpected request %s %q", r.Method, r.Host)
			http.Error(w, "refused", http.StatusForbidden)
			return
		}
		remote, err := net.DialTimeout("tcp4", targetAddress, time.Second)
		if err != nil {
			http.Error(w, "target unavailable", http.StatusBadGateway)
			return
		}
		defer remote.Close()
		client, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer client.Close()
		_, _ = io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n")
		go func() { _, _ = io.Copy(remote, buffered); _ = remote.Close() }()
		_, _ = io.Copy(client, remote)
	}))
	defer upstream.Close()
	upstreamPort := upstream.Listener.Addr().(*net.TCPAddr).Port
	if err := s.Import("local smoke node", "", []byte(fmt.Sprintf("proxies:\n - {name: smoke, type: http, server: 127.0.0.1, port: %d}\n", upstreamPort)), "smoke", 0, ""); err != nil {
		t.Fatal(err)
	}
	s.Selected = s.Sources[0].Nodes[0].ID
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	t.Cleanup(func() {
		if err := Stop(profile); err != nil {
			t.Error(err)
		}
	})
	readRecord := func() linuxCoreRecord {
		t.Helper()
		b, err := os.ReadFile(linuxCoreRecordPath(profile))
		var record linuxCoreRecord
		if err != nil || json.Unmarshal(b, &record) != nil {
			t.Fatal("missing proxy process record", err)
		}
		return record
	}
	if err := Start(ctx, profile, s); err != nil {
		t.Fatal(err)
	}
	first := readRecord()
	if !Running(ctx, profile, s) {
		t.Fatal("bundled core did not expose its authenticated local state")
	}
	if info, err := os.Stat(Socket(profile)); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("proxy control socket is not private", err)
	}
	if err := Start(ctx, profile, s); err != nil {
		t.Fatal(err)
	}
	if readRecord() != first {
		t.Fatal("idempotent start replaced the running core")
	}
	proxyURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", s.Port))
	proxyURL.User = url.UserPassword("cage", s.Auth)
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	res, err := client.Get(target.URL + "/cage-linux-smoke")
	if err != nil {
		t.Fatal("selected proxy did not carry the request", err)
	}
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil || string(body) != "local-target-response" || proxyRequests.Load() != 1 || targetRequests.Load() != 1 {
		t.Fatal("request bypassed the selected proxy", err)
	}
	upstream.Close()
	res, err = client.Get(target.URL + "/cage-linux-smoke")
	if err == nil {
		body, _ = io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode == http.StatusOK || strings.Contains(string(body), "local-target-response") {
			t.Fatal("core fell back to direct after the upstream stopped")
		}
	}
	if targetRequests.Load() != 1 {
		t.Fatal("target was contacted directly after the upstream stopped")
	}
	if err := Stop(profile); err != nil {
		t.Fatal(err)
	}
	if Running(ctx, profile, s) {
		t.Fatal("core still responds after stop")
	}
	if _, err := os.Stat(linuxCoreRecordPath(profile)); !os.IsNotExist(err) {
		t.Fatal("stop retained its process record", err)
	}
	if err := Start(ctx, profile, s); err != nil {
		t.Fatal("could not restart bundled core", err)
	}
	if readRecord() == first {
		t.Fatal("restart reused the old process identity")
	}
	second := readRecord()
	fd, err := unix.PidfdOpen(second.PID, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	if err := unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0); err != nil {
		t.Fatal(err)
	}
	if n, err := unix.Poll([]unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}, 5000); err != nil || n == 0 {
		t.Fatal("core did not exit after simulated crash", err)
	}
	if err := Start(ctx, profile, s); err != nil {
		t.Fatal("stale record prevented restart after a core crash", err)
	}
	if readRecord() == second || !Running(ctx, profile, s) {
		t.Fatal("core did not recover after a crash")
	}
}

func TestLinuxCoreStopPinsAndChecksProcessIdentity(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-done })
	record, err := identifyLinuxCore(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	stale := record
	stale.Started++
	if err := stopLinuxProcess(stale, record.Executable); err != nil {
		t.Fatal("stale record prevented recovery", err)
	}
	if err := unix.Kill(cmd.Process.Pid, 0); err != nil {
		t.Fatal("stopped a process with a different identity", err)
	}
	if err := stopLinuxProcess(record, "/proc/self/exe"); err == nil {
		t.Fatal("accepted a different executable")
	}
	previousBoot := record
	previousBoot.BootID = "a-previous-boot"
	if err := stopLinuxProcess(previousBoot, record.Executable); err != nil {
		t.Fatal("could not discard previous boot record", err)
	}
	if err := unix.Kill(cmd.Process.Pid, 0); err != nil {
		t.Fatal("stopped a process referenced by a previous boot", err)
	}
	if err := stopLinuxProcess(record, record.Executable); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("proxy process was not reaped")
	}
	if err := stopLinuxProcess(record, record.Executable); err != nil {
		t.Fatal("stopping an exited process should succeed", err)
	}
}
