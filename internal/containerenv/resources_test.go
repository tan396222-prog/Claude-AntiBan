package containerenv

import (
	"strings"
	"testing"
)

func TestKernelResourceEvidenceRejectsIgnoredOrDifferentLimits(t *testing.T) {
	valid := "4294967296\n200000 100000\n1024\n698 756 0:25 / /sys/fs/cgroup rw,relatime - cgroup2 none rw\n"
	if evidence, err := parseResourceEvidence(valid, fixtureConfig()); err != nil || evidence.CPUs != 2 || evidence.MemoryBytes != 4<<30 || evidence.PIDs != 1024 {
		t.Fatalf("valid cgroup evidence: %+v %v", evidence, err)
	}
	for _, broken := range []string{
		strings.Replace(valid, "4294967296", "max", 1),
		strings.Replace(valid, "200000 100000", "max 100000", 1),
		strings.Replace(valid, "1024", "max", 1),
		strings.Replace(valid, "1024", "512", 1),
		strings.Replace(valid, "200000 100000", "400000 100000", 1),
		strings.Replace(valid, "cgroup2", "tmpfs", 1),
		strings.Replace(valid, "/sys/fs/cgroup", "/somewhere", 1),
	} {
		if _, err := parseResourceEvidence(broken, fixtureConfig()); err == nil {
			t.Fatalf("accepted non-enforced resource evidence %q", broken)
		}
	}
}
