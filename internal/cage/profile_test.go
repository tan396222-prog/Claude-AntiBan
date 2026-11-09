package cage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigChangeRequiresStop(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "lima", "test"), 0700)
	c := DefaultConfig()
	if err := c.RecordRunning(dir, "test"); err != nil {
		t.Fatal(err)
	}
	if err := c.CheckRunning(dir); err != nil {
		t.Fatal(err)
	}
	c.Timezone = "Asia/Tokyo"
	if c.CheckRunning(dir) == nil {
		t.Fatal("running profile silently changed")
	}
}
