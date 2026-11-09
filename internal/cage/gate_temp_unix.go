//go:build !windows

package cage

// macOS's user temp directory often exceeds AF_UNIX's path limit.
func gateTempDir() string { return "/tmp" }
