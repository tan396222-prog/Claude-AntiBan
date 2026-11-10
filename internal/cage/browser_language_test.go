package cage

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrowserLanguagePackageMigration(t *testing.T) {
	start := strings.Index(guestStart, "if ! cage_is_ubuntu")
	if start < 0 {
		t.Fatal("existing guests have no language package migration")
	}
	end := strings.Index(guestStart[start:], "\nfi\n")
	if end < 0 {
		t.Fatal("unterminated migration")
	}
	script := "set -euo pipefail\ncage_is_ubuntu() { [ \"$MODE\" = ubuntu ]; }\n" + guestStart[start:start+end+4] + "\nprintf continued\n"
	for _, mode := range []string{"installed", "ubuntu", "missing", "update-failure", "install-failure"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			commands := map[string]string{
				"dpkg-query": "#!/bin/sh\nif [ \"$MODE\" = installed ] || [ -f \"$FIXTURE/installed\" ]; then printf 'install ok installed'; else exit 1; fi\n",
				"sudo":       "#!/bin/sh\nexec \"$@\"\n",
				"apt-get": `#!/bin/sh
set -eu
[ "$DEBIAN_FRONTEND" = noninteractive ]
[ "$APT_CONFIG" = "$FIXTURE/apt.conf" ]
printf '%s\n' "$*" >> "$FIXTURE/requests"
case "$1" in
 update) [ "$MODE" != update-failure ];;
 install)
  [ "$MODE" != install-failure ] || exit 100
  [ "$*" = 'install -y --no-install-recommends chromium-l10n' ]
  touch "$FIXTURE/installed";;
 *) exit 91;;
esac
`,
			}
			for name, body := range commands {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0755); err != nil {
					t.Fatal(err)
				}
			}
			run := func() ([]byte, error) {
				cmd := exec.Command("bash", "-c", script)
				cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "FIXTURE="+dir, "MODE="+mode, "CAGE_APT_CONFIG="+filepath.Join(dir, "apt.conf"))
				return cmd.CombinedOutput()
			}
			out, err := run()
			failed := strings.HasSuffix(mode, "failure")
			if (err != nil) != failed || strings.Contains(string(out), "continued") == failed {
				t.Fatalf("migration %s: %v %s", mode, err, out)
			}
			requests, _ := os.ReadFile(filepath.Join(dir, "requests"))
			if (mode == "installed" || mode == "ubuntu") && len(requests) != 0 {
				t.Fatal("installed language package triggered network access")
			}
			if mode == "update-failure" && strings.Contains(string(requests), "install") {
				t.Fatal("installation continued after failed package index update")
			}
			if mode == "missing" {
				if out, err := run(); err != nil {
					t.Fatalf("second startup: %v %s", err, out)
				}
				again, _ := os.ReadFile(filepath.Join(dir, "requests"))
				if string(again) != string(requests) {
					t.Fatal("second startup reinstalled language package")
				}
			}
		})
	}
}

func TestBrowserLanguagePreferencesReplaceStaleSelection(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("guest Python interpreter unavailable")
	}
	start := strings.Index(guestStart, "import json, os\nfrom pathlib import Path\np = Path.home()")
	if start < 0 {
		t.Fatal("browser preference writer not found")
	}
	end := strings.Index(guestStart[start:], "\nPYCODE")
	script := strings.Replace(guestStart[start:start+end], "Path.home()", `Path(os.environ["FIXTURE"])`, 1)
	dir := t.TempDir()
	profile := filepath.Join(dir, "profile", "Default")
	if err := os.MkdirAll(profile, 0700); err != nil {
		t.Fatal(err)
	}
	prefs := filepath.Join(profile, "Preferences")
	if err := os.WriteFile(prefs, []byte(`{"intl":{"accept_languages":"en-US","selected_languages":"en-US"},"unrelated":{"keep":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, language := range []string{"zh-TW", "fr-FR", "en-US"} {
		cmd := exec.Command("python3", "-c", script)
		cmd.Env = append(os.Environ(), "FIXTURE="+dir, "CAGE_LANGUAGE="+language)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
		data, err := os.ReadFile(prefs)
		if err != nil {
			t.Fatal(err)
		}
		var settings struct {
			Intl      map[string]string `json:"intl"`
			Unrelated map[string]bool   `json:"unrelated"`
		}
		if err := json.Unmarshal(data, &settings); err != nil {
			t.Fatal(err)
		}
		if settings.Intl["accept_languages"] != language || settings.Intl["selected_languages"] != language || !settings.Unrelated["keep"] {
			t.Fatalf("preference update lost language or unrelated settings: %s", data)
		}
	}
}
