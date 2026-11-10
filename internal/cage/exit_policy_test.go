package cage

import "testing"

func TestExitPolicyStaticAndGeographicChanges(t *testing.T) {
	base := ExitLocation{IP: "8.8.8.8", CountryCode: "US", Region: "California", City: "Los Angeles"}
	city := ExitPolicy{Mode: "dynamic", Scope: "city"}.BindMissing(base)
	country := city
	country.Scope = "country"
	region := city
	region.Scope, region.City = "region", ""
	static := ExitPolicy{Mode: "static"}.BindMissing(base)
	for _, tc := range []struct {
		name     string
		policy   ExitPolicy
		location ExitLocation
		want     CheckState
	}{
		{"same IP", static, base, CheckPassed},
		{"static changes", static, ExitLocation{IP: "1.1.1.1", CountryCode: "US"}, CheckFailed},
		{"same city new IP", city, ExitLocation{IP: "1.1.1.1", CountryCode: "US", Region: "california", City: "Los Angeles"}, CheckPassed},
		{"city moves", city, ExitLocation{IP: "1.1.1.1", CountryCode: "US", Region: "California", City: "San Jose"}, CheckFailed},
		{"same name different state", city, ExitLocation{IP: "1.1.1.1", CountryCode: "US", Region: "Texas", City: "Los Angeles"}, CheckFailed},
		{"country allows city change", country, ExitLocation{IP: "1.1.1.1", CountryCode: "US", Region: "New York", City: "New York"}, CheckPassed},
		{"country moves", country, ExitLocation{IP: "1.1.1.1", CountryCode: "JP", Region: "Tokyo", City: "Tokyo"}, CheckFailed},
		{"missing city", city, ExitLocation{IP: "1.1.1.1", CountryCode: "US", Region: "California"}, CheckUnknown},
		{"region allows city change", region, ExitLocation{IP: "1.1.1.1", CountryCode: "US", Region: "california", City: "San Jose"}, CheckPassed},
		{"region does not require city", region, ExitLocation{IP: "1.1.1.1", CountryCode: "US", Region: "California"}, CheckPassed},
		{"region changes", region, ExitLocation{IP: "1.1.1.1", CountryCode: "US", Region: "Texas"}, CheckFailed},
		{"region country changes", region, ExitLocation{IP: "1.1.1.1", CountryCode: "CA", Region: "California"}, CheckFailed},
		{"region evidence missing", region, ExitLocation{IP: "1.1.1.1", CountryCode: "US"}, CheckUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := tc.policy.Evaluate(tc.location.IP, &tc.location)
			if got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
	if got, _ := city.Evaluate("1.1.1.1", &base); got != CheckUnknown {
		t.Fatal("location for another IP was accepted")
	}
	if got, _ := country.Evaluate("1.1.1.1", nil); got != CheckUnknown {
		t.Fatal("missing geography was accepted")
	}
	if got, _ := (ExitPolicy{Mode: "dynamic", Scope: "any"}).Evaluate("1.1.1.1", nil); got != CheckUnknown {
		t.Fatal("unrestricted dynamic mode bypassed mandatory country evidence")
	}
	if changed := static.BindMissing(ExitLocation{IP: "1.1.1.1"}); changed.IP != static.IP {
		t.Fatal("recheck silently rebound static IP")
	}
	if changed := city.BindMissing(ExitLocation{CountryCode: "JP", Region: "Tokyo", City: "Tokyo"}); changed != city {
		t.Fatal("recheck silently rebound city")
	}
	boundRegion := (ExitPolicy{Mode: "dynamic", Scope: "region"}).BindMissing(base)
	if boundRegion.NeedsBinding() || boundRegion.Region != base.Region || boundRegion.City != "" {
		t.Fatalf("region binding: %+v", boundRegion)
	}
	if changed := boundRegion.BindMissing(ExitLocation{CountryCode: "US", Region: "Texas"}); changed != boundRegion {
		t.Fatal("recheck silently rebound region")
	}
	if !(ExitPolicy{Mode: "dynamic", Scope: "region", CountryCode: "JP"}).NeedsBinding() {
		t.Fatal("region without an anchor accepted")
	}
}
