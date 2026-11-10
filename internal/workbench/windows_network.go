package workbench

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/containers/gvisor-tap-vsock/pkg/types"
	"github.com/containers/gvisor-tap-vsock/pkg/virtualnetwork"
	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/guestos"
	"github.com/sylvinus/agent-vm/internal/host"
)

type windowsHostNetwork struct {
	cancel context.CancelFunc
	done   chan struct{}
}

const windowsPacketDisconnectedReason = "Windows 客机网络通道已断开，等待重新检查"

func (s *Server) startWindowsNetwork() error {
	s.mu.Lock()
	if current := s.windowsNetwork; current != nil {
		select {
		case <-current.done:
			s.windowsNetwork = nil
		default:
			s.mu.Unlock()
			return nil
		}
	}
	if s.closing || s.exe == "" {
		s.mu.Unlock()
		return errors.New("Windows 客机网络服务尚未准备好")
	}
	ctx, cancel := context.WithCancel(s.lifetimeContextLocked())
	process := &windowsHostNetwork{cancel: cancel, done: make(chan struct{})}
	s.windowsNetwork = process
	s.mu.Unlock()
	cmd := exec.CommandContext(ctx, s.exe, "windows-network-host")
	cmd.Env = append(os.Environ(), cage.ConfigEnv+"="+s.configPath)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	host.HideCommandWindow(cmd)
	if err := cmd.Start(); err != nil {
		cancel()
		close(process.done)
		return err
	}
	go func() { defer close(process.done); _ = cmd.Wait() }()
	// Publish only after the real authenticated host gate is reachable.
	wait, cancelWait := context.WithTimeout(s.operationContext(), 30*time.Second)
	defer cancelWait()
	for {
		if gate, err := s.gateClient(); err == nil {
			if state, e := gate.Status(wait); e == nil {
				s.publishGate(state)
				return nil
			}
		}
		select {
		case <-process.done:
			return errors.New("Windows 客机网络服务未能启动")
		case <-wait.Done():
			cancel()
			<-process.done
			return errors.New("Windows 客机网络服务启动超时")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (s *Server) closeWindowsNetwork() {
	s.mu.Lock()
	network := s.windowsNetwork
	s.windowsNetwork = nil
	s.mu.Unlock()
	if network != nil {
		network.cancel()
		<-network.done
	}
}

// This child owns the complete packet stack and gate, just as the Linux VM's
// hostagent does. Process exit also retires gvisor's listeners and goroutines.
func ServeWindowsPacketNetwork(configPath string) error {
	if runtime.GOOS != "windows" {
		return errors.New("Windows 客机网络需要 Windows Hyper-V 主机")
	}
	if err := containWindowsNetworkProcess(); err != nil {
		return err
	}
	profile := filepath.Dir(configPath)
	binding, err := guestos.LoadWindowsProfile(profile)
	if err != nil || binding == nil {
		return errors.New("Windows 客机尚未绑定")
	}
	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}
	gate, err := cage.NewEgressGate(cfg)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	defer gate.Close("Windows 客机网络已停止")
	var stop func()
	defer func() {
		if stop != nil {
			stop()
		}
	}()
	network, err := virtualnetwork.New(&types.Configuration{
		Subnet: "192.168.127.0/24", GatewayIP: "192.168.127.1", GatewayMacAddress: "5a:94:ef:e4:0c:dd", MTU: 1500,
		DHCPStaticLeases: map[string]string{"192.168.127.2": "5a:94:ef:e4:0c:ee"},
		NAT:              map[string]string{}, Forwards: map[string]string{}, Outbound: gate.Allows, DialContext: gate.DialContext, DNSExchange: gate.ExchangeDNS,
	})
	if err != nil {
		return err
	}
	for ctx.Err() == nil {
		command, err := guestos.WindowsRelayCommand(ctx, profile, "network-stream")
		if err != nil {
			return err
		}
		conn, err := openWindowsPacketRelay(ctx, command, binding)
		if err == nil {
			if stop == nil {
				stop, err = cage.StartGateControl(configPath, binding.InstanceDirectory, gate)
				if err != nil {
					_ = conn.Close()
					return err
				}
			}
			_ = network.AcceptQemu(ctx, conn)
			_ = conn.Close()
		}
		gate.Close(windowsPacketDisconnectedReason)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Second):
		}
	}
	return nil
}

type windowsPacketRelay struct {
	net.Conn
	closeOnce sync.Once
	finish    func()
}

func (c *windowsPacketRelay) Close() error {
	c.closeOnce.Do(func() { _ = c.Conn.Close(); c.finish() })
	return nil
}

func openWindowsPacketRelay(parent context.Context, cmd *exec.Cmd, binding *guestos.WindowsProfile) (net.Conn, error) {
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		input.Close()
		return nil, err
	}
	diagnostic, err := cmd.StderrPipe()
	if err != nil {
		input.Close()
		output.Close()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		input.Close()
		output.Close()
		diagnostic.Close()
		return nil, err
	}
	deadlineDone := make(chan struct{})
	deadline := time.AfterFunc(25*time.Second, func() { defer close(deadlineDone); _ = cmd.Process.Kill(); diagnostic.Close() })
	reader := bufio.NewReaderSize(diagnostic, 4096)
	err = readWindowsPacketReady(reader, binding)
	stopped := cancelWindowsAdmissionDeadline(deadline, deadlineDone)
	if err != nil || !stopped || parent.Err() != nil {
		input.Close()
		output.Close()
		diagnostic.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, errors.New("Windows 专用网络尚未建立，外网保持关闭")
	}
	diagnosticDone := make(chan struct{})
	go func() { defer close(diagnosticDone); _, _ = io.Copy(io.Discard, reader) }()
	local, remote := net.Pipe()
	done := make(chan struct{})
	writeDone := make(chan struct{})
	go func() { defer close(writeDone); _, _ = io.Copy(input, remote); _ = input.Close() }()
	go func() {
		defer close(done)
		_, _ = io.Copy(remote, output)
		_ = remote.Close()
		_ = input.Close()
		_ = output.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-writeDone
		_ = cmd.Wait()
		_ = diagnostic.Close()
		<-diagnosticDone
	}()
	connection := &windowsPacketRelay{Conn: local, finish: func() {
		_ = remote.Close()
		_ = input.Close()
		_ = output.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-done
	}}
	return connection, nil
}

func readWindowsPacketReady(reader *bufio.Reader, binding *guestos.WindowsProfile) error {
	line, err := reader.ReadSlice('\n')
	if err != nil {
		return err
	}
	var ready struct {
		Ready     bool   `json:"network_ready"`
		Version   int    `json:"version"`
		VMID      string `json:"vm_id"`
		SID       string `json:"gui_user_sid"`
		Session   string `json:"session"`
		Network   string `json:"network"`
		Available bool   `json:"available"`
	}
	if json.Unmarshal(line, &ready) != nil || binding == nil || !ready.Ready || ready.Version != 1 || ready.VMID != binding.VMID.String() || ready.SID != binding.GUIUserSID || ready.Network != "packet" || !ready.Available || len(ready.Session) == 0 || len(ready.Session) > 128 {
		return errors.New("Windows 专用网络身份或就绪回执无效")
	}
	return nil
}
