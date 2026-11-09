package diagnostics

import (
	"bytes"
	"io"
	"sync"
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
}

func (s *Store) Stream(source, level string) *Stream {
	return &Stream{store: s, source: source, level: level}
}
func NewStream(store *Store, source, level string, sink func(string)) *Stream {
	return &Stream{store: store, source: source, level: level, sink: sink}
}
func (s *Stream) record(level, message string) {
	message = Redact(message)
	if s.sink != nil {
		s.sink(message + "\n")
	}
	if s.store != nil {
		_ = s.store.Record(level, s.source, message)
	}
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
		s.record(s.level, line)
	}
	if s.dropping {
		s.record("warn", "[oversized output line omitted]")
	}
	s.line = nil
	s.dropping = false
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
	return len(p), nil
}
func (s *Stream) Close() error { s.mu.Lock(); defer s.mu.Unlock(); s.emit(); return nil }
func (s *Store) Capture(source, level string, r io.Reader) error {
	w := s.Stream(source, level)
	_, err := io.Copy(w, r)
	_ = w.Close()
	return err
}
