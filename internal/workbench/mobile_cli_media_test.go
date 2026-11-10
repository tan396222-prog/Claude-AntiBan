package workbench

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestMobileCLIMediaReadsOnlyCurrentProjectMedia(t *testing.T) {
	s := testServer(t)
	project, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := writePrivate(filepath.Join(s.profile, "environment.json"), map[string]string{"directory": project}); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	if _, err := s.mobileCLI.create(s.profile, project, id); err != nil {
		t.Fatal(err)
	}
	record, err := s.mobileCLI.record(s.profile, id)
	if err != nil {
		t.Fatal(err)
	}
	record.Turns = []mobileTurn{{ID: uuid.NewString(), Prompt: "查看预览", Text: "![帧](frame%20one.png) `outside.png` `fake.png` `../other.png`"}}
	if err := writePrivate(mobileRecordPath(s.profile, id), record); err != nil {
		t.Fatal(err)
	}
	payload := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, mobileMediaChunk+14)...)
	if err := os.WriteFile(filepath.Join(project, "frame one.png"), payload, 0600); err != nil {
		t.Fatal(err)
	}
	check := func(ref string, offset int64, version string, status int) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(mediaChunkRequest{Reference: ref, Offset: offset, Version: version})
		w := request(s, "POST", "/api/mobile/cli/sessions/"+id+"/media", s.origin, string(body), true)
		if w.Code != status {
			t.Fatalf("%s: got %d, want %d", ref, w.Code, status)
		}
		return w
	}
	var first, last struct {
		Data    []byte
		Version string
		Mime    string
	}
	if err := json.Unmarshal(check("frame%20one.png", 0, "", 200).Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(check("frame%20one.png", int64(len(first.Data)), first.Version, 200).Body.Bytes(), &last); err != nil {
		t.Fatal(err)
	}
	if first.Mime != "image/png" || !bytes.Equal(append(first.Data, last.Data...), payload) {
		t.Fatal("incomplete image")
	}
	check("frame%20one.png", int64(len(first.Data)), "stale", 409)
	check("unmentioned.png", 0, "", 403)
	check("../other.png", 0, "", 403)
	if err := os.WriteFile(filepath.Join(project, "fake.png"), []byte("plain text"), 0600); err != nil {
		t.Fatal(err)
	}
	check("fake.png", 0, "", 415)
	outside := filepath.Join(t.TempDir(), "outside.png")
	if err := os.WriteFile(outside, payload, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(project, "outside.png")); err == nil {
		check("outside.png", 0, "", 404)
	}
	s.account.lock()
	check("frame%20one.png", 0, "", 423)
}
