package workbench

import (
	"bufio"
	"bytes"
	"testing"

	"github.com/google/uuid"
	"github.com/sylvinus/agent-vm/internal/guestos"
)

func TestWindowsPacketReadyRequiresMatchedAuthenticatedStream(t *testing.T) {
	binding := &guestos.WindowsProfile{VMID: uuid.New(), GUIUserSID: "S-1-5-21-1-2-3-1001"}
	valid := `{"network_ready":true,"version":1,"vm_id":"` + binding.VMID.String() + `","gui_user_sid":"` + binding.GUIUserSID + `","session":"fresh-stream","network":"packet","available":true}` + "\n"
	if err := readWindowsPacketReady(bufio.NewReader(bytes.NewBufferString(valid)), binding); err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{`{"version":1}` + "\n", `{"error":"network_failed"}` + "\n", bytes.NewBufferString(valid).String()[:10], string(bytes.Repeat([]byte{'a'}, 5000)) + "\n"} {
		if readWindowsPacketReady(bufio.NewReaderSize(bytes.NewBufferString(line), 4096), binding) == nil {
			t.Fatal("missing or oversized stream readiness accepted")
		}
	}
	wrong := *binding
	wrong.VMID = uuid.New()
	if readWindowsPacketReady(bufio.NewReader(bytes.NewBufferString(valid)), &wrong) == nil {
		t.Fatal("another VM's ready line admitted")
	}
}
