package cage

import "testing"

func TestExitPolicyStaticAndGeographicChanges(t *testing.T) {
	base := ExitLocation{IP: "8.8.8.8", CountryCode: "US", Region: "California", City: "Los Angeles"}
	city := ExitPolicy{Mode: "dynamic", Scope: "city"}.BindMissing(base)
	country := city
	country.Scope = "country"
	static := ExitPolicy{Mode: "static"}.BindMissing(base)
	for _, tc := range []struct {
		name     string
		policy   ExitPolicy
		location ExitLocation
		want     CheckState
	}{
		{"same IP", static, base, CheckPassed},
		{"static changes", static, ExitLocation{IP: "1.1.1.1"}, CheckFailed},
		{"same city new IP", city, ExitLocation{IP: "1.1.1.1", CountryCode: "US", Region: "california", City: "Los Angeles"}, CheckPassed},
		{"city moves", city, ExitLocation{IP: "1.1.1.1", CountryCode: "US", Region: "California", City: "San Jose"}, CheckFailed},
		{"same name different state", city, ExitLocation{IP: "1.1.1.1", CountryCode: "US", Region: "Texas", City: "Los Angeles"}, CheckFailed},
		{"country allows city change", country, ExitLocation{IP: "1.1.1.1", CountryCode: "US", Region: "New York", City: "New York"}, CheckPassed},
		{"country moves", country, ExitLocation{IP: "1.1.1.1", CountryCode: "JP", Region: "Tokyo", City: "Tokyo"}, CheckFailed},
		{"missing city", city, ExitLocation{IP: "1.1.1.1", CountryCode: "US", Region: "California"}, CheckUnknown},
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
	if got, _ := (ExitPolicy{Mode: "dynamic", Scope: "any"}).Evaluate("1.1.1.1", nil); got != CheckPassed {
		t.Fatal("unrestricted dynamic mode incorrectly required geography")
	}
	if changed := static.BindMissing(ExitLocation{IP: "1.1.1.1"}); changed.IP != static.IP {
		t.Fatal("recheck silently rebound static IP")
	}
	if changed := city.BindMissing(ExitLocation{CountryCode: "JP", Region: "Tokyo", City: "Tokyo"}); changed != city {
		t.Fatal("recheck silently rebound city")
	}
}
