package cage

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sylvinus/agent-vm/internal/host"
)

func TestResourceLimitsUseExistingHostShare(t *testing.T) {
	oldMem, oldCPU := host.MemGiB, host.CPUs
	t.Cleanup(func() { host.MemGiB, host.CPUs = oldMem, oldCPU })
	for _, tc := range []struct{ memory, cpu, wantMemory, wantCPU int }{{24, 10, 12, 5}, {8, 4, 4, 2}, {4, 2, 2, 1}, {0, 0, 4, 2}} {
		host.MemGiB = func() int { return tc.memory }
		host.CPUs = func() int { return tc.cpu }
		t.Setenv("AGENT_VM_HOST_SHARE", "2")
		memory, cpus := ResourceLimits()
		if memory != tc.wantMemory || cpus != tc.wantCPU {
			t.Fatal(tc, memory, cpus)
		}
	}
}

func TestMemoryPolicyBurstIdleAndHostPressure(t *testing.T) {
	now := time.Now()
	gib := uint64(1 << 30)
	p := memoryPolicy{maximum: 12 * gib, target: 4 * gib}
	busy := ResourceSample{Total: 4*gib - 128<<20, Available: 128 << 20}
	if got := p.next(now, busy, false); got != 12*gib {
		t.Fatalf("burst gets %d", got)
	}
	if got := p.next(now, busy, true); got != 4*gib {
		t.Fatalf("host pressure grows to %d", got)
	}
	p.target = p.maximum
	idle := ResourceSample{Total: 12*gib - 128<<20, Available: 10 * gib}
	if got := p.next(now, idle, false); got != p.maximum {
		t.Fatal("immediate shrink", got)
	}
	if got := p.next(now.Add(29*time.Second), idle, false); got != p.maximum {
		t.Fatal("premature shrink", got)
	}
	next := p.next(now.Add(31*time.Second), idle, false)
	if next != p.maximum-256<<20 {
		t.Fatalf("reclamation step %d", next)
	}
	p.target = next
	if got := p.next(now.Add(33*time.Second), idle, false); got != next {
		t.Fatal("repeated shrink before guest acknowledgement", got)
	}
	idle.Total -= 256 << 20
	idle.Available -= 256 << 20
	idle.Balloon = 256 << 20
	if got := p.next(now.Add(35*time.Second), idle, false); got >= next {
		t.Fatal("no reclamation after acknowledgement", got)
	}
	// Repeated active workloads must reset the idle interval.
	p.next(now.Add(36*time.Second), busy, false)
	if got := p.next(now.Add(37*time.Second), idle, false); got != p.target {
		t.Fatal("shrunk after burst", got)
	}
}

func TestResourcesRejectIncompleteOrImpossibleObservation(t *testing.T) {
	valid := "MemTotal: 4000000\nMemAvailable: 2000000\nSwapTotal: 1000\nSwapFree: 500\noom_kill 3\nsome avg10=0.50 avg60=0.0 total=2\ncpu 10 0 5 20 2 0 0 0 0 0\nboot 12345678-1234-1234-1234-123456789012\n"
	s, err := parseResources([]byte(valid))
	if err != nil || s.Available != 2000000<<10 || s.OOMKills != 3 || s.Stall != .5 {
		t.Fatal(s, err)
	}
	balloon, err := parseResources([]byte(valid + "balloon_inflate 131072\nballoon_deflate 65536\npage_size 4096\n"))
	if err != nil || balloon.Balloon != 256<<20 {
		t.Fatal("balloon acknowledgement lost", balloon, err)
	}
	for _, invalid := range []string{strings.Replace(valid, "MemAvailable: 2000000", "MemAvailable: 5000000", 1), strings.Replace(valid, "MemAvailable: 2000000\n", "", 1), strings.Replace(valid, "oom_kill 3\n", "", 1), strings.Replace(valid, "SwapFree: 500", "SwapFree: 1001", 1)} {
		if _, err := parseResources([]byte(invalid)); err == nil {
			t.Fatal("accepted invalid sample")
		}
	}
}

func TestResourceManagerCancellationAndFreshness(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stop := StartResourceManager(ctx, t.TempDir(), 4<<30, 2, func(context.Context, string) ([]byte, error) {
		t.Error("read after cancellation")
		return nil, fmt.Errorf("unused")
	}, func(context.Context, uint64) error { t.Error("change after cancellation"); return nil })
	done := make(chan struct{})
	go func() { stop(); stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stop did not join")
	}
	dir := t.TempDir()
	for _, age := range []time.Duration{0, 11 * time.Second} {
		if err := writeResourceStatus(dir, ResourceStatus{Version: 1, UpdatedAt: time.Now().Add(-age)}); err != nil {
			t.Fatal(err)
		}
		_, err := ReadResourceStatus(dir)
		if (err != nil) != (age > 0) {
			t.Fatal(age, err)
		}
	}
}
