package diagnostics

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestStreamPreservesManagedProducerSeverity(t *testing.T) {
	store := New(t.TempDir())
	stream := store.Stream("vm-hostagent", "warn")
	// Framing must still join structured records split across writes.
	stream.Write([]byte(`{"level":"de`))
	stream.Write([]byte("bug\",\"msg\":\"clock in sync\"}\n"))
	stream.Write([]byte("{\"level\":\"info\",\"msg\":\"ready\"}\n"))
	stream.Write([]byte("{\"level\":\"error\",\"msg\":\"failed\"}\n{\"level\":\"error\",\"msg\":\"failed\"}\n"))
	stream.Close()
	proxy := store.Stream("proxy", "info")
	proxy.Write([]byte("time=\"2026-11-08T00:00:00Z\" level=warning msg=\"connection refused\"\n"))
	proxy.Close()
	validation := store.Stream("proxy-validation", "error")
	validation.Write([]byte("time=\"2026-11-08T00:00:00Z\" level=info msg=\"configuration valid\"\n"))
	validation.Close()
	ordinary := store.Stream("task", "error")
	ordinary.Write([]byte("{\"level\":\"debug\",\"msg\":\"not a managed log producer\"}\n"))
	ordinary.Close()
	events := readEvents(t, store)
	want := []string{"debug", "info", "error", "error", "warn", "info", "error"}
	if len(events) != len(want) {
		t.Fatalf("unexpected records: %+v", events)
	}
	for i, event := range events {
		if event.Level != want[i] {
			t.Fatalf("record %d: level %s, want %s", i, event.Level, want[i])
		}
	}
}

func TestGuestRunnerDistinguishesRequestedStopFromFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux guest process contract")
	}
	python, err := exec.Command("uv", "python", "find").Output()
	if err != nil {
		t.Skip("uv Python unavailable")
	}
	for _, tc := range []struct {
		name, child string
		stop        bool
		want        int
	}{
		{"requested-stop", `import signal,time; signal.signal(signal.SIGTERM,lambda *_:exit(143)); print('ready',flush=True); time.sleep(10)`, true, 0},
		{"unexpected-143", `exit(143)`, false, 143},
		{"failure-during-stop", `import signal,time; signal.signal(signal.SIGTERM,lambda *_:exit(7)); print('ready',flush=True); time.sleep(10)`, true, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := filepath.Join(t.TempDir(), "runner.py")
			if err := os.WriteFile(runner, []byte(GuestRunner()), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			interpreter := strings.TrimSpace(string(python))
			cmd := exec.CommandContext(ctx, interpreter, runner, interpreter, "-c", tc.child)
			out, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			if tc.stop {
				if line, err := bufio.NewReader(out).ReadString('\n'); err != nil || line != "ready\n" {
					t.Fatalf("child not ready: %q %v", line, err)
				}
				if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
					t.Fatal(err)
				}
			}
			err = cmd.Wait()
			if cmd.ProcessState.ExitCode() != tc.want {
				t.Fatalf("exit %d, want %d: %v", cmd.ProcessState.ExitCode(), tc.want, err)
			}
		})
	}
}
