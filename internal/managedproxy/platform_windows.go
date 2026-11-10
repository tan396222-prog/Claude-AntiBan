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
	"strings"
	"syscall"
	"time"

	"github.com/sylvinus/agent-vm/internal/diagnostics"
	"github.com/sylvinus/agent-vm/internal/host"
	"golang.org/x/sys/windows"
)

type windowsCoreRecord struct {
	PID        uint32 `json:"pid"`
	Executable string `json:"executable"`
	Created    int64  `json:"created"`
}

func windowsCoreRecordPath(profile string) string {
	return filepath.Join(Dir(profile), "managed-process.json")
}

func coreEnvironment(dir string) []string {
	env := []string{"HOME=" + dir, "USERPROFILE=" + dir}
	for _, key := range []string{"SystemRoot", "WINDIR", "TEMP", "TMP"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	return env
}

func identifyCore(handle windows.Handle) (string, int64, error) {
	name := make([]uint16, 32768)
	size := uint32(len(name))
	if err := windows.QueryFullProcessImageName(handle, 0, &name[0], &size); err != nil {
		return "", 0, err
	}
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &created, &exited, &kernel, &user); err != nil {
		return "", 0, err
	}
	return windows.UTF16ToString(name[:size]), created.Nanoseconds(), nil
}

// Stop opens one process handle and validates both its image and creation time
// before termination. A reused PID cannot terminate an unrelated application.
func stopWindowsCore(profile string) error {
	path := windowsCoreRecordPath(profile)
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil
	}
	if err := host.CheckPrivateFile(path); err != nil {
		return err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var record windowsCoreRecord
	if len(b) > 4096 || json.Unmarshal(b, &record) != nil || record.PID == 0 || record.Created == 0 {
		return errors.New("Cage 代理进程记录无效")
	}
	core, err := CorePath()
	if err != nil {
		return err
	}
	if !strings.EqualFold(filepath.Clean(core), filepath.Clean(record.Executable)) {
		return errors.New("Cage 代理进程不属于当前安装目录")
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, record.PID)
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return os.Remove(path)
	}
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) && !recordedCoreRunning(record) {
		// The core runs as the current user, so a PID that cannot be
		// terminated now belongs to another account's process.
		return removeStaleCore(profile, path)
	}
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	image, created, err := identifyCore(handle)
	if err != nil {
		return err
	}
	if !sameCore(record, image, created) {
		// The recorded core exited and its PID was reused, typically after a
		// reboot. Forget the stale record and leave the other process alone.
		return removeStaleCore(profile, path)
	}
	var code uint32
	if err := windows.GetExitCodeProcess(handle, &code); err != nil {
		return err
	}
	if code == 259 {
		if err := windows.TerminateProcess(handle, 0); err != nil {
			return err
		}
		state, err := windows.WaitForSingleObject(handle, 5000)
		if err != nil {
			return err
		}
		if state != windows.WAIT_OBJECT_0 {
			return errors.New("Cage 代理进程未能及时停止")
		}
	}
	_ = os.Remove(Socket(profile))
	return os.Remove(path)
}

func sameCore(record windowsCoreRecord, image string, created int64) bool {
	return strings.EqualFold(filepath.Clean(image), filepath.Clean(record.Executable)) && created == record.Created
}

func recordedCoreRunning(record windowsCoreRecord) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, record.PID)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	image, created, err := identifyCore(handle)
	return err == nil && sameCore(record, image, created)
}

func removeStaleCore(profile, path string) error {
	_ = os.Remove(Socket(profile))
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func startWindowsCore(ctx context.Context, profile string, s Store) error {
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
	if err := stopWindowsCore(profile); err != nil {
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
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW}
	if err := cmd.Start(); err != nil {
		_ = log.Close()
		return err
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	image, created, err := identifyCore(handle)
	windows.CloseHandle(handle)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	record, _ := json.Marshal(windowsCoreRecord{PID: uint32(cmd.Process.Pid), Executable: image, Created: created})
	if err := Write(windowsCoreRecordPath(profile), record); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	go func() {
		err := cmd.Wait()
		_ = log.Close()
		if err != nil {
			_ = diagnostics.New(profile).Record("error", "proxy", "core exited: "+err.Error())
		}
	}()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(15 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = stopWindowsCore(profile)
			return ctx.Err()
		case <-timeout.C:
			_ = stopWindowsCore(profile)
			return errors.New("代理核心启动超时，请查看私有代理日志")
		case <-ticker.C:
			if Running(ctx, profile, s) {
				if err := host.ProtectFile(Socket(profile)); err != nil {
					_ = stopWindowsCore(profile)
					return err
				}
				return nil
			}
		}
	}
}

func windowsPhysicalInterface(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// Select only hardware adapters with a usable IPv4 default route. Virtual
	// VPN/TUN adapters never become the proxy's underlying host connection.
	const script = `$ErrorActionPreference='Stop'; [Console]::OutputEncoding=[Text.UTF8Encoding]::new($false); $candidates=@(Get-NetAdapter -Physical | Where-Object Status -eq 'Up' | ForEach-Object { $a=$_; $ip=Get-NetIPAddress -InterfaceIndex $a.ifIndex -AddressFamily IPv4 -ErrorAction SilentlyContinue | Where-Object { $_.AddressState -eq 'Preferred' -and $_.IPAddress -notlike '169.254.*' }; $routes=Get-NetRoute -InterfaceIndex $a.ifIndex -AddressFamily IPv4 -DestinationPrefix '0.0.0.0/0' -ErrorAction SilentlyContinue; if ($ip) { $routes | Where-Object { $_.NextHop -ne '0.0.0.0' } | ForEach-Object { [pscustomobject]@{Name=$a.Name;Index=$a.ifIndex;Metric=($_.RouteMetric+$_.InterfaceMetric)} } } }); $best=$candidates | Sort-Object Metric,Index | Select-Object -First 1; if (!$best) { exit 2 }; $best | ConvertTo-Json -Compress`
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	cmd := exec.CommandContext(ctx, filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		var exited *exec.ExitError
		if errors.As(err, &exited) && exited.ExitCode() == 2 {
			return "", errors.New("未找到可用的物理 IPv4 网卡。请连接以太网或 Wi-Fi；仅有虚拟网卡的 Windows 虚拟机和云主机暂不可用。Cage 外网保持关闭")
		}
		return "", errors.New("无法读取 Windows 物理网卡信息，请确认系统网络服务正常后重试；Cage 外网保持关闭")
	}
	var selected struct {
		Name  string
		Index int
	}
	if json.Unmarshal(out, &selected) != nil || selected.Name == "" || selected.Index <= 0 {
		return "", errors.New("Windows 物理网卡信息无效")
	}
	iface, err := net.InterfaceByIndex(selected.Index)
	if err != nil || iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
		return "", errors.New("物理网卡已断开，请重试")
	}
	return iface.Name, nil
}
