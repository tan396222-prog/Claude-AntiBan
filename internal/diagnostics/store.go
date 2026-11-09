package diagnostics

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sylvinus/agent-vm/internal/host"
)

const (
	RetentionDays       = 14
	MaxBytes      int64 = 40 << 20
	FileBytes     int64 = 5 << 20
	MaxMessage          = 24 << 10
)

type Store struct {
	dir                 string
	maxBytes, fileBytes int64
	retention           time.Duration
	now                 func() time.Time
}

type Event struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Source  string `json:"source"`
	Message string `json:"message"`
	PID     int    `json:"pid"`
}

type Info struct {
	RetentionDays int   `json:"retention_days"`
	MaxBytes      int64 `json:"max_bytes"`
	GuestMaxBytes int64 `json:"guest_max_bytes"`
	Bytes         int64 `json:"bytes"`
	Files         int   `json:"files"`
}

func New(profile string) *Store {
	return &Store{dir: filepath.Join(profile, "logs"), maxBytes: MaxBytes, fileBytes: FileBytes, retention: RetentionDays * 24 * time.Hour, now: time.Now}
}

// A process mutex also serializes callers on platforms whose file locks are
// process scoped. The OS lock coordinates the controller and native helpers.
var storeMu sync.Mutex

func (s *Store) withLock(fn func() error) error {
	storeMu.Lock()
	defer storeMu.Unlock()
	if err := host.EnsurePrivateDir(s.dir); err != nil {
		return err
	}
	path := filepath.Join(s.dir, ".lock")
	f, err := privateFile(path, os.O_CREATE|os.O_RDWR)
	if err != nil {
		return err
	}
	defer f.Close()
	if err = lock(f); err != nil {
		return err
	}
	defer unlock(f)
	return fn()
}
func privateFile(path string, flags int) (*os.File, error) {
	if st, err := os.Lstat(path); err == nil {
		if !st.Mode().IsRegular() {
			return nil, errors.New("diagnostic file is not regular")
		}
		if err = host.CheckPrivateFile(path); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	f, err := os.OpenFile(path, flags, 0600)
	if err == nil {
		err = host.ProtectFile(path)
		if err != nil {
			f.Close()
			return nil, err
		}
	}
	return f, err
}
func owned(name string) bool {
	return name == "events.jsonl" || strings.HasPrefix(name, "events-") && strings.HasSuffix(name, ".jsonl")
}
func (s *Store) files() ([]os.FileInfo, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var files []os.FileInfo
	for _, entry := range entries {
		if !owned(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		if info.Mode().IsRegular() {
			files = append(files, info)
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].ModTime().Before(files[j].ModTime()) })
	return files, nil
}
func (s *Store) cleanLocked(reserve int64) error {
	files, err := s.files()
	if err != nil {
		return err
	}
	var size int64
	var live []os.FileInfo
	for _, f := range files {
		born := f.ModTime()
		if first, err := privateFile(filepath.Join(s.dir, f.Name()), os.O_RDONLY); err == nil {
			line, _ := bufio.NewReader(io.LimitReader(first, MaxMessage*8)).ReadBytes('\n')
			first.Close()
			var event Event
			if json.Unmarshal(line, &event) == nil {
				if stamp, err := time.Parse(time.RFC3339Nano, event.Time); err == nil && stamp.Before(born) {
					born = stamp
				}
			}
		}
		if s.now().Sub(born) > s.retention {
			if err := os.Remove(filepath.Join(s.dir, f.Name())); err != nil {
				return err
			}
		} else {
			size += f.Size()
			live = append(live, f)
		}
	}
	for _, f := range live {
		if size+reserve <= s.maxBytes {
			break
		}
		if err := os.Remove(filepath.Join(s.dir, f.Name())); err != nil {
			return err
		}
		size -= f.Size()
	}
	return nil
}
func (s *Store) Clean() error { return s.withLock(func() error { return s.cleanLocked(0) }) }
func (s *Store) Info() (info Info, err error) {
	info = Info{RetentionDays: RetentionDays, MaxBytes: s.maxBytes, GuestMaxBytes: 10 << 20}
	err = s.withLock(func() error {
		if err := s.cleanLocked(0); err != nil {
			return err
		}
		files, err := s.files()
		if err != nil {
			return err
		}
		for _, f := range files {
			info.Bytes += f.Size()
			info.Files++
		}
		return nil
	})
	return
}
func (s *Store) Record(level, source, message string) error {
	switch level {
	case "debug", "info", "warn", "error":
	default:
		level = "error"
	}
	// Redact before bounding: cutting a URL or key before redaction can expose a
	// prefix. Oversized lines are discarded in Stream, not split into fragments.
	message = Redact(message)
	if len(message) > MaxMessage {
		message = message[:MaxMessage] + " [truncated]"
	}
	source = Redact(source)
	if len(source) > 64 {
		source = "unknown"
	}
	b, err := json.Marshal(Event{Time: s.now().UTC().Format(time.RFC3339Nano), Level: level, Source: source, Message: message, PID: os.Getpid()})
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return s.withLock(func() error {
		if err := s.cleanLocked(int64(len(b))); err != nil {
			return err
		}
		path := filepath.Join(s.dir, "events.jsonl")
		if st, err := os.Lstat(path); err == nil && (st.Size()+int64(len(b)) > s.fileBytes || st.ModTime().UTC().Format("2006-01-02") != s.now().UTC().Format("2006-01-02")) {
			if !st.Mode().IsRegular() {
				return errors.New("diagnostic file is not regular")
			}
			next := filepath.Join(s.dir, fmt.Sprintf("events-%d-%d.jsonl", s.now().UnixNano(), os.Getpid()))
			if err := os.Rename(path, next); err != nil {
				return err
			}
		}
		f, err := privateFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND)
		if err != nil {
			return err
		}
		_, err = f.Write(b)
		return errors.Join(err, f.Close())
	})
}

var defaultMu sync.RWMutex
var defaultStore *Store

func SetDefault(s *Store) { defaultMu.Lock(); defaultStore = s; defaultMu.Unlock() }
func Default() *Store     { defaultMu.RLock(); defer defaultMu.RUnlock(); return defaultStore }
func Log(level, source, message string) {
	if s := Default(); s != nil {
		_ = s.Record(level, source, message)
	}
}
