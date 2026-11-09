package diagnostics

import (
	"io"
	"os"
	"path/filepath"
)

// ImportLegacy consumes only the old Cage-owned logs. Limit reads so an old
// unbounded file cannot exhaust memory; omit a partial first line at the tail.
func (s *Store) ImportLegacy(profile string) error {
	paths := []string{"control.log", filepath.Join("proxy", "runtime.log"), filepath.Join("proxy", "validation.log"), filepath.Join("state", "setup.log")}
	for _, relative := range paths {
		path := filepath.Join(profile, relative)
		st, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !st.Mode().IsRegular() {
			continue
		}
		if s.now().Sub(st.ModTime()) > s.retention {
			if err := os.Remove(path); err != nil {
				return err
			}
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		const limit = 256 << 10
		if st.Size() > limit {
			_, _ = f.Seek(st.Size()-limit, io.SeekStart)
		}
		r := io.LimitReader(f, limit)
		if st.Size() > limit { // Discard the cut line before framing secret values.
			var one [1]byte
			for {
				n, e := r.Read(one[:])
				if n == 0 || e != nil || one[0] == '\n' {
					break
				}
			}
		}
		stream := s.Stream("legacy", "warn")
		_, err = io.Copy(stream, r)
		_ = stream.Close()
		f.Close()
		if err != nil {
			return err
		}
		if err = os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}
