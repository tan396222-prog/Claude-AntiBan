package remotehost

import (
	"os"
	"path/filepath"
	"reflect"
)

// ImportOnce runs under the new desktop profile's controller lock. Old
// profiles remain independently usable. A saved empty list is authoritative.
func (s *Store) ImportOnce(directories ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Lstat(filepath.Join(s.profile, "remote-hosts.json")); !os.IsNotExist(err) {
		return err
	}
	var merged []HostProfile
	seen := make(map[string]HostProfile)
	for _, directory := range directories {
		profiles, err := NewStore(directory).List()
		if err != nil {
			return err
		}
		for _, p := range profiles {
			if old, exists := seen[p.ID]; exists {
				if reflect.DeepEqual(old, p) {
					continue
				}
				// A divergent device binding must be trusted again. Keep both
				// choices; do not silently change the pin of an existing host.
				p.ID = newID()
				p.HostKey = nil
				p.Name = "旧入口 · " + p.Name
				if len(p.Name) > 200 {
					p.Name = old.Name
				}
			}
			if len(merged) >= 100 {
				return failure("profile_limit", "已有主机列表合计超过 100 项，请整理后重试；原配置已保留")
			}
			seen[p.ID] = p
			merged = append(merged, p)
		}
	}
	return s.write(merged)
}
