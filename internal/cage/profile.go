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
	Fingerprint string `json:"fingerprint"`
	JobID       string `json:"job_id,omitempty"`
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
	b, _ := json.Marshal(runningProfile{PID: os.Getpid(), Fingerprint: c.Fingerprint(), JobID: os.Getenv(JobEnv)})
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
		if r.PID < 1 {
			continue
		}
		alive := profileProcessAlive(r.PID)
		if alive && r.Fingerprint != c.Fingerprint() {
			return fmt.Errorf("cage configuration changed while a VM is running; run cage stop before restarting")
		}
	}
	return nil
}
