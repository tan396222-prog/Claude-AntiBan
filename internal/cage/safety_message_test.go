package cage

import (
	"strings"
	"testing"
	"time"
)

func TestSafetyRejectionExplainsFailedCheck(t *testing.T) {
	report := SafetyReport{Policy: SafetyPolicyVersion, ID: "test", RunID: "run", CheckID: "check", Configuration: "config", CreatedAt: time.Now()}
	for _, id := range requiredSafetyChecks {
		report.Checks = append(report.Checks, SafetyCheck{ID: id, Critical: true, State: CheckPassed, Evidence: "verified", CheckedAt: report.CreatedAt})
	}
	report.Checks[4].State = CheckFailed
	report.Checks[4].Title = "浏览器时区与语言"
	report.Checks[4].Evidence = "浏览器语言与设置不一致"
	report.Checks[4].Remedy = "更新环境组件，再重新检测"
	err := report.Authorizes(report.RunID, report.Configuration, time.Now())
	for _, part := range []string{"浏览器时区与语言", "浏览器语言与设置不一致", "更新环境组件", "外网保持关闭"} {
		if err == nil || !strings.Contains(err.Error(), part) {
			t.Fatalf("missing actionable failure %q: %v", part, err)
		}
	}
	report.Checks[4].State = CheckUnknown
	report.Checks[4].Evidence = "未能读取浏览器设置"
	err = report.Authorizes(report.RunID, report.Configuration, time.Now())
	if err == nil || !strings.Contains(err.Error(), "未能确认") {
		t.Fatalf("unknown was presented as a confirmed mismatch: %v", err)
	}
}
