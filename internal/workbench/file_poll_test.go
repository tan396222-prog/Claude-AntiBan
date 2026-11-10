package workbench

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
	"testing"
)

func TestDesktopFileRequestsReportTransportFailureAsUnavailable(t *testing.T) {
	if _, err := exec.LookPath(sshExecutable()); err != nil {
		t.Skip("SSH unavailable")
	}
	s := testServer(t)
	s.ready = true
	// This isolated profile has no SSH config. The shipped SSH entry fails
	// locally, without connecting to any running VM or host service.
	w := request(s, "GET", "/api/input/files/desktop/requests", "", "", true)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("transport failure became bad user input: %d %s", w.Code, w.Body.String())
	}
}

func TestDesktopFilePollDistinguishesUnopenedAndBrokenDesktop(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash unavailable")
	}
	fixture := `systemctl() {
  printf '%s\n' "$DESKTOP_STATE"
  [ "$DESKTOP_STATE" = active ]
}
`
	for _, state := range []string{"inactive", "activating", "active", "failed", ""} {
		t.Run(state, func(t *testing.T) {
			// The final command stands for contacting the broker. It must only
			// run for an active service; a failed service cannot become empty success.
			script := "DESKTOP_STATE='" + state + "'\n" + fixture + desktopFilesPoll + "printf broker-contacted"
			cmd := exec.Command(bash, "--noprofile", "--norc", "-s")
			cmd.Stdin = strings.NewReader(script)
			out, err := cmd.CombinedOutput()
			switch state {
			case "inactive", "activating":
				var result map[string]any
				if err != nil || json.Unmarshal(out, &result) != nil || result["available"] != false {
					t.Fatalf("unopened desktop became error: %s %v", out, err)
				}
			case "active":
				if err != nil || string(out) != "broker-contacted" {
					t.Fatalf("live broker not polled: %s %v", out, err)
				}
			default:
				if err == nil || strings.Contains(string(out), "broker-contacted") {
					t.Fatalf("broken service hidden: %s %v", out, err)
				}
			}
		})
	}
}
