package exitpolicy

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"testing"
)

//go:embed data/source.json
var countrySource []byte

func TestBundledMainlandListSourceAndCoverage(t *testing.T) {
	var source struct {
		SHA256   string `json:"sha256"`
		Prefixes int    `json:"ipv4_prefixes"`
	}
	if err := json.Unmarshal(countrySource, &source); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(mainlandIPv4))
	if mainlandListError != nil || source.Prefixes != len(mainlandPrefixes) || source.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatal("bundled address list does not match its source metadata", mainlandListError)
	}
	for _, ip := range []string{"114.114.114.114", "180.76.76.76", "223.5.5.5"} {
		if state, _ := CheckExitCountry(ip, nil); state != Failed {
			t.Errorf("mainland address %s accepted", ip)
		}
	}
	for _, ip := range []string{"8.8.8.8", "1.1.1.1"} {
		if state, _ := CheckExitCountry(ip, &Location{IP: ip, CountryCode: "US"}); state != Passed {
			t.Errorf("non-mainland address %s rejected", ip)
		}
	}
	for _, invalid := range []string{"", "0.0.0.0/0", "1.0.1.1/24", "not-a-prefix", "::/0"} {
		if _, err := parseMainlandPrefixes(invalid); err == nil {
			t.Errorf("invalid list accepted: %q", invalid)
		}
	}
}

func TestUnsafeExitCannotBecomeInitialAnchor(t *testing.T) {
	for _, policy := range []Policy{{Mode: "static"}, {Mode: "dynamic", Scope: "country"}} {
		for _, location := range []Location{{IP: "223.5.5.5", CountryCode: "US"}, {IP: "8.8.8.8", CountryCode: "CN"}, {IP: "8.8.8.8"}} {
			if got := policy.BindMissing(location); got != policy.Normalized() {
				t.Fatalf("unsafe exit became an anchor: %+v", got)
			}
		}
	}
}

func TestMainlandExitIsDeniedInEveryMode(t *testing.T) {
	policies := []Policy{
		{Mode: "static", IP: "223.5.5.5"},
		{Mode: "dynamic", Scope: "any"},
		{Mode: "dynamic", Scope: "country", CountryCode: "CN"},
	}
	for _, policy := range policies {
		for _, country := range []string{"", "CN", "US"} {
			location := &Location{IP: "223.5.5.5", CountryCode: country}
			state, message := policy.Evaluate(location.IP, location)
			if state != Failed {
				t.Errorf("mode=%s scope=%s country=%s: got %s (%s), want denied", policy.Mode, policy.Scope, country, state, message)
			}
		}
	}
}

func TestAllModesRequireMatchingNonMainlandCountryEvidence(t *testing.T) {
	for _, policy := range []Policy{{Mode: "static", IP: "8.8.8.8"}, {Mode: "dynamic", Scope: "any"}} {
		for _, tc := range []struct {
			name     string
			location *Location
			want     State
		}{
			{"missing", nil, Unknown},
			{"missing country", &Location{IP: "8.8.8.8"}, Unknown},
			{"wrong address", &Location{IP: "1.1.1.1", CountryCode: "US"}, Unknown},
			{"invalid country", &Location{IP: "8.8.8.8", CountryCode: "??"}, Unknown},
			{"mainland geolocation", &Location{IP: "8.8.8.8", CountryCode: "cn"}, Failed},
			{"United States", &Location{IP: "8.8.8.8", CountryCode: "US"}, Passed},
			{"Hong Kong", &Location{IP: "8.8.8.8", CountryCode: "HK"}, Passed},
			{"Macao", &Location{IP: "8.8.8.8", CountryCode: "MO"}, Passed},
			{"Taiwan", &Location{IP: "8.8.8.8", CountryCode: "TW"}, Passed},
		} {
			t.Run(policy.Mode+"/"+tc.name, func(t *testing.T) {
				if got, message := policy.Evaluate("8.8.8.8", tc.location); got != tc.want {
					t.Fatalf("got %s (%s), want %s", got, message, tc.want)
				}
			})
		}
	}
}
