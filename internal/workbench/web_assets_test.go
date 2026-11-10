package workbench

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"testing/fstest"
)

func TestPackagedWebAssetsRejectDamageAfterBuild(t *testing.T) {
	files := fstest.MapFS{
		"index.html":     {Data: []byte(`<script type="module" src="/assets/main.js"></script>`)},
		"mobile.html":    {Data: []byte(`<script type="module" src="/assets/main.js"></script>`)},
		"assets/main.js": {Data: []byte("export const valid=true;")},
	}
	hashes := map[string]string{}
	for name, file := range files {
		hash := sha256.Sum256(file.Data)
		hashes[name] = hex.EncodeToString(hash[:])
	}
	manifest, _ := json.Marshal(map[string]any{"version": 1, "files": hashes})
	files["build-integrity.json"] = &fstest.MapFile{Data: manifest}
	if err := verifyWebAssets(files); err != nil {
		t.Fatal(err)
	}
	files["assets/main.js"].Data = make([]byte, 20)
	if err := verifyWebAssets(files); err == nil {
		t.Fatal("damaged embedded JavaScript accepted")
	}
	delete(files, "assets/main.js")
	if err := verifyWebAssets(files); err == nil {
		t.Fatal("missing embedded JavaScript accepted")
	}
	delete(files, "build-integrity.json")
	if err := verifyWebAssets(files); err == nil {
		t.Fatal("unverified build accepted")
	}
}
