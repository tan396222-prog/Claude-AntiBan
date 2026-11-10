package containerenv

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"al.essio.dev/pkg/shellescape"
	"github.com/lima-vm/sshocker/pkg/reversesshfs"
	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/host"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

const guestHelper = "/usr/local/lib/cage-runtime"

type networkRecord struct {
	ContainerID    string `json:"container_id"`
	PID            int    `json:"pid"`
	RunID          string `json:"run_id"`
	SSHAddress     string `json:"ssh_address"`
	BrowserAddress string `json:"browser_address"`
	VNCAddress     string `json:"vnc_address"`
	Shared         bool   `json:"shared"`
}

func (r *Runtime) networkState(ctx context.Context, containerID string) (networkRecord, error) {
	path := filepath.Join(r.InstanceDir(), "network.json")
	if err := host.CheckPrivateFile(path); err != nil {
		return networkRecord{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 8192 {
		return networkRecord{}, errors.New("容器连接记录不可用")
	}
	var record networkRecord
	if json.Unmarshal(data, &record) != nil || record.ContainerID != containerID || record.RunID == "" || !record.Shared {
		return record, errors.New("容器连接记录不属于当前运行环境")
	}
	for _, address := range []string{record.SSHAddress, record.BrowserAddress, record.VNCAddress} {
		host, port, err := net.SplitHostPort(address)
		number, parseErr := strconv.Atoi(port)
		ip := net.ParseIP(host)
		if err != nil || parseErr != nil || ip == nil || !ip.IsLoopback() || number < 1024 || number > 65535 {
			return record, errors.New("容器连接入口无效")
		}
	}
	client, err := cage.OpenGateClient(r.InstanceDir())
	if err != nil {
		return record, err
	}
	status, err := client.Status(ctx)
	if err != nil || status.RunID != record.RunID {
		return record, errors.New("容器网络控制服务尚未连接")
	}
	return record, nil
}

// Start starts an existing prepared container and its host network worker.
// Parent cancellation before readiness terminates only this newly created
// worker; the persistent container and all of its files remain available.
func (r *Runtime) Start(ctx context.Context, configPath string, log io.Writer) error {
	cfg, err := cage.Load(configPath)
	if err != nil {
		return err
	}
	if err := cfg.RequireManagedProxy(r.profile); err != nil {
		return err
	}
	if err := host.EnsurePrivateDir(r.InstanceDir()); err != nil {
		return err
	}
	unlock, err := lockRuntime(ctx, filepath.Join(r.InstanceDir(), "lifecycle.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	if err := r.ensureResourceSlice(ctx); err != nil {
		return err
	}
	inspection, err := r.Inspect(ctx)
	if err != nil {
		return err
	}
	if !inspection.State.Exists {
		return errors.New("容器尚未准备，请先运行 Cage 容器安装")
	}
	if inspection.State.Running {
		if !inspection.State.MountsReady {
			return errors.New("容器的项目挂载尚未初始化，请先完整停止环境再启动；文件和登录状态会保留")
		}
		if _, err := r.networkState(ctx, inspection.State.ContainerID); err == nil {
			client, err := cage.OpenGateClient(r.InstanceDir())
			if err != nil {
				return err
			}
			state, err := client.Status(ctx)
			if err != nil {
				return err
			}
			if state.Configuration != cfg.Fingerprint() {
				return errors.New("容器运行配置已变化，请先停止环境后重新启动")
			}
			return nil
		}
	} else if _, err := r.output(ctx, "start", inspection.State.ContainerID); err != nil {
		return err
	}
	if _, err := r.verifyResources(ctx, inspection.State.ContainerID); err != nil {
		if !inspection.State.Running {
			stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_, _ = r.output(stopCtx, "stop", "--time", "20", inspection.State.ContainerID)
			cancel()
		}
		return err
	}
	if !inspection.State.Running {
		if err := r.initializeMounts(ctx, inspection.State.ContainerID); err != nil {
			stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_, _ = r.output(stopCtx, "stop", "--time", "20", inspection.State.ContainerID)
			cancel()
			return err
		}
	}
	if err := r.InstallAccess(ctx); err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	if _, err := r.output(ctx, "cp", executable, inspection.State.ContainerID+":"+guestHelper); err != nil {
		return err
	}
	if _, err := r.output(ctx, "exec", "--user", "0", inspection.State.ContainerID, "chmod", "0755", guestHelper); err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(r.InstanceDir(), "network.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	command := exec.Command(executable, "container-network")
	command.Env = append(os.Environ(), cage.ConfigEnv+"="+configPath)
	command.Stdout, command.Stderr = file, file
	detachWorker(command)
	if err := command.Start(); err != nil {
		file.Close()
		return err
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait(); file.Close() }()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(45 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case err := <-done:
			return fmt.Errorf("容器网络服务退出，请查看容器网络日志: %w", err)
		case <-ctx.Done():
			_ = command.Process.Kill()
			<-done
			return ctx.Err()
		case <-timeout.C:
			_ = command.Process.Kill()
			<-done
			return errors.New("容器连接超时；容器文件保留，请查看网络日志")
		case <-ticker.C:
			checkCtx, cancel := context.WithTimeout(ctx, time.Second)
			record, err := r.networkState(checkCtx, inspection.State.ContainerID)
			cancel()
			if err == nil && record.PID == command.Process.Pid {
				if log != nil {
					fmt.Fprintln(log, "Cage container network and protected project share are ready.")
				}
				return nil
			}
		}
	}
}

// ServeNetwork runs in the dedicated `cage container-network` process. It is
// independent of viewer connections. Exiting closes the gate, every proxy
// connection and the sole TAP; it never adds a fallback network.
func (r *Runtime) ServeNetwork(ctx context.Context, configPath string) error {
	if err := host.EnsurePrivateDir(r.InstanceDir()); err != nil {
		return err
	}
	unlock, err := tryLockRuntime(filepath.Join(r.InstanceDir(), "network.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopWatch, err := cage.WatchJobStop(r.profile, os.Getenv(cage.JobEnv), cancel)
	if err != nil {
		return err
	}
	defer stopWatch()
	inspection, err := r.Inspect(ctx)
	if err != nil {
		return err
	}
	if !inspection.State.Running {
		return errors.New("容器未运行")
	}
	if !inspection.State.MountsReady {
		return errors.New("容器项目挂载尚未初始化，请完整停止后重新启动")
	}
	cfg, err := cage.Load(configPath)
	if err != nil {
		return err
	}
	if err := cfg.RequireManagedProxy(r.profile); err != nil {
		return err
	}
	network, err := NewNetwork(cfg)
	if err != nil {
		return err
	}
	defer network.Gate.Close("容器网络服务已停止")
	command := r.command(ctx, "exec", "-i", "--user", "0", inspection.State.ContainerID, guestHelper, "container-tap")
	command.Stderr = os.Stderr
	endpoint, wait, err := ProcessEndpoint(command)
	if err != nil {
		return err
	}
	defer func() { endpoint.Close(); _ = wait() }()
	networkDone := make(chan error, 1)
	go func() { err := network.Run(ctx, endpoint); cancel(); networkDone <- err }()
	client, err := r.connectGuestSSH(ctx, network)
	if err != nil {
		return err
	}
	defer client.Close()
	interruptSSH := context.AfterFunc(ctx, func() { _ = client.Close() })
	defer interruptSSH()
	shareDone, stopShare, err := r.shareEnvironment(ctx, client)
	if err != nil {
		return err
	}
	defer stopShare()
	sshListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer sshListener.Close()
	browserListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer browserListener.Close()
	vncListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer vncListener.Close()
	if err := r.writeSSHConfig(sshListener.Addr().(*net.TCPAddr).Port); err != nil {
		return err
	}
	serviceDone := make(chan error, 3)
	go func() { serviceDone <- network.ServeSSH(ctx, sshListener) }()
	go func() { serviceDone <- serveGuestService(ctx, client, browserListener, "127.0.0.1:18500") }()
	go func() { serviceDone <- serveGuestService(ctx, client, vncListener, "127.0.0.1:15900") }()
	stopGate, err := cage.StartGateControl(configPath, r.InstanceDir(), network.Gate)
	if err != nil {
		return err
	}
	defer stopGate()
	if err := cfg.RecordRunningAt(r.InstanceDir()); err != nil {
		return err
	}
	defer os.Remove(filepath.Join(r.InstanceDir(), "cage-running.json"))
	record := networkRecord{ContainerID: inspection.State.ContainerID, PID: os.Getpid(), RunID: network.Gate.Status().RunID, SSHAddress: sshListener.Addr().String(), BrowserAddress: browserListener.Addr().String(), VNCAddress: vncListener.Addr().String(), Shared: true}
	if err := writePrivateJSON(filepath.Join(r.InstanceDir(), "network.json"), record); err != nil {
		return err
	}
	defer os.Remove(filepath.Join(r.InstanceDir(), "network.json"))
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-networkDone:
		return err
	case err := <-shareDone:
		return fmt.Errorf("受限项目共享已断开: %w", err)
	case err := <-serviceDone:
		return err
	}
}

func (r *Runtime) connectGuestSSH(ctx context.Context, network *Network) (*ssh.Client, error) {
	configuration, err := r.guestSSHConfiguration()
	if err != nil {
		return nil, err
	}
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		connection, err := network.stack.DialContextTCP(attemptCtx, GuestIP+":22")
		if err == nil {
			_ = connection.SetDeadline(time.Now().Add(2 * time.Second))
			stop := context.AfterFunc(attemptCtx, func() { _ = connection.Close() })
			transport, channels, requests, handshakeErr := ssh.NewClientConn(connection, "cage-container-"+r.identity+":22", configuration)
			stop()
			cancel()
			if handshakeErr == nil {
				_ = connection.SetDeadline(time.Time{})
				return ssh.NewClient(transport, channels, requests), nil
			}
			_ = connection.Close()
			var mismatch *knownhosts.KeyError
			if errors.As(handshakeErr, &mismatch) {
				return nil, errors.New("容器 SSH 主机身份不匹配")
			}
		} else {
			cancel()
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, errors.New("容器 SSH 未能就绪")
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (r *Runtime) guestSSHConfiguration() (*ssh.ClientConfig, error) {
	private, err := os.ReadFile(filepath.Join(r.InstanceDir(), "id_ed25519"))
	if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(private)
	if err != nil {
		return nil, err
	}
	check, err := knownhosts.New(filepath.Join(r.InstanceDir(), "known_hosts"))
	if err != nil {
		return nil, err
	}
	configuration := &ssh.ClientConfig{User: "cage", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: check, HostKeyAlgorithms: []string{ssh.KeyAlgoED25519}}
	return configuration, nil
}

func serveGuestService(ctx context.Context, client *ssh.Client, listener net.Listener, target string) error {
	stop := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stop()
	for {
		local, err := listener.Accept()
		if err != nil {
			return err
		}
		go func() {
			defer local.Close()
			interrupt := context.AfterFunc(ctx, func() { _ = local.Close() })
			defer interrupt()
			remote, err := client.Dial("tcp", target)
			if err != nil {
				return
			}
			defer remote.Close()
			interruptRemote := context.AfterFunc(ctx, func() { _ = remote.Close() })
			defer interruptRemote()
			done := make(chan struct{})
			go func() { _, _ = io.Copy(remote, local); _ = remote.Close(); _ = local.Close(); close(done) }()
			_, _ = io.Copy(local, remote)
			_ = local.Close()
			_ = remote.Close()
			<-done
		}()
	}
}

func (r *Runtime) shareEnvironment(ctx context.Context, client *ssh.Client) (<-chan error, func(), error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	workspace := cage.EnvironmentDir(r.profile)
	if err := host.EnsurePrivateDir(workspace); err != nil {
		return nil, nil, err
	}
	quoted := shellescape.Quote(workspace)
	prepare, err := client.NewSession()
	if err != nil {
		return nil, nil, err
	}
	interrupt := context.AfterFunc(ctx, func() { _ = prepare.Close() })
	err = prepare.Run("if mountpoint -q " + quoted + "; then fusermount3 -uz " + quoted + "; fi; sudo mkdir -p " + quoted + " && sudo chown cage:cage " + quoted)
	interrupt()
	prepare.Close()
	if err != nil {
		return nil, nil, errors.New("无法准备容器项目共享目录")
	}
	probe, err := os.CreateTemp(workspace, ".cage-mount-probe-*")
	if err != nil {
		return nil, nil, err
	}
	defer os.Remove(probe.Name())
	var nonce [24]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		probe.Close()
		return nil, nil, err
	}
	challenge := hex.EncodeToString(nonce[:])
	_, err = probe.WriteString(challenge)
	probe.Close()
	if err != nil {
		return nil, nil, err
	}
	session, err := client.NewSession()
	if err != nil {
		return nil, nil, err
	}
	reader, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		return nil, nil, err
	}
	writer, err := session.StdinPipe()
	if err != nil {
		session.Close()
		return nil, nil, err
	}
	session.Stderr = os.Stderr
	if err = session.Start("exec sshfs " + shellescape.Quote(":"+workspace) + " " + quoted + " -o slave,allow_other,cache=no,nodev,nosuid"); err != nil {
		session.Close()
		return nil, nil, err
	}
	stop := func() { _ = writer.Close(); _ = session.Close() }
	done := make(chan error, 1)
	go func() {
		err := reversesshfs.ServeConfined(&shareTransport{Reader: reader, Writer: writer, close: stop}, workspace, false, nil)
		stop()
		_ = session.Wait()
		done <- err
	}()
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	for {
		check, err := client.NewSession()
		if err != nil {
			stop()
			return nil, nil, err
		}
		var value bytes.Buffer
		check.Stdout = &value
		interrupt := context.AfterFunc(ctx, func() { _ = check.Close() })
		err = check.Run("cat " + shellescape.Quote(probe.Name()))
		interrupt()
		check.Close()
		if err == nil && value.String() == challenge {
			return done, stop, nil
		}
		select {
		case <-ctx.Done():
			stop()
			return nil, nil, ctx.Err()
		case <-deadline.C:
			stop()
			return nil, nil, errors.New("容器项目共享验证超时")
		case err := <-done:
			stop()
			return nil, nil, fmt.Errorf("容器项目共享失败: %w", err)
		case <-time.After(150 * time.Millisecond):
		}
	}
}

func (r *Runtime) GuestCommand(ctx context.Context, args ...string) *exec.Cmd {
	base := []string{"-F", r.SSHConfig(), "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-o", "ForwardAgent=no", "-o", "ClearAllForwardings=yes", "-o", "ConnectTimeout=3"}
	return exec.CommandContext(ctx, "ssh", append(base, args...)...)
}

type shareTransport struct {
	io.Reader
	io.Writer
	close func()
}

func (s *shareTransport) Close() error { s.close(); return nil }
