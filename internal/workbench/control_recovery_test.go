package workbench

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Reusing a dead controller's port must not turn an old token into a
// permanently unopenable profile, including when its PID has been reused.
func TestDesktopRecoversStaleRecordOnReusedPort(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(strconv.FormatBool(held), func(t *testing.T) {
			profile := t.TempDir()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "old token", http.StatusUnauthorized)
			}))
			defer server.Close()
			_, portText, _ := net.SplitHostPort(server.Listener.Addr().String())
			port, _ := strconv.Atoi(portText)
			if err := writePrivate(filepath.Join(profile, "control.json"), controlFile{Port: port, PID: os.Getpid(), Token: strings.Repeat("a", 64)}); err != nil {
				t.Fatal(err)
			}
			lock, err := os.OpenFile(filepath.Join(profile, "control.lock"), os.O_CREATE|os.O_RDWR, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			if held {
				if err := lockController(lock); err != nil {
					t.Fatal(err)
				}
				defer unlockController(lock)
			}
			started := errors.New("replacement started")
			_, err = desktopLaunch(profile, false, DesktopOptions{}, func() error { return started })
			if held && (err == nil || errors.Is(err, started)) {
				t.Fatalf("replaced a locked unauthenticated controller: %v", err)
			}
			if !held && !errors.Is(err, started) {
				t.Fatalf("stale record prevented restart: %v", err)
			}
			if !held {
				exe, _ := os.Executable()
				st, err := NewDesktopLocalController(filepath.Join(profile, "config.json"), exe).Status(context.Background())
				if err != nil || !st.Known || st.ServiceRunning {
					t.Fatalf("stale port/PID reported as a live controller: %+v %v", st, err)
				}
			}
		})
	}
}

func TestControllerRestartAfterGracefulAndForcedExit(t *testing.T) {
	for _, forced := range []bool{false, true} {
		t.Run(strconv.FormatBool(forced), func(t *testing.T) {
			profile := t.TempDir()
			config := filepath.Join(profile, "config.json")
			keep := filepath.Join(profile, "personal-data-sentinel")
			if err := os.WriteFile(keep, []byte("preserve projects and account data"), 0600); err != nil {
				t.Fatal(err)
			}
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			start := func() *exec.Cmd {
				cmd := exec.Command(exe, "-test.run=^TestServeShutdownHandoff$", "-test.timeout=30s")
				cmd.Env = append(os.Environ(), "CAGE_TEST_SHUTDOWN_CONFIG="+config)
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
				return cmd
			}
			waitReady := func() *controllerClient {
				deadline := time.Now().Add(10 * time.Second)
				for time.Now().Before(deadline) {
					c, err := connectController(profile)
					if err == nil {
						return c
					}
					time.Sleep(25 * time.Millisecond)
				}
				t.Fatal("controller never became ready")
				return nil
			}
			stop := func(c *controllerClient) {
				deadline := time.Now().Add(3 * time.Second)
				for {
					status, err := c.call("POST", "/api/handoff", map[string]string{"token": c.record.Token}, nil)
					if err != nil {
						t.Fatal(err)
					}
					if status == http.StatusOK {
						break
					}
					if status != http.StatusConflict || time.Now().After(deadline) {
						t.Fatalf("idle handoff returned HTTP %d", status)
					}
					time.Sleep(25 * time.Millisecond)
				}
				if err := c.waitForExit(); err != nil {
					t.Fatal(err)
				}
			}
			cmd := start()
			c := waitReady()
			old := c.record
			if forced {
				if err := cmd.Process.Kill(); err != nil {
					t.Fatal(err)
				}
				_ = cmd.Wait()
				if _, err := os.Stat(filepath.Join(profile, "control.json")); err != nil {
					t.Fatalf("forced exit did not leave the expected stale record: %v", err)
				}
			} else {
				stop(c)
				if err := cmd.Wait(); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(filepath.Join(profile, "control.json")); !os.IsNotExist(err) {
					t.Fatalf("normal exit left a connection record: %v", err)
				}
			}
			c.close()
			if !controllerLockFree(profile) {
				t.Fatal("exited process left the OS lock held")
			}
			// Occupy the old port with a different service while reopening Cage.
			listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(old.Port)))
			if err != nil {
				t.Fatal(err)
			}
			foreign := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "different service", 401) }))
			foreign.Listener.Close()
			foreign.Listener = listener
			foreign.Start()
			defer foreign.Close()
			_, err = desktopLaunch(profile, false, DesktopOptions{}, func() error { cmd = start(); return nil })
			if err != nil {
				t.Fatalf("reopen failed: %v", err)
			}
			c = waitReady()
			defer c.close()
			if c.record.PID == old.PID || c.record.Token == old.Token {
				t.Fatal("reopen reused a dead controller's identity")
			}
			stop(c)
			if err := cmd.Wait(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(profile, "control.json")); !os.IsNotExist(err) {
				t.Fatalf("reopened controller left a record: %v", err)
			}
			if b, err := os.ReadFile(keep); err != nil || string(b) != "preserve projects and account data" {
				t.Fatalf("personal data changed: %v", err)
			}
		})
	}
}

func TestDesktopRecoveryRejectsNonRegularLock(t *testing.T) {
	profile := t.TempDir()
	if err := os.Mkdir(filepath.Join(profile, "control.lock"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "control.json"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := desktopLaunch(profile, false, DesktopOptions{}, func() error {
		t.Fatal("started with an unusable lock")
		return nil
	})
	if err == nil {
		t.Fatal("unusable lock accepted")
	}
}

func TestDesktopWaitsForRetiringController(t *testing.T) {
	profile := t.TempDir()
	lock, err := openControllerLock(profile, os.O_CREATE|os.O_RDWR)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := lockController(lock); err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() { time.Sleep(100 * time.Millisecond); _ = unlockController(lock); close(released) }()
	defer func() { <-released }()
	started := errors.New("new controller started")
	_, err = desktopLaunch(profile, false, DesktopOptions{}, func() error {
		if !controllerLockFree(profile) {
			t.Error("started before the retiring controller released its lock")
		}
		return started
	})
	if !errors.Is(err, started) {
		t.Fatalf("did not resume after shutdown: %v", err)
	}
}

func TestPrepareUpdateWaitsForRetiringController(t *testing.T) {
	profile := t.TempDir()
	lock, err := openControllerLock(profile, os.O_CREATE|os.O_RDWR)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := lockController(lock); err != nil {
		t.Fatal(err)
	}
	// Shutdown has removed control.json but has not released the profile yet.
	released := make(chan struct{})
	go func() { time.Sleep(100 * time.Millisecond); _ = unlockController(lock); close(released) }()
	defer func() { <-released }()
	result, err := PrepareUpdate(filepath.Join(profile, "config.json"), false)
	if err != nil || result.Status != "prepared" {
		t.Fatalf("retiring controller prevented update: %+v %v", result, err)
	}
	if !controllerLockFree(profile) {
		t.Fatal("update accepted before the controller released its lock")
	}
}
