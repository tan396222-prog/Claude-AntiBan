package workbench

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sylvinus/agent-vm/internal/cage"
)

func TestAutomaticRegionSettingsPreserveManualOverrides(t *testing.T) {
	cfg := cage.DefaultConfig()
	loc := Location{CountryCode: "JP", Timezone: "Asia/Tokyo"}
	manual := autoRegionSettings(cfg, Preferences{}, loc)
	if manual != cfg {
		t.Fatal("location changed manually selected settings")
	}
	tzOnly := autoRegionSettings(cfg, Preferences{AutoTimezone: true}, loc)
	if tzOnly.Timezone != "Asia/Tokyo" || tzOnly.Locale != cfg.Locale {
		t.Fatal("timezone option changed language")
	}
	languageOnly := autoRegionSettings(cfg, Preferences{AutoLanguage: true}, loc)
	if languageOnly.Locale != "ja_JP.UTF-8" || languageOnly.BrowserLanguage != "ja-JP" || languageOnly.Timezone != cfg.Timezone {
		t.Fatal("language option did not preserve independent timezone")
	}
	unknown := autoRegionSettings(cfg, Preferences{AutoLanguage: true}, Location{CountryCode: "ZZ"})
	if unknown != cfg {
		t.Fatal("unknown region invented a language setting")
	}
}

func TestMissingExitPolicyDoesNotRelaxSavedStaticRule(t *testing.T) {
	s := testServer(t)
	old := cage.DefaultConfig()
	old.ExitPolicy = cage.ExitPolicy{Mode: "static", Scope: "country", IP: "8.8.8.8"}
	input := settingsView(old, Preferences{Keyboard: "mac"})
	input.ExitPolicy = cage.ExitPolicy{}
	got, _, err := proposedSettings(input, old, s.profile)
	if err != nil {
		t.Fatal(err)
	}
	if got.ExitPolicy != old.ExitPolicy {
		t.Fatal("legacy form silently relaxed static IP")
	}
}

func TestRegionPresetsAreValidSettings(t *testing.T) {
	seen := map[string]bool{}
	for _, preset := range regionPresets {
		if seen[preset.ID] {
			t.Fatalf("duplicate preset %s", preset.ID)
		}
		seen[preset.ID] = true
		cfg := cage.DefaultConfig()
		cfg.Timezone, cfg.Locale, cfg.BrowserLanguage = preset.Timezone, preset.Locale, preset.BrowserLanguage
		if err := cfg.Validate(); err != nil {
			t.Fatalf("%s: %v", preset.ID, err)
		}
		if preset.Label == "" || preset.LabelEN == "" || preset.CountryCode == "" {
			t.Fatalf("%s lacks labels", preset.ID)
		}
	}
}

func TestRecommendedRegionPresetFollowsExit(t *testing.T) {
	for _, c := range []struct {
		loc  *Location
		want string
	}{
		{nil, ""},
		{&Location{CountryCode: "US", Timezone: "America/New_York"}, "us-east"},
		{&Location{CountryCode: "US", Timezone: "America/Phoenix"}, "us-west"},
		{&Location{CountryCode: "JP"}, "jp"},
		{&Location{CountryCode: "ZZ", Timezone: "Etc/UTC"}, ""},
	} {
		if got := recommendedRegionPreset(c.loc); got != c.want {
			t.Fatalf("%+v: got %q want %q", c.loc, got, c.want)
		}
	}
}

func TestRegionPatch(t *testing.T) {
	patch, err := regionPatch(regionApplyInput{Preset: "us-west"})
	if err != nil || string(patch["timezone"]) != `"America/Los_Angeles"` || string(patch["locale"]) != `"en_US.UTF-8"` ||
		string(patch["browser_language"]) != `"en-US"` || string(patch["auto_timezone"]) != "false" || string(patch["auto_language"]) != "false" {
		t.Fatalf("preset patch: %v %v", patch, err)
	}
	patch, err = regionPatch(regionApplyInput{Timezone: "Europe/Berlin", Locale: "de_DE.UTF-8"})
	if err != nil || string(patch["browser_language"]) != `"de-DE"` {
		t.Fatalf("custom patch: %v %v", patch, err)
	}
	patch, err = regionPatch(regionApplyInput{FollowExit: true})
	if err != nil || string(patch["auto_timezone"]) != "true" || string(patch["auto_language"]) != "true" || patch["timezone"] != nil {
		t.Fatalf("follow patch: %v %v", patch, err)
	}
	for _, bad := range []regionApplyInput{{}, {Preset: "nowhere"}, {Timezone: "Asia/Tokyo"}, {Preset: "jp", Locale: "ja_JP.UTF-8"}, {FollowExit: true, Preset: "jp"}} {
		if _, err := regionPatch(bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}

func TestRegionApplyDryRunUsesSettingsValidator(t *testing.T) {
	s := testServer(t)
	w := request(s, "POST", "/api/region/apply", s.origin, `{"preset":"jp","dry_run":true}`, true)
	if w.Code != 200 {
		t.Fatalf("dry run: %d %s", w.Code, w.Body)
	}
	var view Settings
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Timezone != "Asia/Tokyo" || view.Locale != "ja_JP.UTF-8" || view.BrowserLanguage != "ja-JP" || view.AutoTimezone || view.AutoLanguage {
		t.Fatalf("dry run view: %+v", view)
	}
	if saved, _ := cage.Load(s.configPath); saved.Timezone != cage.DefaultConfig().Timezone {
		t.Fatal("dry run wrote the configuration")
	}
	if w := request(s, "POST", "/api/region/apply", s.origin, `{"timezone":"Mars/Base","locale":"en_US.UTF-8","dry_run":true}`, true); w.Code != 400 {
		t.Fatalf("invalid timezone accepted: %d", w.Code)
	}
	w = request(s, "GET", "/api/region/presets", s.origin, "", true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"us-west"`) {
		t.Fatalf("presets: %d %s", w.Code, w.Body)
	}
}
