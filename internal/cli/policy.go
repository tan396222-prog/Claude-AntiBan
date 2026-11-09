package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/sylvinus/agent-vm/internal/guard"
	"github.com/sylvinus/agent-vm/internal/netguard"
	"github.com/sylvinus/agent-vm/internal/state"
)

// The network and guarded-files policies are read when a VM boots, by its
// hostagent, and kept until it stops. Starting a VM records what they were
// (PolicyOf); a later start that finds the VM running says so when they
// differ now.

// quiet is AGENT_VM_QUIET=1: notices that are not about a risk to the host
// are not printed.
func quiet() bool { return os.Getenv("AGENT_VM_QUIET") == "1" }

// policyFingerprint identifies the policies as the hostagent would load
// them now: the network file's rules and the guarded paths, comments and
// order not counting, and the two switches in the environment.
func policyFingerprint(st state.Dir) string {
	var parts []string
	if pol, err := netguard.Load(string(st)); err == nil {
		parts = append(parts, fmt.Sprintf("open=%t", pol.Open))
		for _, r := range pol.Allow {
			parts = append(parts, fmt.Sprintf("allow %v %d", r.Net, r.Port))
		}
		for _, d := range pol.Domains {
			parts = append(parts, "domain "+d)
		}
	} else {
		parts = append(parts, "network: "+err.Error())
	}
	if g, err := guard.Load(string(st), ""); err == nil {
		for p := range g.Paths {
			parts = append(parts, "guard "+p)
		}
	} else {
		parts = append(parts, "guarded: "+err.Error())
	}
	parts = append(parts, fmt.Sprintf("deny=%t", os.Getenv("AGENT_VM_GUARDED_WRITES") == "deny"))
	slices.Sort(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])
}

// recordPolicy notes the policies the VM was just booted with.
func (s *start) recordPolicy() {
	if err := s.state.Write(state.PolicyOf+s.name, []byte(policyFingerprint(s.state)+"\n")); err != nil {
		s.warnf("Warning: could not record the network and guarded-files policies VM '%s' booted with: %v", s.name, err)
	}
}

// notePolicyChange says so when the running VM booted with other policies
// than the ones in force now. A VM with no record (booted by an agent-vm
// that kept none) is not said anything about.
func (s *start) notePolicyChange() {
	booted := s.state.ReadMarker(state.PolicyOf, s.name)
	if booted == "" || booted == policyFingerprint(s.state) {
		return
	}
	s.warnf("Note: VM '%s' booted with other network or guarded-files settings than now (%s, %s, AGENT_VM_UNSAFE_OPEN_NETWORK, AGENT_VM_GUARDED_WRITES). The running VM keeps the old ones: 'agent-vm stop', then run again to apply them.",
		s.name, netguard.FileName, guard.FileName)
}

// warnDomains says, once per start, that the network file's 'domain' lines
// are experimental.
func warnDomains(st state.Dir, w io.Writer) {
	if quiet() {
		return
	}
	if pol, err := netguard.Load(string(st)); err == nil && len(pol.Domains) > 0 {
		fmt.Fprintf(w, "Warning: the 'domain' lines in %s are experimental: do not rely on them alone to confine a VM. AGENT_VM_QUIET=1 hides this.\n", st.Path(netguard.FileName))
	}
}
