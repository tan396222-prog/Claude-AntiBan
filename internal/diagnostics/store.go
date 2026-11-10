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
	return s.appendEvents([]Event{{Level: level, Source: source, Message: message}})
}

// appendEvents amortizes filesystem and permission checks across one bounded
// stream chunk. Rotation still applies between records, under the process lock.
func (s *Store) appendEvents(events []Event) error {
	if len(events) == 0 {
		return nil
	}
	records := make([][]byte, 0, len(events))
	var size int64
	for _, event := range events {
		switch event.Level {
		case "debug", "info", "warn", "error":
		default:
			event.Level = "error"
		}
		// Redact before bounding; partial secrets must not reach disk.
		event.Message = Redact(event.Message)
		if len(event.Message) > MaxMessage {
			event.Message = event.Message[:MaxMessage] + " [truncated]"
		}
		event.Source = Redact(event.Source)
		if len(event.Source) > 64 {
			event.Source = "unknown"
		}
		event.Time = s.now().UTC().Format(time.RFC3339Nano)
		event.PID = os.Getpid()
		b, err := json.Marshal(event)
		if err != nil {
			return err
		}
		b = append(b, '\n')
		records = append(records, b)
		size += int64(len(b))
	}
	return s.withLock(func() error {
		// Oversized batches are split by Stream; cap reserve for small test stores.
		reserve := min(size, s.fileBytes, s.maxBytes)
		if err := s.cleanLocked(reserve); err != nil {
			return err
		}
		path := filepath.Join(s.dir, "events.jsonl")
		var f *os.File
		defer func() {
			if f != nil {
				_ = f.Close()
			}
		}()
		var buffered []byte
		flush := func() error {
			if len(buffered) == 0 {
				return nil
			}
			if f == nil {
				var err error
				f, err = privateFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND)
				if err != nil {
					return err
				}
			}
			_, err := f.Write(buffered)
			buffered = buffered[:0]
			return err
		}
		var activeSize int64
		if st, err := os.Lstat(path); err == nil {
			if !st.Mode().IsRegular() {
				return errors.New("diagnostic file is not regular")
			}
			activeSize = st.Size()
			if st.ModTime().UTC().Format("2006-01-02") != s.now().UTC().Format("2006-01-02") {
				activeSize = s.fileBytes // Rotate before the first new record.
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		for _, b := range records {
			if activeSize > 0 && activeSize+int64(len(b)) > s.fileBytes {
				if err := flush(); err != nil {
					return err
				}
				if f != nil {
					if err := f.Close(); err != nil {
						return err
					}
					f = nil
				}
				next := filepath.Join(s.dir, fmt.Sprintf("events-%d-%d.jsonl", s.now().UnixNano(), os.Getpid()))
				if err := os.Rename(path, next); err != nil {
					return err
				}
				if err := s.cleanLocked(min(size, s.fileBytes, s.maxBytes)); err != nil {
					return err
				}
				activeSize = 0
			}
			buffered = append(buffered, b...)
			activeSize += int64(len(b))
			size -= int64(len(b))
		}
		err := flush()
		if f != nil {
			err = errors.Join(err, f.Close())
			f = nil
		}
		return err
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
