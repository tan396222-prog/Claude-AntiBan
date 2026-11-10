package main

import (
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"testing"
)

func TestRelayRecognizesOnlyTheRequestedTermination(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signal fixture; Windows build is checked separately")
	}
	for _, tc := range []struct {
		child     string
		forwarded os.Signal
		want      bool
	}{
		{"kill -TERM $$", syscall.SIGTERM, true},
		{"exit 143", syscall.SIGTERM, true},
		{"exit 143", nil, false},
		{"kill -TERM $$", nil, false},
		{"exit 7", syscall.SIGTERM, false},
		{"kill -KILL $$", syscall.SIGTERM, false},
	} {
		err := exec.Command("sh", "-c", tc.child).Run()
		if got := expectedRelayStop(err, tc.forwarded); got != tc.want {
			t.Fatalf("child=%q signal=%v: got %t, want %t", tc.child, tc.forwarded, got, tc.want)
		}
	}
}
