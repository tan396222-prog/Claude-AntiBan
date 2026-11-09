// SPDX-License-Identifier: Apache-2.0
package dns

import (
	"context"
	"errors"
	"testing"

	"github.com/miekg/dns"
)

func TestCageDNSFailureDoesNotUseStaticOrHostResolver(t *testing.T) {
	previous := Exchange
	t.Cleanup(func() { Exchange = previous })
	Exchange = func(context.Context, *dns.Msg) (*dns.Msg, error) { return nil, errors.New("proxy down") }
	h, err := NewHandler(HandlerOptions{StaticHosts: map[string]string{"example.com": "1.1.1.1"}})
	if err != nil {
		t.Fatal(err)
	}
	q := new(dns.Msg)
	q.SetQuestion("example.com.", dns.TypeA)
	h.ServeDNS(TestResponseWriter{}, q)
	if dnsResult.Rcode != dns.RcodeServerFailure || len(dnsResult.Answer) != 0 {
		t.Fatal("expected SERVFAIL without static/host fallback")
	}
}
