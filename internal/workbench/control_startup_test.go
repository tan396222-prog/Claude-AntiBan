package workbench

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestControllerStartsAfterProbeReleasesLock(t *testing.T) {
	profile := t.TempDir()
	lock, err := openControllerLock(profile, os.O_CREATE|os.O_RDWR)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := lockController(lock); err != nil {
		t.Fatal(err)
	}
	defer unlockController(lock)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestServeShutdownHandoff$", "-test.timeout=12s")
	cmd.Env = append(os.Environ(), "CAGE_TEST_SHUTDOWN_CONFIG="+filepath.Join(profile, "config.json"))
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() { cancel(); <-done; t.Log(output.String()) }()
	// Wait until the real server reaches startup while another process still
	// holds the short-lived status-probe lock.
	for {
		log, _ := os.ReadFile(filepath.Join(profile, "logs", "events.jsonl"))
		if strings.Contains(string(log), "service starting") {
			break
		}
		select {
		case err := <-done:
			done <- err
			t.Fatalf("controller exited during startup: %v", err)
		case <-ctx.Done():
			t.Fatal("controller did not reach startup")
		case <-time.After(10 * time.Millisecond):
		}
	}
	select {
	case err := <-done:
		done <- err
		t.Fatalf("controller mistook a status probe for a running service: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := unlockController(lock); err != nil {
		t.Fatal(err)
	}
	for {
		c, err := connectController(profile)
		if err == nil {
			defer c.close()
			status, err := c.call("POST", "/api/handoff", map[string]string{"token": c.record.Token}, nil)
			if err != nil || status != http.StatusOK {
				t.Fatalf("shutdown: HTTP %d, %v", status, err)
			}
			return
		}
		select {
		case err := <-done:
			done <- err
			t.Fatalf("controller exited before becoming ready: %v", err)
		case <-ctx.Done():
			t.Fatal("controller did not become ready after probe released its lock")
		case <-time.After(25 * time.Millisecond):
		}
	}
}
