package clipboardfiles

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

func TestCacheReleaseRetriesAfterExternalWindowsReaderCloses(t *testing.T) {
	store, selection := cacheFixture(t)
	name, err := windows.UTF16PtrFromString(selection.Paths[0])
	if err != nil {
		t.Fatal(err)
	}
	reader, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if reader != 0 {
			windows.CloseHandle(reader)
		}
	}()
	if err := store.Release(selection.ID); err == nil {
		t.Fatal("a file opened without delete sharing was removed")
	}
	buffer := make([]byte, 64)
	var count uint32
	if err := windows.ReadFile(reader, buffer, &count, nil); err != nil || string(buffer[:count]) != "complete copied bytes" {
		t.Fatal("the external reader lost its file", err)
	}
	if err := windows.CloseHandle(reader); err != nil {
		t.Fatal(err)
	}
	reader = 0
	if err := store.Release(selection.ID); err != nil {
		t.Fatal("release could not retry", err)
	}
	if _, err := os.Stat(selection.Paths[0]); !os.IsNotExist(err) {
		t.Fatal("released cache remained")
	}
}
