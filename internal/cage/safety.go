package cage

import (
	"errors"
	"time"
)

// SafetyPolicyVersion changes when the mandatory evidence contract changes.
// Reports authorize one running VM with one applied configuration.
const SafetyPolicyVersion = "cage-safety/2"
const SafetyReportLifetime = 24 * time.Hour

type CheckState string

const (
	CheckPassed  CheckState = "passed"
	CheckFailed  CheckState = "failed"
	CheckUnknown CheckState = "unknown"
)

type SafetyCheck struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	State     CheckState `json:"state"`
	Critical  bool       `json:"critical"`
	Evidence  string     `json:"evidence"`
	Remedy    string     `json:"remedy,omitempty"`
	CheckedAt time.Time  `json:"checked_at"`
}

type SafetyReport struct {
	Policy        string        `json:"policy"`
	ID            string        `json:"id"`
	RunID         string        `json:"run_id"`
	CheckID       string        `json:"check_id"`
	Configuration string        `json:"configuration"`
	CreatedAt     time.Time     `json:"created_at"`
	Checks        []SafetyCheck `json:"checks"`
}

var requiredSafetyChecks = [...]string{
	"egress_policy", "proxy_transport", "dns_transport", "guest_settings",
	"browser_settings", "filesystem_boundary", "management_boundary",
	"fail_closed", "udp_policy", "ipv6_policy", "exit_consistency",
}

// Authorizes rejects missing, duplicate, unknown, stale or mismatched evidence.
// The required IDs are defined here, never supplied by the frontend or a provider.
func (r SafetyReport) Authorizes(runID, configuration string, now time.Time) error {
	if r.Policy != SafetyPolicyVersion || r.ID == "" || r.CheckID == "" || runID == "" || configuration == "" ||
		r.RunID != runID || r.Configuration != configuration {
		return errors.New("检测报告不属于当前环境配置，请重新检测")
	}
	if r.CreatedAt.IsZero() || r.CreatedAt.After(now.Add(time.Minute)) || !now.Before(r.CreatedAt.Add(SafetyReportLifetime)) {
		return errors.New("检测报告已过期，请重新检测")
	}
	seen := make(map[string]SafetyCheck, len(r.Checks))
	for _, check := range r.Checks {
		if _, duplicate := seen[check.ID]; duplicate {
			return errors.New("检测报告包含重复项目")
		}
		if check.State != CheckPassed && check.State != CheckFailed && check.State != CheckUnknown {
			return errors.New("检测状态无效")
		}
		seen[check.ID] = check
		if check.Critical && check.State != CheckPassed {
			return errors.New("关键检查尚未通过，外网保持关闭")
		}
	}
	for _, id := range requiredSafetyChecks {
		check, found := seen[id]
		if !found || !check.Critical || check.State != CheckPassed || check.Evidence == "" ||
			check.CheckedAt.Before(r.CreatedAt) || check.CheckedAt.After(now.Add(time.Minute)) {
			return errors.New("关键检测证据不完整，外网保持关闭")
		}
	}
	return nil
}
