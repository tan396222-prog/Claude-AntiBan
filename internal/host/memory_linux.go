package host

import (
	"os"
	"strconv"
	"strings"
)

func MemoryPressure() bool {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return false
	}
	var total, available uint64
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		v, _ := strconv.ParseUint(f[1], 10, 64)
		if f[0] == "MemTotal:" {
			total = v
		}
		if f[0] == "MemAvailable:" {
			available = v
		}
	}
	return total > 0 && available < total/10
}
