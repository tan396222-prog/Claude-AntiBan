// Package desktop reaches the user out of any terminal: notifications and
// yes/no dialogs, where the desktop has a way (osascript on macOS; notify-send,
// zenity or kdialog on Linux). Text always goes as arguments, never as script.
package desktop

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Notify shows a notification, without waiting; nothing where there is no
// way.
func Notify(title, msg string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("osascript", "-e", "on run argv", "-e", "display notification (item 2 of argv) with title (item 1 of argv)", "-e", "end run", title, msg)
	case "linux":
		if _, err := exec.LookPath("notify-send"); err != nil {
			return
		}
		cmd = exec.Command("notify-send", "--", title, msg)
	default:
		return
	}
	if cmd.Start() == nil {
		go func() { _ = cmd.Wait() }()
	}
}

// Ask shows a dialog with Deny (the default) and Allow, until ctx ends (a
// refusal). ok is false when no dialog could be shown (no tool, no display or
// graphical session, the tool failed to start): allow is then false and nobody
// answered. ok is true when the user answered, or ctx ended with the dialog up.
func Ask(ctx context.Context, title, msg string) (allow, ok bool) {
	switch runtime.GOOS {
	case "darwin":
		res := run(ctx, "osascript", "-e", "on run argv",
			"-e", `display dialog (item 2 of argv) with title (item 1 of argv) buttons {"Deny", "Allow"} default button "Deny" with icon caution`,
			"-e", "end run", title, msg)
		switch {
		case res.err == nil:
			return strings.Contains(res.stdout, "button returned:Allow"), true
		case res.timedOut:
			return false, true
		}
		// Deny by closing the dialog is "User canceled. (-128)", an exit status 1. Without a
		// graphical session osascript exits 1 as well, with another error.
		return false, strings.Contains(res.stderr, "(-128)")
	case "linux":
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return false, false
		}
		if _, err := exec.LookPath("zenity"); err == nil {
			res := run(ctx, "zenity", "--question", "--title", title, "--text", msg,
				"--ok-label=Allow", "--cancel-label=Deny", "--default-cancel", "--no-markup")
			if allow, ok := linuxAnswer(res, 1, "cannot open display", "failed to open display", "unable to init server"); ok {
				return allow, true
			}
		}
		if _, err := exec.LookPath("kdialog"); err == nil {
			res := run(ctx, "kdialog", "--title", title, "--warningyesno", msg,
				"--yes-label", "Allow", "--no-label", "Deny")
			if allow, ok := linuxAnswer(res, 2, "could not connect to display", "cannot connect to x server", "no qt platform plugin"); ok {
				return allow, true
			}
		}
	}
	return false, false
}

// linuxAnswer reads the result of zenity or kdialog: exit 0 is Allow, and an exit status up to
// maxDeny is Deny, unless stderr says the tool had no display to show the dialog on.
func linuxAnswer(res result, maxDeny int, noDisplay ...string) (allow, ok bool) {
	if res.err == nil {
		return true, true
	}
	if res.timedOut {
		return false, true
	}
	if res.code < 1 || res.code > maxDeny {
		return false, false
	}
	stderr := strings.ToLower(res.stderr)
	for _, s := range noDisplay {
		if strings.Contains(stderr, s) {
			return false, false
		}
	}
	return false, true
}

type result struct {
	stdout, stderr string
	err            error
	code           int  // the exit status, -1 when the tool did not exit (not found, killed)
	timedOut       bool // ctx ended while the tool ran
}

func run(ctx context.Context, name string, args ...string) result {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	res := result{stdout: stdout.String(), stderr: stderr.String(), err: err, code: -1}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		res.code = ee.ExitCode()
		// Killed when ctx ended, rather than exited.
		res.timedOut = res.code == -1 && ctx.Err() != nil
	}
	return res
}
