package diagnostics

import (
	"archive/zip"
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// Export accepts only an explicit metadata map and fixed-name supplements. It
// never traverses the profile or includes configuration, projects or credentials.
func (s *Store) Export(w io.Writer, version string, guest []byte, guestState string) error {
	return s.withLock(func() error {
		if err := s.cleanLocked(0); err != nil {
			return err
		}
		archive := zip.NewWriter(w)
		add := func(name string, b []byte) error {
			f, err := archive.Create(name)
			if err != nil {
				return err
			}
			_, err = f.Write(b)
			return err
		}
		meta, _ := json.MarshalIndent(map[string]any{"format": 1, "created_at": s.now().UTC().Format(time.RFC3339), "app_version": version, "os": runtime.GOOS, "arch": runtime.GOARCH, "go_version": runtime.Version(), "retention_days": RetentionDays, "host_max_bytes": MaxBytes, "guest_max_bytes": 10 << 20, "guest_collection": Redact(guestState)}, "", "  ")
		if err := add("manifest.json", meta); err != nil {
			return err
		}
		if err := add("README.txt", []byte("Cage diagnostic bundle. Logs are redacted automatically. Review before sharing. No project files, terminal sessions, configuration or browser history are collected. Guest collection is best effort; see manifest.json.\n")); err != nil {
			return err
		}
		files, err := s.files()
		if err != nil {
			return err
		}
		for _, file := range files {
			path := filepath.Join(s.dir, file.Name())
			f, err := privateFile(path, os.O_RDONLY)
			if err != nil {
				return err
			}
			target, err := archive.Create("logs/" + file.Name())
			if err != nil {
				f.Close()
				return err
			}
			scan := bufio.NewScanner(io.LimitReader(f, s.fileBytes+MaxMessage*8))
			scan.Buffer(make([]byte, 4096), MaxMessage*8)
			for scan.Scan() {
				var event Event
				if json.Unmarshal(scan.Bytes(), &event) != nil {
					continue
				}
				event.Message = Redact(event.Message)
				event.Source = Redact(event.Source)
				b, _ := json.Marshal(event)
				if _, err = target.Write(append(b, '\n')); err != nil {
					f.Close()
					return err
				}
			}
			scanErr := scan.Err()
			f.Close()
			if scanErr != nil {
				return scanErr
			}
		}
		if len(guest) > 0 {
			if err := add("guest/services.log", []byte(Redact(string(guest)))); err != nil {
				return err
			}
		}
		return archive.Close()
	})
}
