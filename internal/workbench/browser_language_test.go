package workbench

import (
	"strings"
	"testing"

	"github.com/sylvinus/agent-vm/internal/cage"
)

func TestBrowserSettingsJapaneseChromiumLocale(t *testing.T) {
	cfg := cage.DefaultConfig()
	cfg.Timezone, cfg.BrowserLanguage = "Asia/Tokyo", "ja-JP"
	for _, language := range []string{"ja", "JA", "ja-JP", "ja-US", "en-US", ""} {
		v := browserObservation{Timezone: cfg.Timezone, ExpectedTimezone: cfg.Timezone, Language: language, Platform: "Linux x86_64"}
		want := language == "ja" || language == "JA" || language == "ja-JP"
		if got := browserSettingsMatch(v, cfg); got != want {
			t.Errorf("Japanese browser language %q: got %t, want %t", language, got, want)
		}
	}
	cfg.BrowserLanguage = "zh-TW"
	v := browserObservation{Timezone: cfg.Timezone, ExpectedTimezone: cfg.Timezone, Language: "zh", Platform: "Linux x86_64"}
	if browserSettingsMatch(v, cfg) {
		t.Fatal("regional distinction in Chinese was discarded")
	}
}

func TestBrowserSettingsReportsOnlyMismatchedFields(t *testing.T) {
	cfg := cage.DefaultConfig()
	cfg.Timezone, cfg.BrowserLanguage = "Asia/Taipei", "zh-TW"
	valid := browserObservation{Timezone: "Asia/Taipei", ExpectedTimezone: "Asia/Taipei", Language: "zh-TW", Platform: "Linux x86_64"}
	for _, field := range []string{"none", "language", "timezone", "platform", "all", "timezone-alias", "language-case"} {
		t.Run(field, func(t *testing.T) {
			v := valid
			var want []string
			switch field {
			case "language":
				v.Language = "en-US"
				want = []string{"语言不一致", `"zh-TW"`, `"en-US"`}
			case "timezone":
				v.Timezone = "UTC"
				want = []string{"时区不一致", `"Asia/Taipei"`, `"UTC"`}
			case "platform":
				v.Platform = "MacIntel"
				want = []string{"系统标识不一致", "Linux", `"MacIntel"`}
			case "all":
				v = browserObservation{}
				want = []string{"时区不一致", "语言不一致", "系统标识不一致"}
			case "timezone-alias":
				v.Timezone, v.ExpectedTimezone = "Asia/Calcutta", "Asia/Calcutta"
			case "language-case":
				v.Language = "zh-tw"
			}
			mismatch := browserSettingsMismatch(v, cfg)
			if browserSettingsMatch(v, cfg) != (len(want) == 0) {
				t.Fatalf("unexpected match: %s", mismatch)
			}
			for _, text := range want {
				if !strings.Contains(mismatch, text) {
					t.Fatalf("missing %q in %q", text, mismatch)
				}
			}
			if len(want) == 0 && mismatch != "" {
				t.Fatal(mismatch)
			}
			if field == "language" && (strings.Contains(mismatch, "时区") || strings.Contains(mismatch, "系统标识")) {
				t.Fatalf("language failure blamed unrelated fields: %s", mismatch)
			}
		})
	}
}
