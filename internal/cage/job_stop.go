package cage

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

const JobEnv = "CLAUDE_CAGE_JOB_ID"

var jobIDPattern = regexp.MustCompile(`^[a-f0-9]{12,64}$`)

// RequestJobStop covers detached hostagents which may initialize after their
// launching command was cancelled. The private marker belongs to one job;
// it cannot cancel a subsequent start with a fresh ID.
func RequestJobStop(profile, id string) error {
	if !jobIDPattern.MatchString(id) {
		return errors.New("invalid Cage job identifier")
	}
	dir := filepath.Join(profile, "stopped-jobs")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, id), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if os.IsExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return f.Close()
}

func InstanceJobID(state, name string) string {
	if filepath.Base(name) != name {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(state, "lima", name, "cage-running.json"))
	if err != nil || len(data) > 4096 {
		return ""
	}
	var record runningProfile
	if json.Unmarshal(data, &record) != nil || !jobIDPattern.MatchString(record.JobID) {
		return ""
	}
	return record.JobID
}

// WatchJobStop is installed before starting the VM driver. onStop signals
// this hostagent itself; it never guesses or signals another process's PID.
func WatchJobStop(profile, id string, onStop func()) (func(), error) {
	if id == "" {
		return func() {}, nil
	}
	if !jobIDPattern.MatchString(id) {
		return nil, errors.New("invalid Cage job identifier")
	}
	path := filepath.Join(profile, "stopped-jobs", id)
	stopped := func() bool { _, err := os.Lstat(path); return err == nil || !os.IsNotExist(err) }
	if stopped() {
		return nil, errors.New("Cage startup was cancelled")
	}
	done := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(done) }) }
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if stopped() {
					onStop()
					return
				}
			}
		}
	}()
	return stop, nil
}
