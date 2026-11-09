// Package version provides the application version and version comparisons.
package version

import (
	agentvm "github.com/sylvinus/agent-vm"
	"strings"
)

// Version is set at build time (-ldflags -X); integrators gate on it with
// `agent-vm version --min`.
var Version string

func init() {
	if Version == "" {
		Version = strings.TrimSpace(agentvm.CageVersion)
	}
}

// AtLeast reports whether version have is want or later. The numbers before
// any "-" compare first, anything but digits in a component dropped, a
// missing component being 0 (1.2 is 1.2.0). With equal numbers, a version
// with a suffix after "-" is a prerelease and is older than the same
// without: 0.3.0-dev, as 0.3.0-dev.<commit> from a development build, does
// not satisfy 0.3.0. Two prereleases compare identifier by identifier, as
// semver does.
func AtLeast(have, want string) bool {
	a, apre, _ := strings.Cut(have, "-")
	b, bpre, _ := strings.Cut(want, "-")
	for a != "" || b != "" {
		var x, y string
		x, a, _ = strings.Cut(a, ".")
		y, b, _ = strings.Cut(b, ".")
		x, y = number(x), number(y)
		if len(x) != len(y) {
			return len(x) > len(y)
		}
		if x != y {
			return x > y
		}
	}
	switch {
	case bpre == "":
		return apre == ""
	case apre == "":
		return true
	}
	return comparePre(apre, bpre) >= 0
}

// comparePre orders two prerelease strings: dot-separated identifiers, the
// numeric ones as numbers and lower than the others, which compare as text;
// a longer list is higher when the shorter is its start.
func comparePre(a, b string) int {
	for a != "" || b != "" {
		if a == "" {
			return -1
		}
		if b == "" {
			return 1
		}
		var x, y string
		x, a, _ = strings.Cut(a, ".")
		y, b, _ = strings.Cut(b, ".")
		xn, yn := isNumeric(x), isNumeric(y)
		switch {
		case xn && yn:
			x, y = strings.TrimLeft(x, "0"), strings.TrimLeft(y, "0")
			if len(x) != len(y) {
				return len(x) - len(y)
			}
		case xn:
			return -1
		case yn:
			return 1
		}
		if c := strings.Compare(x, y); c != 0 {
			return c
		}
	}
	return 0
}

func isNumeric(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}

// number is the digits of s, without leading zeros: compared by length,
// then as text, it compares as a number of any size.
func number(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return strings.TrimLeft(b.String(), "0")
}
