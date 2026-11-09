package workbench

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sylvinus/agent-vm/internal/cage"
)

func TestNetworkStatusImmediatelyAfterManualGrant(t *testing.T) {
	for _, independentError := range []string{"", "出口位置服务暂时不可用"} {
		t.Run("location-error="+independentError, func(t *testing.T) {
			s, gate := grantedToolUpdateServer(t)
			s.setJobForTest(Job{})
			s.networkError = independentError
			openBeforeFailure := gate.Status()
			client, err := s.gateClient()
			if err != nil {
				t.Fatal(err)
			}
			checkStatus := func(wantMode, wantError string) {
				t.Helper()
				response := httptest.NewRecorder()
				s.status(response, httptest.NewRequest("GET", "/api/status", nil))
				var status struct {
					NetworkError string      `json:"network_error"`
					Safety       safetyState `json:"safety"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
					t.Fatal(err)
				}
				if status.Safety.Gate == nil || status.Safety.Gate.Mode != wantMode || status.NetworkError != wantError {
					t.Fatalf("expected %s and %q, got %+v", wantMode, wantError, status)
				}
				if wantMode == cage.GateOpen && status.Safety.Error != "" {
					t.Fatalf("open gate retained a safety error: %s", status.Safety.Error)
				}
			}

			const issue = "代理出口探测超时"
			s.finishSafetyObservation(t.Context(), client, openBeforeFailure, "", issue, true)
			s.publishGate(openBeforeFailure) // A delayed poll must not clear the current failure.
			checkStatus(cage.GateClosed, issue)

			checking, err := client.BeginCheck(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			s.publishGate(checking)
			checkStatus(cage.GateChecking, issue)
			report := *checking.Report
			report.ID, report.CheckID, report.CreatedAt = "manual-recheck", checking.CheckID, time.Now()
			for i := range report.Checks {
				report.Checks[i].CheckedAt = report.CreatedAt
			}
			opened, err := client.Grant(t.Context(), report)
			if err != nil {
				t.Fatal(err)
			}
			s.publishGate(opened)
			checkStatus(cage.GateOpen, independentError)
		})
	}
}
