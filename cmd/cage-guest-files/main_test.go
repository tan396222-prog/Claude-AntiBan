package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sylvinus/agent-vm/internal/clipboardfiles"
)

type closedOutput struct{}

func (closedOutput) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestGuestFileFailedReplyReleasesUnpublishedCopy(t *testing.T) {
	isolateClipboardCache(t)
	source := filepath.Join(t.TempDir(), "source.txt")
	if err := os.WriteFile(source, []byte("retained source"), 0600); err != nil {
		t.Fatal(err)
	}
	selection, _ := json.Marshal([]string{source})
	if err := run([]string{"copy", "--paths-stdin"}, bytes.NewReader(selection), closedOutput{}); err == nil {
		t.Fatal("closed output accepted a selection")
	}
	store, err := clipboardfiles.UserCacheStore()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(store.Directory)
	if err != nil || len(entries) != 1 || entries[0].Name() != ".lock" {
		t.Fatal("an unpublished result retained its ownership receipt", err)
	}
	if data, err := os.ReadFile(source); err != nil || string(data) != "retained source" {
		t.Fatal("source changed", err)
	}
}

func TestGuestFileSaveRequiresCompleteFrames(t *testing.T) {
	isolateClipboardCache(t)
	cache := t.TempDir()
	t.Setenv("TMPDIR", cache)
	t.Setenv("TEMP", cache)
	t.Setenv("TMP", cache)
	content := []byte{0, 255, 128, '\n'}
	var wire bytes.Buffer
	binary.Write(&wire, binary.LittleEndian, uint32(len(content)))
	wire.Write(content)
	truncated := append([]byte(nil), wire.Bytes()...)
	binary.Write(&wire, binary.LittleEndian, uint32(0))
	var output bytes.Buffer
	if err := run([]string{"save", "图片 ' .bin"}, &wire, &output); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Paths []string `json:"paths"`
	}
	if json.Unmarshal(output.Bytes(), &result) != nil || len(result.Paths) != 1 {
		t.Fatal("saved file did not return its private path")
	}
	data, err := os.ReadFile(result.Paths[0])
	if err != nil || !bytes.Equal(data, content) {
		t.Fatal("saved bytes changed")
	}
	selection, _ := json.Marshal(result.Paths)
	output.Reset()
	if err := run([]string{"link-cached", "--paths-stdin"}, bytes.NewReader(selection), &output); err != nil {
		t.Fatal(err)
	}
	var linked struct {
		Paths []string `json:"paths"`
	}
	if json.Unmarshal(output.Bytes(), &linked) != nil || len(linked.Paths) != 1 {
		t.Fatal("cache grouping did not return its private path")
	}
	sourceInfo, _ := os.Lstat(result.Paths[0])
	linkedInfo, err := os.Lstat(linked.Paths[0])
	if err != nil || !os.SameFile(sourceInfo, linkedInfo) {
		t.Fatal("cache grouping copied or replaced file data")
	}
	if err := os.Remove(result.Paths[0]); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(linked.Paths[0])
	if err != nil || !bytes.Equal(data, content) {
		t.Fatal("removing the original upload lost the grouped attachment")
	}
	before, _ := os.ReadDir(cache)
	if err := run([]string{"save", "partial.bin"}, bytes.NewReader(truncated), io.Discard); err == nil {
		t.Fatal("missing completion frame was accepted")
	}
	after, _ := os.ReadDir(cache)
	if len(after) != len(before) {
		t.Fatal("failed save left a partial attachment")
	}
	for _, name := range []string{"../escape", "/absolute", "file:stream", ""} {
		if err := run([]string{"save", name}, bytes.NewReader([]byte{0, 0, 0, 0}), io.Discard); err == nil {
			t.Fatal("invalid filename accepted")
		}
	}
}

func TestGuestFileOperationsUseStdinAndPreserveContent(t *testing.T) {
	isolateClipboardCache(t)
	root := t.TempDir()
	path := filepath.Join(root, "资料.bin")
	content := []byte{0, 255, 128, '\n'}
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	selection, _ := json.Marshal([]string{path})
	var wire bytes.Buffer
	if err := run([]string{"export", "--paths-stdin"}, bytes.NewReader(selection), &wire); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := run([]string{"import"}, &wire, &output); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Paths []string `json:"paths"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil || len(result.Paths) != 1 {
		t.Fatalf("import: %v", err)
	}
	defer os.RemoveAll(filepath.Dir(result.Paths[0]))
	got, err := os.ReadFile(result.Paths[0])
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("content changed: %v", err)
	}
	if result.Paths[0] == path {
		t.Fatal("import reused source path")
	}
}

func isolateClipboardCache(t *testing.T) {
	t.Helper()
	directory := t.TempDir()
	for _, name := range []string{"HOME", "USERPROFILE", "LOCALAPPDATA", "XDG_CACHE_HOME"} {
		t.Setenv(name, directory)
	}
}

func TestGuestFileSelectionBoundsAndMissingSourceFail(t *testing.T) {
	for _, input := range []string{`["/does/not/exist"]`, `[]`, `{}`, `[] {}`, strings.Repeat(" ", (1<<20)+1)} {
		if err := run([]string{"copy", "--paths-stdin"}, strings.NewReader(input), io.Discard); err == nil {
			t.Fatal("invalid selection succeeded")
		}
	}
}
