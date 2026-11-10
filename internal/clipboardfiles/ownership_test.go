package clipboardfiles

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func cacheFixture(t *testing.T) (CacheStore, CachedSelection) {
	t.Helper()
	directory := t.TempDir()
	created, err := os.MkdirTemp(directory, "created-")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(created, "fixture.txt")
	if err := os.WriteFile(path, []byte("complete copied bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	store := CacheStore{Directory: filepath.Join(directory, "records")}
	selection, err := store.Register([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	return store, selection
}

func TestCacheReleaseUsesItsCreationRecord(t *testing.T) {
	store, selection := cacheFixture(t)
	unrelated := filepath.Join(t.TempDir(), "cage-clipboard-unrelated")
	if err := os.Mkdir(unrelated, 0700); err != nil {
		t.Fatal(err)
	}
	if err := store.Release(selection.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(selection.Paths[0])); !os.IsNotExist(err) {
		t.Fatal("owned copy remained")
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatal("unrelated directory changed")
	}
	if err := store.Release(selection.ID); err != nil {
		t.Fatal("release was not idempotent", err)
	}
}

func TestCacheReleaseRejectsChangedCreationMarker(t *testing.T) {
	store, selection := cacheFixture(t)
	record, err := store.load(selection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(record.Directory, record.Marker), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.Release(selection.ID); err == nil {
		t.Fatal("changed directory ownership was accepted")
	}
	if data, err := os.ReadFile(selection.Paths[0]); err != nil || string(data) != "complete copied bytes" {
		t.Fatal("unverified files were removed")
	}
}

func TestCacheRecoveryKeepsUnpublishedLiveTransfers(t *testing.T) {
	store, selection := cacheFixture(t)
	listed, err := store.List()
	if err != nil || len(listed) != 0 {
		t.Fatal("an active unpublished transfer was collected", err)
	}
	if err := store.Publish(selection.ID); err != nil {
		t.Fatal(err)
	}
	listed, err = store.List()
	if err != nil || len(listed) != 1 || listed[0].ID != selection.ID {
		t.Fatal("published ownership could not be restored", err)
	}
	record, _ := store.load(selection.ID)
	record.Published = false
	record.Owner = 2147483647
	if err := store.save(record); err != nil {
		t.Fatal(err)
	}
	listed, err = store.List()
	if err != nil || len(listed) != 1 {
		t.Fatal("abandoned receive ownership was lost", err)
	}
}

func TestCacheRejectsForeignOrMalformedIdentifiers(t *testing.T) {
	store, selection := cacheFixture(t)
	for _, id := range []string{"../outside", "", strings.Repeat("g", 32)} {
		if err := store.Release(id); err == nil {
			t.Fatal("invalid cache identifier admitted")
		}
	}
	if _, err := os.Stat(selection.Paths[0]); err != nil {
		t.Fatal(err)
	}
}

func TestCacheRecoveryLeavesAnotherLiveNativeOwnerInControl(t *testing.T) {
	store, selection := cacheFixture(t)
	record, err := store.load(selection.ID)
	if err != nil {
		t.Fatal(err)
	}
	record.Published = true
	record.Owner = os.Getpid()
	if err := store.save(record); err != nil {
		t.Fatal(err)
	}
	listed, err := store.List()
	if err != nil || len(listed) != 0 {
		t.Fatal("another live native owner's clipboard entered recovery", err)
	}
	if _, err := os.Stat(selection.Paths[0]); err != nil {
		t.Fatal(err)
	}
}

func TestCacheConcurrentPublishCannotRecreateAReleasedReceipt(t *testing.T) {
	for range 20 {
		store, selection := cacheFixture(t)
		start := make(chan struct{})
		published := make(chan error, 1)
		released := make(chan error, 1)
		go func() { <-start; published <- store.Publish(selection.ID) }()
		go func() { <-start; released <- store.Release(selection.ID) }()
		close(start)
		if err := <-published; err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if err := <-released; err != nil {
			t.Fatal(err)
		}
		if _, err := store.load(selection.ID); !os.IsNotExist(err) {
			t.Fatal("a concurrent publication recreated a retired receipt", err)
		}
	}
}

type producerInput struct{}

func (producerInput) Read(buffer []byte) (int, error) {
	fmt.Fprintln(os.Stdout, "copy-started")
	return os.Stdin.Read(buffer)
}

func TestCacheProducerFixture(t *testing.T) {
	directory := os.Getenv("CAGE_CLIPBOARD_PRODUCER_TEST")
	if directory == "" {
		t.Skip("subprocess fixture")
	}
	_, _ = (CacheStore{Directory: directory}).Read(producerInput{})
}

func TestCacheRecoversAProducerKilledDuringItsFirstRead(t *testing.T) {
	root := t.TempDir()
	store := CacheStore{Directory: filepath.Join(root, "records")}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCacheProducerFixture$")
	child.Env = append(os.Environ(), "CAGE_CLIPBOARD_PRODUCER_TEST="+store.Directory, "TMPDIR="+root, "TEMP="+root, "TMP="+root)
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer child.Process.Kill()
	line, err := bufio.NewReader(output).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "copy-started" {
		t.Fatal("producer did not enter its blocking read", err)
	}
	if listed, err := store.List(); err != nil || len(listed) != 0 {
		t.Fatal("the live producer lost ownership", err)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	listed, err := store.List()
	if err != nil || len(listed) != 1 {
		t.Fatal("terminated producer had no creation receipt", err)
	}
	if err := store.Release(listed[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(listed[0].Paths[0])); !os.IsNotExist(err) {
		t.Fatal("terminated producer retained its partial directory")
	}
}
