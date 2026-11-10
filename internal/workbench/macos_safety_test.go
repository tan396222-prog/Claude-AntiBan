package workbench

import (
	"context"
	"errors"
	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/guestos"
	"testing"
	"time"
)

func TestMacIPv6ScopedRoutesRequireCompleteLiveEvidence(t *testing.T) {
	base := guestObservation{IPv6DefaultRoutes: 1, IPv6UnscopedAbsent: true, IPv6LinkSent: true, IPv6LinkInterface: "en0",
		IPv6Unbound: macIPv6Probe{Result: "no-route"}, IPv6Scoped: []macIPv6Probe{{Name: "utun0", RouteVerified: true, BoundIndex: 7, Result: "timeout"}}}
	before := cage.GateStatus{Mode: cage.GateChecking, RunID: "selected-vm", CheckID: "current-check"}
	after := before
	after.PolicyVerified = true
	after.IPv6LinkProbeCheck = before.CheckID
	if !macOSIPv6LinkVerified(base, before, after) {
		t.Fatal("live matching IPv6 drop proof rejected")
	}
	for _, change := range []func(*guestObservation, *cage.GateStatus){
		func(_ *guestObservation, a *cage.GateStatus) { a.IPv6LinkProbeCheck = "old-check" },
		func(_ *guestObservation, a *cage.GateStatus) { a.RunID = "another-vm" },
		func(b *guestObservation, _ *cage.GateStatus) { b.IPv6Scoped[0].GlobalSources = 1 },
		func(b *guestObservation, _ *cage.GateStatus) { b.IPv6Scoped[0].Result = "connected" },
		func(b *guestObservation, _ *cage.GateStatus) { b.IPv6Scoped[0].BoundIndex = 0 },
		func(b *guestObservation, _ *cage.GateStatus) { b.IPv6UnscopedAbsent = false },
		func(b *guestObservation, _ *cage.GateStatus) { b.IPv6DefaultRoutes++ },
	} {
		v, a := base, after
		v.IPv6Scoped = append([]macIPv6Probe(nil), base.IPv6Scoped...)
		change(&v, &a)
		if macOSIPv6LinkVerified(v, before, a) {
			t.Fatal("accepted incomplete interface or ingress observations")
		}
	}
}

func TestMacOSStaticExitRequiresThreeMatchingSuccessfulSamples(t *testing.T) {
	cfg := cage.DefaultConfig()
	host := "8.8.8.8"
	ok := networkObservation{Status: 200, IP: host}
	sample := macOSStaticCandidate(cfg, "this-run", host, ok, ok)
	if sample == nil || sample.IP != host || sample.Run != "this-run" || sample.Fingerprint != cfg.Fingerprint() {
		t.Fatal("missing verified candidate")
	}
	for _, bad := range []networkObservation{{Status: 503, IP: host}, {Status: 200, IP: "8.8.4.4"}, {Status: 200}} {
		if macOSStaticCandidate(cfg, "this-run", host, bad, ok) != nil || macOSStaticCandidate(cfg, "this-run", host, ok, bad) != nil {
			t.Fatal("accepted different or incomplete egress samples")
		}
	}
	cfg.ExitPolicy = cage.ExitPolicy{Mode: "static", IP: host}
	if macOSStaticCandidate(cfg, "this-run", host, ok, ok) != nil {
		t.Fatal("offered the existing binding again")
	}
}

func TestMacOSWorkloadStartsNetworkSupervision(t *testing.T) {
	s := testServer(t)
	s.macosGuest = &guestos.MacOSProfile{Instance: "mac", User: "guestdev"}
	checked := make(chan struct{}, 1)
	previous := physicalProxyInterface
	physicalProxyInterface = func(ctx context.Context) (string, error) {
		select {
		case checked <- struct{}{}:
		default:
		}
		return "", errors.New("network disconnected")
	}
	defer func() { s.closeBackground(); physicalProxyInterface = previous }()
	s.startMonitors()
	select {
	case <-checked:
	case <-time.After(7 * time.Second):
		t.Fatal("the running macOS workbench never started its real network monitor")
	}
}
