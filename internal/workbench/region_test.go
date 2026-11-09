package workbench

import (
	"github.com/sylvinus/agent-vm/internal/cage"
	"testing"
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
