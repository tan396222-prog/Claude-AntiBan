package cage

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func processStartToken(pid int) string {
	if pid <= 0 {
		return ""
	}
	p, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || p.Proc.P_pid != int32(pid) {
		return ""
	}
	t := p.Proc.P_starttime
	if t.Sec == 0 && t.Usec == 0 {
		return ""
	}
	return fmt.Sprintf("%d.%06d", t.Sec, t.Usec)
}
