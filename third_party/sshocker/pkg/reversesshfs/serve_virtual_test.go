package reversesshfs

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pkg/sftp"
)

func TestServeConfinedVirtualRootAndRevocation(t *testing.T) {
	directory := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "hello.txt"), []byte("live project"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(directory, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, ".git", "config"), []byte("protected"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	symlink := os.Symlink(outside, filepath.Join(directory, "escape")) == nil
	serverSide, clientSide := net.Pipe()
	_ = serverSide.SetDeadline(time.Now().Add(5 * time.Second))
	_ = clientSide.SetDeadline(time.Now().Add(5 * time.Second))
	done := make(chan struct{})
	go func() { defer close(done); _ = ServeConfinedVirtual(serverSide, directory, false, []string{".git"}) }()
	client, err := sftp.NewClientPipe(clientSide, clientSide)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	defer serverSide.Close()
	actual, err := client.RealPath(".")
	if err != nil || actual != "/" {
		t.Fatalf("virtual root exposed host: %q %v", actual, err)
	}
	f, err := client.Open("/hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(f)
	f.Close()
	if err != nil || string(data) != "live project" {
		t.Fatalf("read %q %v", data, err)
	}
	temp, err := client.Create("/atomic.tmp")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = temp.Write([]byte("atomic save")); err != nil {
		t.Fatal(err)
	}
	temp.Close()
	if err = client.PosixRename("/atomic.tmp", "/saved.txt"); err != nil {
		t.Fatal(err)
	}
	if data, err = os.ReadFile(filepath.Join(directory, "saved.txt")); err != nil || string(data) != "atomic save" {
		t.Fatalf("atomic save %q %v", data, err)
	}
	if f, err := client.Create("/.git/config"); err == nil {
		f.Close()
		t.Fatal("protected metadata writable")
	}
	if symlink {
		if f, err := client.Open("/escape/secret.txt"); err == nil {
			f.Close()
			t.Fatal("root symlink escaped")
		}
	}
	if f, err := client.Open(filepath.ToSlash(filepath.Join(outside, "secret.txt"))); err == nil {
		f.Close()
		t.Fatal("absolute host path escaped")
	}
	if err = serverSide.Close(); err != nil {
		t.Fatal(err)
	}
	<-done
	if _, err = client.Create("/after-revoke"); err == nil {
		t.Fatal("revoked transport writable")
	}
}
