package workbench

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCodexAppearanceImportsOnlyAppearance(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	data := `model = "unrelated"
[desktop]
sansFontSize = 16
codeFontSize = 13
appearanceLightCodeThemeId = "solarized"
appearanceDarkCodeThemeId = "ayu"
[desktop.appearanceLightChromeTheme]
accent = "#b58900"
surface = "#fdf6e3"
ink = "#657b83"
contrast = 45
opaqueWindows = false
[desktop.appearanceDarkChromeTheme]
accent = "#e6b450"
surface = "#10141c"
ink = "#bfbdb6"
contrast = 60
opaqueWindows = true
[desktop.appearanceDarkChromeTheme.fonts]
code = "Menlo"
`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".codex-global-state.json"), []byte(`{"reduced-motion-preference":"on","irrelevant":"secret"}`), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := readCodexAppearance(path)
	if err != nil {
		t.Fatal(err)
	}
	if a.Mode != "system" || a.Light.Preset != "solarized" || a.Dark.Surface != "#10141c" || a.UIFontSize != 16 || a.CodeFontSize != 13 || a.Dark.Fonts.Code != "Menlo" || a.ReducedMotion != "on" || !a.FontSmoothing {
		t.Fatalf("unexpected appearance: %+v", a)
	}
	b, _ := json.Marshal(a)
	if bytes.Contains(b, []byte("secret")) || bytes.Contains(b, []byte("unrelated")) {
		t.Fatal("non-appearance data leaked")
	}
}

func TestAppearanceSaveDoesNotChangeEnvironmentAndRejectsBadInput(t *testing.T) {
	s := &Server{profile: t.TempDir(), preferences: Preferences{Workspace: "/project"}, jobs: jobManager{state: Job{Running: true, Kind: "start"}}}
	a := defaultAppearance()
	a.Mode = "dark"
	a.UIFontSize = 16
	b, _ := json.Marshal(a)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/appearance", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	s.saveAppearance(w, req)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if !s.jobs.snapshot().Running || s.jobs.snapshot().Kind != "start" || s.preferences.Workspace != "/project" {
		t.Fatal("appearance changed environment state")
	}
	w = httptest.NewRecorder()
	s.getAppearance(w, httptest.NewRequest("GET", "/api/appearance", nil))
	var saved Appearance
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil || !reflect.DeepEqual(saved, a) {
		t.Fatalf("appearance did not round trip: %s", w.Body.String())
	}
	a.UIFontSize = 400
	b, _ = json.Marshal(a)
	w = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/api/appearance", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	s.saveAppearance(w, req)
	if w.Code == 200 {
		t.Fatal("invalid appearance accepted")
	}
	stored, _ := os.ReadFile(filepath.Join(s.profile, "appearance.json"))
	if bytes.Contains(stored, []byte("400")) {
		t.Fatal("invalid settings replaced saved appearance")
	}
}

func TestAppearanceCustomThemesPersistAndInvalidUpdatesKeepFile(t *testing.T) {
	s := &Server{profile: t.TempDir()}
	a := defaultAppearance()
	a.Light.Colors = map[string]string{"chrome": "#123456", "buttonInk": "#abcdef"}
	a.Dark.Terminal = map[string]string{"background": "#112233", "brightRed": "#aabbcc"}
	a.CustomThemes = []SavedAppearanceTheme{{ID: "my-theme", Name: "我的配色", Light: a.Light, Dark: a.Dark}}
	save := func(a Appearance) *httptest.ResponseRecorder {
		b, _ := json.Marshal(a)
		req := httptest.NewRequest("POST", "/api/appearance", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.saveAppearance(w, req)
		return w
	}
	if w := save(a); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	original, err := os.ReadFile(filepath.Join(s.profile, "appearance.json"))
	if err != nil {
		t.Fatal(err)
	}
	restarted := &Server{profile: s.profile}
	w := httptest.NewRecorder()
	restarted.getAppearance(w, httptest.NewRequest("GET", "/api/appearance", nil))
	var loaded Appearance
	if err := json.Unmarshal(w.Body.Bytes(), &loaded); err != nil || !reflect.DeepEqual(loaded, a) {
		t.Fatalf("lost saved themes: %s", w.Body.String())
	}
	for _, mutate := range []func(*Appearance){
		func(a *Appearance) { a.Light.Colors["chrome"] = "red" },
		func(a *Appearance) { a.Dark.Terminal["unknown"] = "#ffffff" },
		func(a *Appearance) { a.CustomThemes[0].Name = "  " },
		func(a *Appearance) { a.CustomThemes = append(a.CustomThemes, a.CustomThemes[0]) },
		func(a *Appearance) { a.CustomThemes[0].Dark.Colors = map[string]string{"line": "#abc"} },
	} {
		var invalid Appearance
		if err := json.Unmarshal(original, &invalid); err != nil {
			t.Fatal(err)
		}
		mutate(&invalid)
		if w := save(invalid); w.Code == 200 {
			t.Fatal("invalid theme accepted")
		}
		stored, _ := os.ReadFile(filepath.Join(s.profile, "appearance.json"))
		if !bytes.Equal(stored, original) {
			t.Fatal("invalid input replaced valid theme file")
		}
	}
	// The library can exceed the old 64 KB appearance request limit.
	for i := 1; i < 50; i++ {
		item := SavedAppearanceTheme{ID: fmt.Sprintf("theme-%d", i), Name: "配色", Light: a.Light, Dark: a.Dark}
		item.Light.Fonts.UI = strings.Repeat("a", 200)
		item.Light.Fonts.Code = strings.Repeat("b", 200)
		item.Dark.Fonts = item.Light.Fonts
		for _, theme := range []*AppearanceTheme{&item.Light, &item.Dark} {
			theme.Colors = make(map[string]string)
			theme.Terminal = make(map[string]string)
			for _, key := range appearanceColorKeys {
				theme.Colors[key] = "#123456"
			}
			for _, key := range terminalColorKeys {
				theme.Terminal[key] = "#abcdef"
			}
		}
		a.CustomThemes = append(a.CustomThemes, item)
	}
	b, _ := json.Marshal(a)
	if len(b) <= 64<<10 {
		t.Fatal("fixture must exceed old request limit")
	}
	if w := save(a); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	a.CustomThemes = append(a.CustomThemes, SavedAppearanceTheme{ID: "overflow", Name: "超限", Light: a.Light, Dark: a.Dark})
	if w := save(a); w.Code == 200 {
		t.Fatal("library limit not enforced")
	}
}

func TestAppearanceLegacyFileKeepsDerivedDefaults(t *testing.T) {
	s := &Server{profile: t.TempDir()}
	old := defaultAppearance()
	b, _ := json.Marshal(old)
	if err := os.WriteFile(filepath.Join(s.profile, "appearance.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.getAppearance(w, httptest.NewRequest("GET", "/api/appearance", nil))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var loaded Appearance
	if err := json.Unmarshal(w.Body.Bytes(), &loaded); err != nil || !reflect.DeepEqual(loaded, old) {
		t.Fatalf("legacy appearance lost: %s", w.Body.String())
	}
}
