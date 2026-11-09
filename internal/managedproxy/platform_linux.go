package managedproxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sylvinus/agent-vm/internal/diagnostics"
	"github.com/sylvinus/agent-vm/internal/host"
	"golang.org/x/sys/unix"
)

type linuxCoreRecord struct {
	PID        int    `json:"pid"`
	Executable string `json:"executable"`
	Started    uint64 `json:"started"`
	BootID     string `json:"boot_id"`
}

func linuxCoreRecordPath(profile string) string {
	return filepath.Join(Dir(profile), "managed-process.json")
}

func identifyLinuxCore(pid int) (linuxCoreRecord, error) {
	var record linuxCoreRecord
	root := filepath.Join("/proc", strconv.Itoa(pid))
	executable, err := os.Readlink(filepath.Join(root, "exe"))
	if err != nil {
		return record, err
	}
	started, err := linuxProcessStart(pid)
	if err != nil {
		return record, err
	}
	bootID, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return record, err
	}
	return linuxCoreRecord{PID: pid, Executable: executable, Started: started, BootID: strings.TrimSpace(string(bootID))}, nil
}

func linuxProcessStart(pid int) (uint64, error) {
	stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0, err
	}
	// comm is parenthesized and may itself contain spaces and parentheses.
	end := strings.LastIndexByte(string(stat), ')')
	if end < 0 {
		return 0, errors.New("无法读取代理进程启动时间")
	}
	fields := strings.Fields(string(stat)[end+1:])
	if len(fields) < 20 {
		return 0, errors.New("无法读取代理进程启动时间")
	}
	return strconv.ParseUint(fields[19], 10, 64) // field 22: starttime
}

// A pidfd pins the process while its recorded executable and start time are
// checked. A PID recycled between verification and signaling remains harmless.
func stopLinuxProcess(record linuxCoreRecord, core string) error {
	bootID, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(bootID)) != record.BootID {
		return nil // A process from a previous boot cannot still own the proxy.
	}
	resolved, err := filepath.EvalSymlinks(core)
	if err != nil {
		return err
	}
	if resolved != record.Executable {
		return errors.New("Cage 代理进程不属于当前安装目录")
	}
	fd, err := unix.PidfdOpen(record.PID, 0)
	if errors.Is(err, unix.ESRCH) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("无法安全打开 Cage 代理进程（需要 Linux 5.3 或更新内核）: %w", err)
	}
	defer unix.Close(fd)
	// Check start time before exe: a reused PID may belong to another user,
	// whose /proc/PID/exe we cannot read even though starttime is available.
	started, err := linuxProcessStart(record.PID)
	if os.IsNotExist(err) || err == nil && started != record.Started {
		return nil
	}
	if err != nil {
		return err
	}
	current, err := identifyLinuxCore(record.PID)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if current != record {
		// The recorded process has exited and this PID now belongs to someone
		// else. Discard the stale record without signaling the new owner.
		return nil
	}
	for _, signal := range []unix.Signal{unix.SIGTERM, unix.SIGKILL} {
		if err := unix.PidfdSendSignal(fd, signal, nil, 0); errors.Is(err, unix.ESRCH) {
			return nil
		} else if err != nil {
			return err
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
			n, err := unix.Poll(poll, 100)
			if err != nil && !errors.Is(err, unix.EINTR) {
				return err
			}
			if n > 0 && poll[0].Revents&unix.POLLIN != 0 {
				return nil
			}
		}
	}
	return errors.New("Cage 代理进程未能及时停止")
}

func stopLinuxCore(profile string) error {
	path := linuxCoreRecordPath(profile)
	if err := host.CheckPrivateFile(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var record linuxCoreRecord
	if len(b) > 4096 || json.Unmarshal(b, &record) != nil || record.PID <= 1 || record.Started == 0 || record.BootID == "" {
		return errors.New("Cage 代理进程记录无效")
	}
	core, err := CorePath()
	if err != nil {
		return err
	}
	if err := stopLinuxProcess(record, core); err != nil {
		return err
	}
	_ = os.Remove(Socket(profile))
	return os.Remove(path)
}

func startLinuxCore(ctx context.Context, profile string, s Store) error {
	name, err := PhysicalInterface(ctx)
	if err != nil {
		return err
	}
	path := filepath.Join(Dir(profile), "config.yaml")
	bound, _ := BoundInterface(path)
	if bound == name && Running(ctx, profile, s) {
		return nil
	}
	if err := Validate(ctx, profile, s); err != nil {
		return err
	}
	if err := stopLinuxCore(profile); err != nil {
		return err
	}
	l, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", s.Port))
	if err != nil {
		return errors.New("Cage 代理端口已被占用，请更换独立端口")
	}
	l.Close()
	core, err := CorePath()
	if err != nil {
		return err
	}
	if err := host.EnsurePrivateDir(filepath.Dir(Socket(profile))); err != nil {
		return err
	}
	b, err := s.Runtime(profile)
	if err != nil {
		return err
	}
	b, err = BindInterface(b, name)
	if err != nil {
		return err
	}
	if err := Write(path, b); err != nil {
		return err
	}
	_ = os.Remove(Socket(profile))
	log := diagnostics.New(profile).Stream("proxy", "warn")

	cmd := exec.Command(core, "-d", Dir(profile), "-f", path)
	cmd.Env, cmd.Stdout, cmd.Stderr = coreEnvironment(Dir(profile)), log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		_ = log.Close()
		return err
	}
	record, err := identifyLinuxCore(cmd.Process.Pid)
	if err == nil {
		var data []byte
		data, err = json.Marshal(record)
		if err == nil {
			err = Write(linuxCoreRecordPath(profile), data)
		}
	}
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	exited := make(chan struct{})
	go func() {
		err := cmd.Wait()
		_ = log.Close()
		if err != nil {
			_ = diagnostics.New(profile).Record("error", "proxy", "core exited: "+err.Error())
		}
		close(exited)
	}()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(15 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = stopLinuxCore(profile)
			return ctx.Err()
		case <-exited:
			_ = stopLinuxCore(profile)
			return errors.New("代理核心启动失败，请查看私有代理日志")
		case <-timeout.C:
			_ = stopLinuxCore(profile)
			return errors.New("代理核心启动超时，请查看私有代理日志")
		case <-ticker.C:
			if Running(ctx, profile, s) {
				if err := host.ProtectFile(Socket(profile)); err != nil {
					_ = stopLinuxCore(profile)
					return err
				}
				return nil
			}
		}
	}
}
