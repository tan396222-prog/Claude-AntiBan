package host

import "golang.org/x/sys/unix"

// Free pages alone exclude reclaimable caches; use the kernel pressure signal.
func MemoryPressure() bool {
	level, err := unix.SysctlUint32("kern.memorystatus_vm_pressure_level")
	return err == nil && level >= 4
}
