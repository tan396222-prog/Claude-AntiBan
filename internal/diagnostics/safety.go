package diagnostics

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
)

// Exclude run identities, configuration hashes and future private fields.
type safetyEvidence struct {
	Policy    string `json:"policy"`
	CreatedAt string `json:"created_at"`
	Checks    []struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		State     string `json:"state"`
		Critical  bool   `json:"critical"`
		Evidence  string `json:"evidence"`
		Remedy    string `json:"remedy,omitempty"`
		CheckedAt string `json:"checked_at"`
	} `json:"checks"`
}

func (s *Store) safetySnapshot() ([]byte, string) {
	f, err := privateFile(filepath.Join(filepath.Dir(s.dir), "last-safety-report.json"), os.O_RDONLY)
	if os.IsNotExist(err) {
		return nil, "no saved report"
	}
	if err != nil {
		return nil, "saved report unavailable"
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (128<<10)+1))
	var report safetyEvidence
	if err != nil || len(data) > 128<<10 || json.Unmarshal(data, &report) != nil || report.CreatedAt == "" || len(report.Checks) == 0 {
		return nil, "saved report invalid or oversized"
	}
	report.Policy, report.CreatedAt = Redact(report.Policy), Redact(report.CreatedAt)
	for i := range report.Checks {
		c := &report.Checks[i]
		c.ID, c.Title, c.State = Redact(c.ID), Redact(c.Title), Redact(c.State)
		c.Evidence, c.Remedy, c.CheckedAt = Redact(c.Evidence), Redact(c.Remedy), Redact(c.CheckedAt)
	}
	data, _ = json.MarshalIndent(report, "", "  ")
	return data, "saved report; compare created_at with incident time"
}
