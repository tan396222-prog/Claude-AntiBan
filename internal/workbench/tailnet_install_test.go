package workbench

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTailnetInstallAtomicallyReplacesRunningPayloadAndPreservesIdentity(t *testing.T) {
	if os.PathSeparator != '/' {
		t.Skip("guest installation executes on Unix")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	sudoers := filepath.Join(root, "sudoers")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sudoers, 0700); err != nil {
		t.Fatal(err)
	}
	old, err := os.ReadFile("/bin/sleep")
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(bin, "cage-tailnet")
	if err = os.WriteFile(executable, old, 0755); err != nil {
		t.Fatal(err)
	}
	identity := filepath.Join(root, "identity.json")
	if err = os.WriteFile(identity, []byte("saved-node-and-keys"), 0600); err != nil {
		t.Fatal(err)
	}
	// This is a real open executable inode, rather than a mocked install result.
	running := exec.Command(executable, "60")
	if err = running.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = running.Process.Kill(); _ = running.Wait() }()
	if err = os.WriteFile(filepath.Join(bin, "sudo"), []byte("#!/bin/sh\nexec \"$@\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	script := strings.ReplaceAll(tailnetInstallScript, "/usr/local/bin", bin)
	script = strings.ReplaceAll(script, "/etc/sudoers.d", sudoers)
	fresh := []byte("#!/bin/sh\nprintf updated-tailnet\n")
	install := exec.Command("/bin/bash", "-c", script)
	install.Env = append(os.Environ(), "PATH="+bin+":/usr/bin:/bin")
	install.Stdin = bytes.NewReader(fresh)
	if output, err := install.CombinedOutput(); err != nil {
		t.Fatalf("running binary replacement failed: %v %s", err, output)
	}
	output, err := exec.Command(executable).Output()
	if err != nil || string(output) != "updated-tailnet" {
		t.Fatalf("new process uses old payload: %q %v", output, err)
	}
	if data, err := os.ReadFile(identity); err != nil || string(data) != "saved-node-and-keys" {
		t.Fatal("installation touched identity", err)
	}
	if entries, err := filepath.Glob(filepath.Join(bin, ".cage-tailnet.*")); err != nil || len(entries) != 0 {
		t.Fatal("staged payload left behind", entries, err)
	}
	// A failed staged copy must leave the working binary and identity in place.
	if err = os.WriteFile(filepath.Join(bin, "sudo"), []byte("#!/bin/sh\nif test \"$1\" = install; then exit 42; fi\nexec \"$@\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	failed := exec.Command("/bin/bash", "-c", script)
	failed.Env = install.Env
	failed.Stdin = strings.NewReader("broken candidate")
	if output, err := failed.CombinedOutput(); err == nil {
		t.Fatalf("failed staged copy reported success: %s", output)
	}
	if actual, err := os.ReadFile(executable); err != nil || !bytes.Equal(actual, fresh) {
		t.Fatal("failed installation replaced the working binary", err)
	}
	if actual, err := os.ReadFile(identity); err != nil || string(actual) != "saved-node-and-keys" {
		t.Fatal("failed installation touched identity", err)
	}
	if entries, err := filepath.Glob(filepath.Join(bin, ".cage-tailnet.*")); err != nil || len(entries) != 0 {
		t.Fatal("failed installation leaked its stage", entries, err)
	}
}
