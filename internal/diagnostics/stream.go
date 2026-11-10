package diagnostics

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Stream frames arbitrary stdout/stderr writes before redacting. It never
// writes a partial overlong line, so split credentials cannot leak at a bound.
type Stream struct {
	mu                   sync.Mutex
	store                *Store
	source, level        string
	sink                 func(string)
	line                 []byte
	dropping, privateKey bool
	last                 string
	lastLevel            string
	repeated             uint64
	reported             time.Time
	batch                []Event
	batchBytes           int
}

func (s *Store) Stream(source, level string) *Stream {
	return &Stream{store: s, source: source, level: level}
}
func NewStream(store *Store, source, level string, sink func(string)) *Stream {
	return &Stream{store: store, source: source, level: level, sink: sink}
}

// flush writes at most 64 KiB of redacted messages; no background queue can
// grow while a slow disk is being drained.
func (s *Stream) flush() {
	if s.store != nil {
		_ = s.store.appendEvents(s.batch)
	}
	s.batch = nil
	s.batchBytes = 0
}
func (s *Stream) record(level, message string) {
	message = Redact(message)
	if s.batchBytes+len(message) > 64<<10 {
		s.flush()
	}
	if s.sink != nil {
		s.sink(message + "\n")
	}
	s.batch = append(s.batch, Event{Level: level, Source: Redact(s.source), Message: message})
	s.batchBytes += len(message)
	if s.batchBytes >= 64<<10 || len(s.batch) >= 128 {
		s.flush()
	}
}
func (s *Stream) summary() {
	if s.repeated > 0 {
		s.record(s.lastLevel, fmt.Sprintf("[previous output repeated %d additional times]", s.repeated))
		s.repeated = 0
	}
}
func (s *Stream) output(level, line string) {
	// Compare complete raw lines, so distinct credentials never become identical
	// just because redaction removes their values. Never coalesce progress records.
	if line == s.last && !strings.HasPrefix(line, "CAGE_") {
		s.repeated++
		if time.Since(s.reported) >= time.Second {
			s.summary()
			s.reported = time.Now()
		}
		return
	}
	s.summary()
	s.last = line
	s.lastLevel = level
	s.reported = time.Now()
	s.record(level, line)
}
func (s *Stream) emit() {
	line := string(s.line)
	if bytes.Contains(s.line, []byte("-----BEGIN ")) {
		s.privateKey = true
	}
	if s.privateKey {
		if bytes.Contains(s.line, []byte("-----END ")) {
			s.privateKey = false
		}
	} else if !s.dropping && len(s.line) > 0 {
		s.output(s.outputLevel(line), line)
	}
	if s.dropping {
		s.output("warn", "[oversized output line omitted]")
	}
	s.line = nil
	s.dropping = false
}

var logfmtLevel = regexp.MustCompile(`^time=(?:"[^"]*"|\S+)\s+level=(\w+)(?:\s|$)`)

// These managed producers provide their own severity; stderr is not an error
// classification. Other streams keep the severity chosen by their caller.
func (s *Stream) outputLevel(line string) string {
	switch s.source {
	case "vm-hostagent", "proxy", "proxy-validation":
	default:
		return s.level
	}
	var entry struct {
		Level string `json:"level"`
	}
	_ = json.Unmarshal([]byte(line), &entry)
	if entry.Level == "" {
		if match := logfmtLevel.FindStringSubmatch(line); len(match) == 2 {
			entry.Level = match[1]
		}
	}
	switch entry.Level {
	case "trace", "debug":
		return "debug"
	case "info":
		return "info"
	case "warn", "warning":
		return "warn"
	case "error", "fatal", "panic":
		return "error"
	default:
		return s.level
	}
}
func (s *Stream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range p {
		if b == '\n' || b == '\r' {
			s.emit()
			continue
		}
		if s.dropping {
			continue
		}
		if len(s.line) >= MaxMessage {
			s.line = nil
			s.dropping = true
			continue
		}
		s.line = append(s.line, b)
	}
	s.flush()
	return len(p), nil
}
func (s *Stream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emit()
	s.summary()
	s.flush()
	s.last = ""
	return nil
}
func (s *Store) Capture(source, level string, r io.Reader) error {
	w := s.Stream(source, level)
	_, err := io.Copy(w, r)
	_ = w.Close()
	return err
}
