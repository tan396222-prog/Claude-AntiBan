//go:build !windows

package host

func ListenInstanceStop(string, func()) (func(), error) { return func() {}, nil }
func RequestInstanceStop(string) (bool, error)          { return false, nil }
