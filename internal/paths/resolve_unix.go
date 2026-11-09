//go:build !windows

package paths

import "path/filepath"

// Resolve returns the physical path using the platform's native separators.
func Resolve(path string) (string, error) { return filepath.EvalSymlinks(path) }
