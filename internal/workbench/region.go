package workbench

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/sylvinus/agent-vm/internal/cage"
)

// These are product defaults for common regions, not a claim that a country
// has one language. Unlisted regions retain the user's applied language.
var regionLanguage = map[string]string{
	"US": "en", "GB": "en", "CA": "en", "AU": "en", "NZ": "en", "IE": "en", "SG": "en", "IN": "en", "PH": "en", "ZA": "en",
	"JP": "ja", "KR": "ko", "CN": "zh", "TW": "zh", "HK": "zh",
	"DE": "de", "AT": "de", "CH": "de", "FR": "fr", "BE": "nl", "NL": "nl", "SE": "sv", "FI": "fi", "NO": "nb", "DK": "da",
	"ES": "es", "MX": "es", "AR": "es", "CL": "es", "PT": "pt", "BR": "pt", "IT": "it", "PL": "pl", "CZ": "cs", "RO": "ro", "HU": "hu", "GR": "el",
	"ID": "id", "MY": "ms", "VN": "vi", "IL": "he", "TR": "tr", "UA": "uk", "RU": "ru", "AE": "ar", "SA": "ar",
}

func languageSuggestion(country string) (locale, browser string) {
	if language := regionLanguage[country]; language != "" {
		return language + "_" + country + ".UTF-8", language + "-" + country
	}
	return "", ""
}

func autoRegionSettings(cfg cage.Config, preferences Preferences, location Location) cage.Config {
	if preferences.AutoTimezone && location.Timezone != "" {
		cfg.Timezone = location.Timezone
	}
	if preferences.AutoLanguage {
		if locale, browser := languageSuggestion(location.CountryCode); locale != "" {
			cfg.Locale, cfg.BrowserLanguage = locale, browser
		}
	}
	return cfg
}

// RegionPreset is one "one-click" timezone and language combination. Applying
// a preset writes the same timezone/locale/browser_language settings as the
// manual form and turns both automatic options off, so the next start applies
// it to the guest system, CLI environment and browser together.
type RegionPreset struct {
	ID              string `json:"id"`
	Label           string `json:"label"`
	LabelEN         string `json:"label_en"`
	CountryCode     string `json:"country_code"`
	Timezone        string `json:"timezone"`
	Locale          string `json:"locale"`
	BrowserLanguage string `json:"browser_language"`
}

// Ordered so that the first preset of a country is its default suggestion.
var regionPresets = []RegionPreset{
	{"us-west", "美国西部（洛杉矶）", "US West (Los Angeles)", "US", "America/Los_Angeles", "en_US.UTF-8", "en-US"},
	{"us-mountain", "美国山地（丹佛）", "US Mountain (Denver)", "US", "America/Denver", "en_US.UTF-8", "en-US"},
	{"us-central", "美国中部（芝加哥）", "US Central (Chicago)", "US", "America/Chicago", "en_US.UTF-8", "en-US"},
	{"us-east", "美国东部（纽约）", "US East (New York)", "US", "America/New_York", "en_US.UTF-8", "en-US"},
	{"ca-east", "加拿大（多伦多）", "Canada (Toronto)", "CA", "America/Toronto", "en_CA.UTF-8", "en-CA"},
	{"ca-west", "加拿大（温哥华）", "Canada (Vancouver)", "CA", "America/Vancouver", "en_CA.UTF-8", "en-CA"},
	{"gb", "英国（伦敦）", "United Kingdom (London)", "GB", "Europe/London", "en_GB.UTF-8", "en-GB"},
	{"ie", "爱尔兰（都柏林）", "Ireland (Dublin)", "IE", "Europe/Dublin", "en_IE.UTF-8", "en-IE"},
	{"de", "德国（柏林）", "Germany (Berlin)", "DE", "Europe/Berlin", "de_DE.UTF-8", "de-DE"},
	{"fr", "法国（巴黎）", "France (Paris)", "FR", "Europe/Paris", "fr_FR.UTF-8", "fr-FR"},
	{"nl", "荷兰（阿姆斯特丹）", "Netherlands (Amsterdam)", "NL", "Europe/Amsterdam", "nl_NL.UTF-8", "nl-NL"},
	{"jp", "日本（东京）", "Japan (Tokyo)", "JP", "Asia/Tokyo", "ja_JP.UTF-8", "ja-JP"},
	{"kr", "韩国（首尔）", "South Korea (Seoul)", "KR", "Asia/Seoul", "ko_KR.UTF-8", "ko-KR"},
	{"sg", "新加坡", "Singapore", "SG", "Asia/Singapore", "en_SG.UTF-8", "en-SG"},
	{"hk", "中国香港", "Hong Kong", "HK", "Asia/Hong_Kong", "zh_HK.UTF-8", "zh-HK"},
	{"tw", "中国台湾", "Taiwan", "TW", "Asia/Taipei", "zh_TW.UTF-8", "zh-TW"},
	{"au", "澳大利亚（悉尼）", "Australia (Sydney)", "AU", "Australia/Sydney", "en_AU.UTF-8", "en-AU"},
}

func regionPresetByID(id string) (RegionPreset, bool) {
	for _, preset := range regionPresets {
		if preset.ID == id {
			return preset, true
		}
	}
	return RegionPreset{}, false
}

// recommendedRegionPreset matches the observed exit: an exact timezone match
// wins, otherwise the country's first preset. Unknown exits get no suggestion.
func recommendedRegionPreset(location *Location) string {
	if location == nil {
		return ""
	}
	for _, preset := range regionPresets {
		if location.Timezone != "" && preset.Timezone == location.Timezone {
			return preset.ID
		}
	}
	for _, preset := range regionPresets {
		if location.CountryCode != "" && preset.CountryCode == location.CountryCode {
			return preset.ID
		}
	}
	return ""
}

// currentRegionPreset reports which preset the saved settings already equal.
func currentRegionPreset(cfg cage.Config, p Preferences) string {
	if p.AutoTimezone || p.AutoLanguage {
		return ""
	}
	for _, preset := range regionPresets {
		if preset.Timezone == cfg.Timezone && preset.Locale == cfg.Locale && preset.BrowserLanguage == cfg.BrowserLanguage {
			return preset.ID
		}
	}
	return ""
}

// browserLanguageForLocale derives a web language tag such as en-US from
// en_US.UTF-8 when a custom combination omits it.
func browserLanguageForLocale(locale string) string {
	tag, _, _ := strings.Cut(locale, ".")
	return strings.ReplaceAll(tag, "_", "-")
}

type regionApplyInput struct {
	Preset          string `json:"preset,omitempty"`
	Timezone        string `json:"timezone,omitempty"`
	Locale          string `json:"locale,omitempty"`
	BrowserLanguage string `json:"browser_language,omitempty"`
	FollowExit      bool   `json:"follow_exit,omitempty"`
	DryRun          bool   `json:"dry_run,omitempty"`
}

// regionPatch converts one-click input into the ordinary settings patch.
func regionPatch(in regionApplyInput) (map[string]json.RawMessage, error) {
	values := map[string]any{}
	switch {
	case in.FollowExit:
		if in.Preset != "" || in.Timezone != "" || in.Locale != "" || in.BrowserLanguage != "" {
			return nil, errors.New("跟随出口时不能同时指定预设、时区或语言")
		}
		values["auto_timezone"], values["auto_language"] = true, true
	case in.Preset != "":
		if in.Timezone != "" || in.Locale != "" || in.BrowserLanguage != "" {
			return nil, errors.New("使用预设时不能同时指定自定义时区或语言")
		}
		preset, ok := regionPresetByID(in.Preset)
		if !ok {
			return nil, errors.New("未知的时区与语言预设")
		}
		values["timezone"], values["locale"], values["browser_language"] = preset.Timezone, preset.Locale, preset.BrowserLanguage
		values["auto_timezone"], values["auto_language"] = false, false
	default:
		if in.Timezone == "" || in.Locale == "" {
			return nil, errors.New("自定义需要同时提供时区和系统语言，例如 America/Los_Angeles 和 en_US.UTF-8")
		}
		language := in.BrowserLanguage
		if language == "" {
			language = browserLanguageForLocale(in.Locale)
		}
		values["timezone"], values["locale"], values["browser_language"] = in.Timezone, in.Locale, language
		values["auto_timezone"], values["auto_language"] = false, false
	}
	patch := map[string]json.RawMessage{}
	for key, value := range values {
		patch[key], _ = json.Marshal(value)
	}
	return patch, nil
}

func (s *Server) getRegionPresets(w http.ResponseWriter, r *http.Request) {
	c, err := loadConfig(s.configPath)
	if err != nil {
		requestError(w, err)
		return
	}
	s.mu.Lock()
	p, location := s.preferences, s.location
	s.mu.Unlock()
	jsonReply(w, map[string]any{
		"presets":     regionPresets,
		"recommended": recommendedRegionPreset(location),
		"current":     currentRegionPreset(c, p),
		"settings":    map[string]any{"timezone": c.Timezone, "locale": c.Locale, "browser_language": c.BrowserLanguage, "auto_timezone": p.AutoTimezone, "auto_language": p.AutoLanguage},
		"location":    location,
	})
}

// applyRegion is the one-click entry: it validates and applies through the
// same settings job as the form, restarting a running environment once.
func (s *Server) applyRegion(w http.ResponseWriter, r *http.Request) {
	var in regionApplyInput
	if !readJSON(w, r, &in) {
		return
	}
	patch, err := regionPatch(in)
	if err != nil {
		requestError(w, err)
		return
	}
	v, err := s.proposePatchedSettings(patch)
	if err != nil {
		requestError(w, err)
		return
	}
	if in.DryRun {
		jsonReply(w, settingsView(v.Config, v.Preferences))
		return
	}
	s.launch(w, "settings", func() error {
		v, err := s.proposePatchedSettings(patch)
		if err != nil {
			return err
		}
		return s.apply(v, true)
	})
}
