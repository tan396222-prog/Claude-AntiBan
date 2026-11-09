//go:build !darwin && !linux && !windows

package host

func MemoryPressure() bool { return false }
