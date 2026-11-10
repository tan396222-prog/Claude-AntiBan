package workbench

import (
	"context"
	"net"
	"net/http"

	"github.com/sylvinus/agent-vm/internal/exitpolicy"
)

func lookupLocation(ctx context.Context, client *http.Client, address net.IP) (Location, error) {
	loc, err := exitpolicy.Lookup(ctx, client, address)
	if err != nil {
		return Location{}, err
	}
	locale, browser := languageSuggestion(loc.CountryCode)
	return Location{
		IP: loc.IP, Country: loc.Country, CountryCode: loc.CountryCode,
		Region: loc.Region, RegionCode: loc.RegionCode, City: loc.City,
		Timezone: loc.Timezone, CheckedAt: loc.CheckedAt,
		SuggestedLocale: locale, SuggestedBrowserLanguage: browser,
	}, nil
}
