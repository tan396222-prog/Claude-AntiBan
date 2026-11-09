package workbench

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type legacyProcessIdentity struct {
	PID                 int
	Executable, Started string
}

// Legacy controllers have no atomic handoff endpoint. This compatibility path
// runs only after an explicit stop-and-update request, never during startup.
// The private PID record alone is not authority to signal a process.
func identifyLegacyController(profile string, record controlFile) (legacyProcessIdentity, error) {
	refused := errors.New("无法确认旧后台进程身份，未停止任何进程。请保留现有环境并重试更新。")
	if runtime.GOOS != "darwin" || record.PID <= 1 || record.PID == os.Getpid() {
		return legacyProcessIdentity{}, refused
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	read := func(binary string, args ...string) (string, error) {
		b, err := exec.CommandContext(ctx, binary, args...).Output()
		return strings.TrimSpace(string(b)), err
	}
	pid := strconv.Itoa(record.PID)
	uid, err := read("/bin/ps", "-p", pid, "-o", "uid=")
	if err != nil || uid != strconv.Itoa(os.Getuid()) {
		return legacyProcessIdentity{}, refused
	}
	started, err := read("/bin/ps", "-p", pid, "-o", "lstart=")
	if err != nil || started == "" {
		return legacyProcessIdentity{}, refused
	}
	executable, err := read("/bin/ps", "-p", pid, "-o", "comm=")
	if err != nil || !filepath.IsAbs(executable) || filepath.Base(executable) != "cage" {
		return legacyProcessIdentity{}, refused
	}
	command, err := read("/bin/ps", "-ww", "-p", pid, "-o", "command=")
	if err != nil || command != executable+" serve" {
		return legacyProcessIdentity{}, refused
	}
	cwd, err := read("/usr/sbin/lsof", "-a", "-p", pid, "-d", "cwd", "-Fn")
	realProfile, pathErr := filepath.EvalSymlinks(profile)
	if err != nil || pathErr != nil || !strings.Contains("\n"+cwd+"\n", "\nn"+realProfile+"\n") {
		return legacyProcessIdentity{}, refused
	}
	listener, err := read("/usr/sbin/lsof", "-nP", "-a", "-p", pid, fmt.Sprintf("-iTCP:%d", record.Port), "-sTCP:LISTEN", "-Fn")
	if err != nil || !strings.Contains("\n"+listener+"\n", fmt.Sprintf("\nn127.0.0.1:%d\n", record.Port)) {
		return legacyProcessIdentity{}, refused
	}
	finalStart, err := read("/bin/ps", "-p", pid, "-o", "lstart=")
	if err != nil || finalStart != started {
		return legacyProcessIdentity{}, refused
	}
	return legacyProcessIdentity{PID: record.PID, Executable: executable, Started: started}, nil
}
