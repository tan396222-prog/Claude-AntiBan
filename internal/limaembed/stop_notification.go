package limaembed

import (
	"os"
	"path/filepath"

	"github.com/sylvinus/agent-vm/internal/host"
)

// One hostagent runs in this process. Buffering preserves cancellation that
// arrives while the driver and hostagent are still initializing.
var hostagentSignals = make(chan os.Signal, 1)

func notifyHostagentStop() {
	select {
	case hostagentSignals <- os.Interrupt:
	default:
	}
}

func watchHostagentStop(socket string) (func(), error) {
	return host.ListenInstanceStop(filepath.Dir(socket), notifyHostagentStop)
}
