//go:build darwin && arm64 && cgo && !no_vz

package maclogin

import (
	"bufio"
	"bytes"
	"encoding/json"
	"testing"
)

type passwordProbeInput struct{ bytes.Buffer }

func (*passwordProbeInput) Close() error { return nil }

func TestMaintenancePasswordProbeUsesFixedPrivateStreamRequest(t *testing.T) {
	target := passwordTarget{maintenanceWindow: maintenanceFixture(), SelectionRequired: true}
	first, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	target.Window++
	second, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	input := &passwordProbeInput{}
	output := append(append(append(first, '\n'), second...), '\n')
	probe := passwordProbe{input: input, scanner: bufio.NewScanner(bytes.NewReader(output)), user: "trial"}
	before, err := probe.target()
	if err != nil || before.Accessibility || !before.SelectionRequired {
		t.Fatal("private probe lost the AX-disabled authorization target")
	}
	after, err := probe.target()
	if err != nil || before.geometry() == after.geometry() {
		t.Fatal("per-key probe failed to observe a changed authorization panel")
	}
	if input.String() != "{\"action\":\"probe\"}\n{\"action\":\"probe\"}\n" {
		t.Fatal("maintenance probe used a GUI socket command or extra request fields")
	}
}
