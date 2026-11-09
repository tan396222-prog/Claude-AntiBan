// SPDX-License-Identifier: Apache-2.0
package dns

import (
	"context"
	"errors"
	"testing"

	"github.com/miekg/dns"
)

type cageWriter struct {
	dns.ResponseWriter
	msg *dns.Msg
}

func (w *cageWriter) WriteMsg(m *dns.Msg) error { w.msg = m; return nil }

func TestCageDNSFailureDoesNotUseHostResolver(t *testing.T) {
	q := new(dns.Msg)
	q.SetQuestion("example.com.", dns.TypeA)
	w := &cageWriter{}
	// A nil upstream would panic if the ordinary resolver path were used.
	h := &dnsHandler{exchange: func(context.Context, *dns.Msg) (*dns.Msg, error) { return nil, errors.New("proxy down") }}
	h.handle(w, q, dns.MaxMsgSize)
	if w.msg == nil || w.msg.Rcode != dns.RcodeServerFailure {
		t.Fatal("expected fail-closed SERVFAIL")
	}
}
