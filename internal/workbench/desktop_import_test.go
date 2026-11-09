package workbench

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sylvinus/agent-vm/internal/remotehost"
)

func TestDesktopImportPreservesBothLegacyProfilesAndRemovedHost(t *testing.T) {
	full := t.TempDir()
	entry := filepath.Dir(desktopConfigPath(filepath.Join(full, "config.json")))
	client := filepath.Join(full, "client")
	first, err := remotehost.NewStore(full).Upsert(remotehost.HostProfile{Name: "full", Host: "full.example", User: "test", Auth: remotehost.AuthConfig{Kind: "agent"}})
	if err != nil {
		t.Fatal(err)
	}
	other, err := remotehost.NewStore(client).Upsert(remotehost.HostProfile{Name: "client", Host: "client.example", User: "test", Auth: remotehost.AuthConfig{Kind: "agent"}})
	if err != nil {
		t.Fatal(err)
	}
	id := "722f6417-7290-42d6-95f9-8de08970f23d"
	instance := randomToken()
	saved := SavedDownload{ID: id, InstanceID: instance, Name: "result.txt", Path: filepath.Join(t.TempDir(), "result.txt"), SavedAt: time.Now().UTC()}
	if err := writePrivate(savedDownloadPath(filepath.Join(client, "remote-downloads", instance), id), saved); err != nil {
		t.Fatal(err)
	}
	if err := importDesktopConnections(full, entry); err != nil {
		t.Fatal(err)
	}
	dest := remotehost.NewStore(entry)
	if _, err = dest.Get(first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = dest.Get(other.ID); err != nil {
		t.Fatal(err)
	}
	result, err := readSavedDownload(filepath.Join(entry, "remote-downloads", instance), id)
	if err != nil || result.Path != saved.Path || !result.SavedAt.Equal(saved.SavedAt) {
		t.Fatalf("imported saved download: %+v %v", result, err)
	}
	if err = dest.Delete(first.ID); err != nil {
		t.Fatal(err)
	}
	if err = importDesktopConnections(full, entry); err != nil {
		t.Fatal(err)
	}
	hosts, err := dest.List()
	if err != nil || len(hosts) != 1 || hosts[0].ID != other.ID {
		t.Fatalf("restored deleted host: %+v %v", hosts, err)
	}
	if _, err = remotehost.NewStore(full).Get(first.ID); err != nil {
		t.Fatal("old profile was modified")
	}
	if _, err = os.Stat(saved.Path); !os.IsNotExist(err) {
		t.Fatal("migration touched actual destination")
	}
}
