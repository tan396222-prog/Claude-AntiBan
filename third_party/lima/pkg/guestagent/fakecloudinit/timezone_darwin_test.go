// SPDX-FileCopyrightText: Copyright The Lima Authors
// SPDX-License-Identifier: Apache-2.0

package fakecloudinit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSystemSetupUTCZone(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "args")
	cmd := filepath.Join(dir, "systemsetup")
	if err := os.WriteFile(cmd, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CAGE_TEST_ARGS\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("CAGE_TEST_ARGS", log)
	for input, want := range map[string]string{"Etc/UTC": "GMT", "UTC": "GMT", "GMT": "GMT", "Asia/Shanghai": "Asia/Shanghai", "Unlisted/Zone": "Unlisted/Zone"} {
		if err := setTimezone(t.Context(), input); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(log)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(string(data)); got != "-settimezone\n"+want {
			t.Errorf("%s: got %q", input, got)
		}
	}
}
