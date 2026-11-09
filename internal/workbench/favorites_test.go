package workbench

import (
	"encoding/json"
	"fmt"
	"github.com/sylvinus/agent-vm/internal/host"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestFavoritesPersistenceAndFolders(t *testing.T) {
	s := testServer(t)
	post := func(body string) Favorites {
		t.Helper()
		w := request(s, "POST", "/api/favorites", s.origin, body, true)
		if w.Code != 200 {
			t.Fatalf("%d: %s", w.Code, w.Body.String())
		}
		var f Favorites
		if err := json.Unmarshal(w.Body.Bytes(), &f); err != nil {
			t.Fatal(err)
		}
		return f
	}
	f := post(`{"action":"save_folder","title":"开发"}`)
	folder := f.Folders[0].ID
	f = post(fmt.Sprintf(`{"action":"save","title":"文档","url":"https://example.com/docs","folder_id":%q}`, folder))
	id := f.Items[0].ID
	f = post(`{"action":"save","title":"文档新版","url":"https://example.com/docs"}`)
	if len(f.Items) != 1 || f.Items[0].ID != id || f.Items[0].Title != "文档新版" {
		t.Fatal("repeated save duplicated a favorite")
	}
	f = post(fmt.Sprintf(`{"action":"save","id":%q,"title":"已移动","url":"https://example.com/new","folder_id":%q}`, id, folder))
	if f.Items[0].FolderID != folder {
		t.Fatal("move failed")
	}
	post(fmt.Sprintf(`{"action":"save_folder","id":%q,"title":"工作"}`, folder))
	// A new server instance reads the saved file without an active VM.
	reopened := &Server{profile: s.profile}
	f, err := reopened.readFavorites()
	if err != nil || f.Folders[0].Title != "工作" || f.Items[0].URL != "https://example.com/new" {
		t.Fatalf("persistence: %+v %v", f, err)
	}
	if host.CheckPrivateFile(filepath.Join(s.profile, "favorites.json")) != nil {
		t.Fatal("favorites file must be private")
	}
	f = post(fmt.Sprintf(`{"action":"delete_folder","id":%q}`, folder))
	if len(f.Folders) != 0 || len(f.Items) != 1 || f.Items[0].FolderID != "" {
		t.Fatal("deleting folder must retain its favorites in unfiled")
	}
	f = post(fmt.Sprintf(`{"action":"delete","id":%q}`, id))
	if len(f.Items) != 0 {
		t.Fatal("delete failed")
	}
}

func TestFavoritesBoundaryAndFailedWrites(t *testing.T) {
	s := testServer(t)
	if w := request(s, "GET", "/api/favorites", "", "", false); w.Code != 401 {
		t.Fatal("unauthenticated favorites readable")
	}
	if w := request(s, "POST", "/api/favorites", "https://example.com", `{}`, true); w.Code != 403 {
		t.Fatal("cross-origin mutation accepted")
	}
	for _, body := range []string{
		`{"action":"save","title":"x","url":"javascript:alert(1)"}`,
		`{"action":"save","title":"x","url":"file:///etc/passwd"}`,
		`{"action":"save","title":"x","url":"https://user:secret@example.com"}`,
		`{"action":"save","title":"x","url":"https://example.com","folder_id":"missing"}`,
		`{"action":"save_folder","title":" "}`,
	} {
		if w := request(s, "POST", "/api/favorites", s.origin, body, true); w.Code == 200 {
			t.Fatalf("accepted invalid favorite: %s", body)
		}
	}
	path := filepath.Join(s.profile, "favorites.json")
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	w := request(s, "POST", "/api/favorites", s.origin, `{"action":"save_folder","title":"x"}`, true)
	b, _ := os.ReadFile(path)
	if w.Code == 200 || string(b) != "broken" {
		t.Fatal("unreadable saved data was overwritten")
	}
}

func TestConcurrentFavoritesKeepBothAdditions(t *testing.T) {
	s := testServer(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			body := fmt.Sprintf(`{"action":"save","title":"Page %d","url":"https://example.com/%d"}`, i, i)
			if w := request(s, "POST", "/api/favorites", s.origin, body, true); w.Code != 200 {
				t.Errorf("save: %d", w.Code)
			}
		})
	}
	wg.Wait()
	f, err := s.readFavorites()
	if err != nil || len(f.Items) != 8 {
		t.Fatalf("concurrent additions lost: %+v, %v", f, err)
	}
}
