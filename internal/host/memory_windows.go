package host

import "github.com/pbnjay/memory"

func MemoryPressure() bool {
	total := memory.TotalMemory()
	return total > 0 && memory.FreeMemory() < total/10
}
