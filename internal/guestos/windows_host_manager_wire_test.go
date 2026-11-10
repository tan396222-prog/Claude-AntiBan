package guestos

import (
	"testing"

	"github.com/google/uuid"
)

func TestWindowsHostManagerRequiresTwoCPUs(t *testing.T) {
	request := windowsHostManagerRequest{Version: 1, VMID: uuid.New(), Operation: "resources", MemoryGiB: 4, CPUs: 1}
	if request.validate() == nil {
		t.Fatal("privileged Windows resource request accepted one CPU below the supported guest minimum")
	}
	request.CPUs = 2
	if err := request.validate(); err != nil {
		t.Fatal("supported Windows CPU minimum rejected", err)
	}
	request.MemoryGiB, request.CPUs = 0, 0
	if err := request.validate(); err != nil {
		t.Fatal("read-only resource request rejected", err)
	}
}
