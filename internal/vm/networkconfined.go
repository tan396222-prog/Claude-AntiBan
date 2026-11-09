package vm

import (
	"errors"
	"fmt"

	"github.com/lima-vm/lima/v2/pkg/limatype"
)

// ErrNetworkOpen is returned by Start for a VM with a network besides its own
// isolated one: the network policy (netguard) applies to the connections of
// the VM's own netstack only.
var ErrNetworkOpen = errors.New("agent-vm starts no VM with a network besides its own")

// NetworkConfined fails when cfg gives the guest a network of Lima's `networks`
// (a shared usernet, a socket, vzNAT, a vmnet one): its traffic does not go
// through the netstack that the policy is applied to.
func NetworkConfined(cfg *limatype.LimaYAML) error {
	// Only these drivers attach the VM's NIC to our host-side netstack.
	// WSL2 and HCS have separate host-managed networking, even with no
	// explicit networks entry, so they cannot enforce the Cage egress gate.
	if cfg.VMType != nil && *cfg.VMType != limatype.QEMU && *cfg.VMType != limatype.VZ {
		return fmt.Errorf("%w: VM driver %q does not use the confined network", ErrNetworkOpen, *cfg.VMType)
	}
	for i, n := range cfg.Networks {
		what := "a network"
		switch {
		case n.Lima != "":
			what = fmt.Sprintf("the shared network %q", n.Lima)
		case n.Socket != "":
			what = fmt.Sprintf("the socket %q", n.Socket)
		case n.VZNAT != nil && *n.VZNAT:
			what = "vzNAT"
		}
		return fmt.Errorf("%w: networks[%d] is %s", ErrNetworkOpen, i, what)
	}
	return nil
}
