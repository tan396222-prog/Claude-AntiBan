package cage

import (
	"os"
	"strings"
	"testing"
)

func TestStorageRefusesVolumeWithIgnoredOwnership(t *testing.T) {
	path := os.Getenv("CAGE_TEST_NOOWNERS_VOLUME")
	if path == "" {
		t.Skip("set CAGE_TEST_NOOWNERS_VOLUME to a test volume mounted noowners")
	}
	err := localStorageVolume(path)
	if err == nil || !strings.Contains(err.Error(), "所有权") {
		t.Fatalf("noowners volume check: %v", err)
	}
}
