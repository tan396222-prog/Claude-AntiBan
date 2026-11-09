package workbench

import "github.com/sylvinus/agent-vm/internal/cage"

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
