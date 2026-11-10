package cage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type runningProfile struct {
	PID         int    `json:"pid"`
	Started     string `json:"started,omitempty"`
	Fingerprint string `json:"fingerprint"`
	JobID       string `json:"job_id,omitempty"`
}

// ProcessStartMatches reports false only when pid now belongs to a process
// started at a different time than the record. After a reboot or crash the
// recorded PID can be reused by an unrelated process. Records from older
// versions carry no start time and an unreadable start time cannot prove
// reuse; both keep the previous PID-only behavior.
func ProcessStartMatches(pid int, started string) bool {
	if started == "" {
		return true
	}
	current := processStartToken(pid)
	return current == "" || current == started
}

func (r runningProfile) alive() bool {
	return r.PID > 0 && profileProcessAlive(r.PID) && ProcessStartMatches(r.PID, r.Started)
}

func (c Config) Fingerprint() string {
	c.ExitPolicy = c.ExitPolicy.Normalized()
	b, _ := json.Marshal(c)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (c Config) RecordRunning(state, name string) error {
	if filepath.Base(name) != name {
		return errors.New("invalid cage instance name")
	}
	dir := filepath.Join(state, "lima", name)
	return c.RecordRunningAt(dir)
}

// RecordRunningAt records the network owner's identity in the runtime's real
// instance directory; container environments do not create Lima metadata.
func (c Config) RecordRunningAt(dir string) error {
	pid := os.Getpid()
	b, _ := json.Marshal(runningProfile{PID: pid, Started: processStartToken(pid), Fingerprint: c.Fingerprint(), JobID: os.Getenv(JobEnv)})
	f, err := os.CreateTemp(dir, ".running-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, "cage-running.json"))
}

// CheckRunning prevents a profile edit from silently leaving a running VM on
// its previous proxy or locale. Stopping and listing remain possible.
func (c Config) CheckRunning(state string) error {
	files, err := filepath.Glob(filepath.Join(state, "lima", "*", "cage-running.json"))
	if err != nil {
		return err
	}
	containerRecords, err := filepath.Glob(filepath.Join(state, "container", "*", "cage-running.json"))
	if err != nil {
		return err
	}
	files = append(files, containerRecords...)
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		var r runningProfile
		if json.Unmarshal(b, &r) != nil {
			return errors.New("invalid running cage profile record")
		}
		if r.alive() && r.Fingerprint != c.Fingerprint() {
			return fmt.Errorf("cage configuration changed while a VM is running; run cage stop before restarting")
		}
	}
	return nil
}
