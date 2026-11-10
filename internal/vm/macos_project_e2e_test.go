//go:build darwin && arm64 && cgo && e2e && !no_vz

package vm

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lima-vm/sshocker/pkg/reversesshfs"
	"github.com/sylvinus/agent-vm/internal/mounts"
	"golang.org/x/sys/unix"
)

// Verify the workbench's restricted project service with the OpenSSH client
// shipped by Apple. The guest accesses the service only through its existing
// management SSH connection. This does not mount the project in the guest.
func verifyMacOSProjectTransfer(t *testing.T, ctx context.Context, l *Lima, name string) {
	t.Helper()
	project := t.TempDir()
	if err := os.Mkdir(filepath.Join(project, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	write := func(path, contents string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(project, path), []byte(contents), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(".git/HEAD", "ref: refs/heads/test\n", 0600)
	write("build script", "#!/bin/sh\nprintf 'ran in guest'\n", 0755)
	if err := os.Symlink("build script", filepath.Join(project, "build-link")); err != nil {
		t.Fatal(err)
	}

	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, fd := range fds {
		unix.CloseOnExec(fd)
	}
	server := os.NewFile(uintptr(fds[0]), "project-server")
	guest := os.NewFile(uintptr(fds[1]), "guest-ssh")
	done := make(chan error, 1)
	go func() {
		defer server.Close()
		done <- reversesshfs.ServeConfined(server, project, false, mounts.BaseNames)
	}()
	// nc relays only the SFTP protocol on SSH stdin/stdout. The client
	// connects in a private guest directory. All diagnostics use stderr.
	const script = `set -eu
umask 077
work=$(mktemp -d /tmp/cage-project.XXXXXXXX)
cd "$work"
exec 3<&0
/usr/bin/nc -lU wire <&3 3<&- &
relay=$!
exec 3<&- </dev/null >&2
trap 'kill "$relay" 2>/dev/null || :; wait "$relay" 2>/dev/null || :; rm -rf "$work"' EXIT
for ((i=0; i<100; i++)); do [[ -S wire ]] && break; sleep 0.05; done
test -S wire
printf 'guest save' > edit
cat > batch <<'EOF'
get -p build-link build
get .git/HEAD head-copy
put edit .saving
rename .saving "source file"
put edit .git/denied
EOF
if /usr/bin/sftp -q -b batch -D '/usr/bin/nc -U wire' unused; then exit 1; fi
test "$(cat head-copy)" = 'ref: refs/heads/test'
test "$(./build)" = 'ran in guest'`
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var output bytes.Buffer
	code, shellErr := l.Shell(ctx, name, ShellOpts{Args: []string{"/bin/bash", "-c", script}, Stdin: guest, Stdout: guest, Stderr: &output})
	guest.Close()
	serverErr := <-done
	if code != 0 || shellErr != nil || (serverErr != nil && !errors.Is(serverErr, io.EOF)) {
		t.Fatalf("guest transfer (%d): SSH %v, SFTP %v\n%s", code, shellErr, serverErr, output.String())
	}
	data, err := os.ReadFile(filepath.Join(project, "source file"))
	if err != nil || string(data) != "guest save" {
		t.Fatalf("guest atomic save did not reach the host: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(project, ".git", "denied")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("guest protected write: %v", err)
	}
	t.Log("Guest SFTP executed a transferred script, saved to the host, and was denied a Git metadata write")
}
