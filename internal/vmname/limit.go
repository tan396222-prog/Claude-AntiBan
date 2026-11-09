package vmname

import (
	"path/filepath"

	"github.com/lima-vm/lima/v2/pkg/limatype/filenames"
	"github.com/lima-vm/lima/v2/pkg/osutil"
)

// HomeLimit fits the longest Lima socket in this profile's UNIX socket path.
// A Cage-managed storage link keeps the original logical home after migration.
func HomeLimit(home string, logical bool) Limit {
	real, rest := home, ""
	for !logical {
		if p, err := filepath.EvalSymlinks(real); err == nil {
			real = filepath.Join(p, rest)
			break
		}
		parent := filepath.Dir(real)
		if parent == real {
			real = home
			break
		}
		rest = filepath.Join(filepath.Base(real), rest)
		real = parent
	}
	// <home>/<name>/<socket>, shorter than UnixPathMax (its NUL).
	n := osutil.UnixPathMax - 1 - len(real) - len("/") - len("/"+filenames.LongestSock)
	return Limit(max(1, min(n, 76)))
}
