package cage

import (
	"bytes"
	"os"
	"strconv"
	"strings"
)

// Linux start times are clock ticks since boot, so the boot ID keeps a PID
// reused after a reboot from matching a record from the previous boot.
func processStartToken(pid int) string {
	if pid <= 0 {
		return ""
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return ""
	}
	// The command name may contain spaces or parentheses; fields resume after
	// the last ')'. starttime is field 22, the 20th after the name.
	i := bytes.LastIndexByte(stat, ')')
	if i < 0 {
		return ""
	}
	fields := strings.Fields(string(stat[i+1:]))
	if len(fields) < 20 {
		return ""
	}
	return strings.TrimSpace(string(boot)) + ":" + fields[19]
}
