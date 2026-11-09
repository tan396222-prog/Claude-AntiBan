package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"al.essio.dev/pkg/shellescape"

	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/containerenv"
	"github.com/sylvinus/agent-vm/internal/managedproxy"
)

var loadConfig = cage.Load
var defaultConfig = cage.DefaultConfig

type guestCredentials struct {
	Token       string `json:"token"`
	VNCPassword string `json:"-"`
}

func (s *Server) workspace() string      { s.mu.Lock(); defer s.mu.Unlock(); return s.preferences.Workspace }
func (s *Server) environmentDir() string { return environmentWorkspace(s.profile) }
func (s *Server) sshTarget() string {
	if containerenv.Configured(s.profile) {
		return "cage-container"
	}
	return "lima-" + cage.InstanceName(s.profile, s.environmentDir())
}

func (s *Server) environmentPrepared() bool {
	if containerenv.Configured(s.profile) {
		environment, err := containerenv.Load(s.profile)
		if err != nil {
			return false
		}
		ctx, cancel := context.WithTimeout(s.lifetimeContext(), 3*time.Second)
		defer cancel()
		state, err := environment.State(ctx)
		return err == nil && state.Exists
	}
	_, err := os.Stat(filepath.Join(s.profile, "state", ".agent-vm-base-version"))
	return err == nil
}
func (s *Server) sshCommand(ctx context.Context, args ...string) *exec.Cmd {
	return s.sshCommandOptions(ctx, false, args...)
}
func (s *Server) sshCommandWithForwarding(ctx context.Context, args ...string) *exec.Cmd {
	return s.sshCommandOptions(ctx, true, args...)
}
func (s *Server) sshCommandOptions(ctx context.Context, maintenance bool, args ...string) *exec.Cmd {
	cfg := filepath.Join(s.instanceDir(), "ssh.config")
	base := []string{"-q", "-e", "none", "-F", cfg, "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-o", "PermitLocalCommand=no", "-o", "PasswordAuthentication=no", "-o", "KbdInteractiveAuthentication=no", "-o", "ProxyCommand=none", "-o", "ProxyJump=none", "-o", "ConnectTimeout=3", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=2", "-o", "ForwardAgent=no", "-o", "IdentityAgent=none", "-o", "ForwardX11=no", "-o", "ClearAllForwardings=yes", "-o", "ControlPath=none"}
	if maintenance {
		for index, value := range base {
			if value == "ClearAllForwardings=yes" {
				base[index] = "ClearAllForwardings=no"
			}
		}
	}
	base = append(base, args...)
	cmd := exec.CommandContext(ctx, sshExecutable(), base...)
	cmd.Env = sshEnvironment()
	hideCommand(cmd)
	return cmd
}
func (s *Server) guest(ctx context.Context, script string) ([]byte, error) {
	// SSH enters the user's default shell. Run control scripts in Bash so
	// zsh initialization cannot change their redirection or error behavior.
	command := "exec /bin/bash --noprofile --norc -c " + shellescape.Quote(script)
	cmd := s.sshCommand(ctx, "-T", s.sshTarget(), "--", command)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(pipe, (64<<10)+1))
	if readErr != nil || len(data) > 64<<10 {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, errors.New("guest control response exceeded its limit")
	}
	if err = cmd.Wait(); err != nil {
		return nil, err
	}
	return data, nil
}
func (s *Server) attach() error {
	ctx, cancel := s.controlContext()
	defer cancel()
	if containerenv.Configured(s.profile) {
		environment, err := containerenv.Load(s.profile)
		if err != nil {
			return err
		}
		state, err := environment.State(ctx)
		if err != nil {
			return err
		}
		if !state.Running {
			return errors.New("环境尚未启动")
		}
		if !state.Ready {
			s.mu.Lock()
			s.ready = false
			s.mu.Unlock()
			if !state.MountsReady {
				return errors.New("容器项目共享需要初始化，请保存工作后完整停止并重新启动环境")
			}
			if !state.ResourcesVerified {
				return errors.New("容器资源限制尚未通过核验，请检查服务器资源配置")
			}
			return errors.New("容器网络与文件连接尚未就绪，请重试启动")
		}
	} else if !s.vmRunning() {
		return errors.New("环境尚未启动")
	}
	b, err := s.guest(ctx, guestConnectionSnapshot)
	if err != nil {
		return errors.New("环境尚未准备好")
	}
	credentials, manifest, err := parseGuestConnection(b)
	s.mu.Lock()
	s.guestManifest = manifest
	s.guestUpdateRequired = errors.Is(err, errGuestUpdateRequired)
	s.guestUpdateError = ""
	if err != nil {
		s.ready = false
		s.guestUpdateError = err.Error()
	}
	s.mu.Unlock()
	if err != nil {
		if errors.Is(err, errGuestUpdateRequired) {
			_ = s.closeProxyGate("环境组件需要更新，外网保持关闭")
		}
		return err
	}
	if _, err = s.guestAPI(ctx, credentials.Token, "GET", "/health", nil); err != nil {
		return err
	}
	s.mu.Lock()
	home := browserHomeURL(s.preferences)
	s.mu.Unlock()
	if err = s.setBrowserHome(ctx, credentials.Token, home); err != nil {
		return err
	}
	s.mu.Lock()
	s.credentials = credentials
	s.ready = true
	s.mu.Unlock()
	s.restoreProjectShares()
	return nil
}
func (s *Server) monitor(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if s.op.TryLock() {
			if !s.busy() {
				s.mu.Lock()
				ready := s.ready
				token := s.credentials.Token
				s.mu.Unlock()
				if !ready {
					_ = s.attach()
				} else {
					ctx, cancel := s.controlContext()
					_, err := s.guestAPI(ctx, token, "GET", "/health", nil)
					cancel()
					if err != nil {
						s.mu.Lock()
						s.ready = false
						s.mu.Unlock()
					}
				}
			}
			s.op.Unlock()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) runCage(args ...string) error {
	ctx := s.operationContext()
	// Setup and startup can both install dependencies. Downloads own their
	// inactivity timeouts; the operation context still permits an explicit stop.
	if len(args) != 1 || (args[0] != "setup" && args[0] != "up") {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Minute)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, s.exe, args...)
	hideCommand(cmd)
	cmd.Dir = s.environmentDir()
	if cmd.Dir == "" {
		cmd.Dir = s.profile
	}
	cmd.Env = append(os.Environ(), cage.ConfigEnv+"="+s.configPath)
	execution := s.jobs.execution()
	if len(args) == 1 && args[0] == "setup" {
		cmd.Env = append(cmd.Env, "CAGE_SETUP_PROGRESS=1")
	}
	cmd.Env = append(cmd.Env, cage.JobEnv+"="+execution.id)
	cmd.Stdout, cmd.Stderr = execution, execution
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s 未完成，请查看操作详情并重试", args[0])
	}
	return nil
}
func (s *Server) startEnvironment(syncTimezone bool) error {
	// Check before proxy activation or the first image download. The UI cannot
	// bypass this gate by calling /start or applying settings directly.
	if err := requireHostGit(s.operationContext()); err != nil {
		return err
	}
	if _, err := validateWorkspace(s.environmentDir(), s.profile); err != nil {
		return err
	}
	if containerenv.Configured(s.profile) {
		environment, err := containerenv.Load(s.profile)
		if err != nil {
			return err
		}
		state, err := environment.State(s.operationContext())
		if err != nil {
			return err
		}
		if state.Running && !state.MountsReady {
			s.mu.Lock()
			s.ready = false
			s.mu.Unlock()
			return errors.New("容器项目共享需要初始化，请保存工作后先停止环境，再重新启动；项目和登录数据会保留")
		}
	}
	cfg, err := loadConfig(s.configPath)
	if err != nil {
		return err
	}
	if err = cfg.RequireManagedProxy(s.profile); err != nil {
		_ = s.closeProxyGate(err.Error())
		return err
	}
	{
		p, e := managedproxy.Load(s.profile)
		if e != nil {
			return e
		}
		if p.Selected == "" || p.Endpoint() != cfg.ProxyURL || p.Revision != cfg.ProxyRevision {
			return errors.New("内置代理配置未应用，请重新选择节点")
		}
		name, e := physicalProxyInterface(s.operationContext())
		if e != nil {
			_ = s.closeProxyGate(e.Error())
			return e
		}
		bound, _ := managedproxy.BoundInterface(filepath.Join(managedproxy.Dir(s.profile), "config.yaml"))
		if bound != name || !managedproxy.Running(s.operationContext(), s.profile, p) {
			// Every core start changes the report generation, including recovery.
			if e = s.activateProxy(p, cfg); e != nil {
				return e
			}
			cfg, e = loadConfig(s.configPath)
			if e != nil {
				return e
			}
		}
	}

	attachErr := s.attach()
	if errors.Is(attachErr, errGuestUpdateRequired) {
		return s.synchronizeEnvironment()
	}
	if attachErr == nil {
		client, gateErr := s.gateClient()
		if gateErr == nil {
			state, e := client.Status(context.Background())
			if e == nil {
				if state.Mode == cage.GateOpen && state.Report != nil && state.Report.Authorizes(state.RunID, cfg.Fingerprint(), time.Now()) == nil {
					s.publishGate(state)
					return nil
				}
				return s.checkNetworkIfWanted()
			}
		}
		s.setMessage("正在重启环境以加载 VM 外网络门槛…")
		if err := s.stopEnvironment(); err != nil {
			return err
		}
	}
	s.mu.Lock()
	preferences := s.preferences
	auto := preferences.AutoTimezone || preferences.AutoLanguage
	s.mu.Unlock()
	s.setMessage("正在验证代理出口…")
	loc, probeErr := probeEnvironmentLocation(s.operationContext(), cfg, preferences)
	if s.operationContext().Err() != nil {
		return s.operationContext().Err()
	}
	s.mu.Lock()
	if probeErr != nil {
		s.networkError = probeErr.Error()
	} else {
		s.location = &loc
		s.networkError = ""
	}
	s.mu.Unlock()
	if cfg.ExitPolicy.NeedsBinding() && loc.IP != "" && !s.vmRunning() {
		bound := cfg.ExitPolicy.BindMissing(loc.exitLocation())
		if bound != cfg.ExitPolicy {
			cfg.ExitPolicy = bound
			if err = writePrivate(s.configPath, cfg); err != nil {
				return err
			}
		}
	}
	if auto && syncTimezone && !s.vmRunning() {
		if probeErr != nil {
			if !s.environmentPrepared() {
				return probeErr
			}
		}
		if probeErr == nil {
			observed := loc.exitLocation()
			within, _ := cfg.ExitPolicy.Evaluate(loc.IP, &observed)
			updated := cfg
			if within == cage.CheckPassed {
				updated = autoRegionSettings(cfg, preferences, loc)
			}
			if updated != cfg {
				// Changing a live hostagent's profile is forbidden. Stop before replacing it.
				s.setMessage("正在应用出口所在地的时区与语言建议…")
				if err = s.stopEnvironment(); err != nil {
					return err
				}
				cfg = updated
				if err = writePrivate(s.configPath, cfg); err != nil {
					return err
				}
			}
		}
	} else if probeErr != nil {
		s.setMessage("正在检查代理连通性…")
		if !s.environmentPrepared() {
			if err = s.runCage("check"); err != nil {
				return err
			}
		}
	}
	if _, err = os.Stat(filepath.Join(s.profile, "state", ".agent-vm-base-version")); !containerenv.Configured(s.profile) && os.IsNotExist(err) {
		s.setMessage("首次准备 Linux 环境与开发工具，可能需要数分钟…")
		job := s.jobs.execution()
		job.setSetupProgress(&SetupProgress{Phase: "image", Label: "正在准备 Debian 13 镜像"})
		if err = s.runCage("setup"); err != nil {
			return err
		}
		job.setSetupProgress(&SetupProgress{Phase: "services", Label: "正在启动浏览器和编辑器"})
		defer job.setSetupProgress(nil)
	}
	s.setMessage("正在启动浏览器、编辑器与共享文件…")
	if err = s.runCage("up"); err != nil {
		return err
	}
	for i := 0; i < 8; i++ {
		if s.operationContext().Err() != nil {
			return s.operationContext().Err()
		}
		if err = s.attach(); err == nil {
			return s.checkNetworkIfWanted()
		}
		time.Sleep(time.Second)
	}
	return errors.New("环境已启动，但浏览器连接尚未就绪。请重试连接或查看操作详情。")
}
func (s *Server) stopEnvironment() (returned error) {
	s.setMessage("正在停止 Cage 环境…")
	explicitStop := s.jobs.snapshot().Kind == "stop"
	defer func() {
		if explicitStop && returned == nil {
			returned = managedproxy.Stop(s.profile)
		}
	}()
	if (explicitStop || s.jobs.snapshot().Kind == "storage") && cage.InstanceJobID(filepath.Join(s.profile, "state"), "base-building") != "" {
		if err := s.runCage("stop", "base-building"); err != nil {
			return err
		}
	}
	if client, err := s.gateClient(); err == nil {
		if state, e := client.Close(context.Background(), "环境正在停止"); e == nil {
			s.publishGate(state)
		}
	}
	s.mu.Lock()
	s.ready = false
	s.credentials = guestCredentials{}
	s.guestManifest = nil
	s.guestUpdateRequired = false
	s.guestUpdateError = ""
	s.safety.Error = ""
	s.mu.Unlock()
	s.terminals.closeAll()
	s.closeProjectShares()
	if s.workspace() == "" {
		return nil
	}
	if _, err := os.Stat(filepath.Join(s.instanceDir(), "lima.yaml")); !containerenv.Configured(s.profile) && os.IsNotExist(err) {
		return nil
	}
	if err := s.runCage("stop"); err != nil {
		return err
	}
	return nil
}
func (s *Server) apply(v savedProfile, validate bool) error {
	old, err := loadConfig(s.configPath)
	if err != nil {
		return err
	}
	// Network endpoints are generated by the node manager, including restore.
	v.Config.ProxyURL, v.Config.ProxyRevision = old.ProxyURL, old.ProxyRevision
	s.mu.Lock()
	oldPrefs := s.preferences
	resumeVersion := s.safety.pauseVersion
	if execution := s.jobs.execution(); execution.id != "" {
		resumeVersion = execution.pauseVersion
	}
	wasReady := vmAlive(s.profile, s.environmentDir())
	s.mu.Unlock()
	changed := old != v.Config || oldPrefs.AutoTimezone != v.Preferences.AutoTimezone || oldPrefs.AutoLanguage != v.Preferences.AutoLanguage
	if changed && validate && old.RequireManagedProxy(s.profile) == nil {
		s.setMessage("正在验证新出口，现有环境保持运行…")
		loc, e := probeEnvironmentLocation(s.operationContext(), v.Config, v.Preferences)
		if e != nil {
			if v.Preferences.AutoTimezone || v.Preferences.AutoLanguage || loc.IP == "" {
				return e
			}
			s.mu.Lock()
			s.networkError = e.Error()
			s.mu.Unlock()
		} else {
			v.Config = autoRegionSettings(v.Config, v.Preferences, loc)
			s.mu.Lock()
			s.location = &loc
			s.networkError = ""
			s.mu.Unlock()
		}
		v.Config.ExitPolicy = v.Config.ExitPolicy.BindMissing(loc.exitLocation())
		observed := loc.exitLocation()
		if result, message := v.Config.ExitPolicy.Evaluate(loc.IP, &observed); result != cage.CheckPassed {
			if result == cage.CheckUnknown && e != nil && !v.Preferences.AutoTimezone && !v.Preferences.AutoLanguage {
				return errors.New("时区和语言已选择手动设置，但出口规则仍要求确认国家或城市。请重试位置检测，或在“IP 变化规则”中明确选择静态 IP 或不限制地区；原配置保持不变")
			}
			return errors.New(message)
		}
	}
	if changed {
		if err := s.resumeNetworkIntentAt(resumeVersion); err != nil {
			return err
		}
		if s.operationContext().Err() != nil {
			return s.operationContext().Err()
		}
		if err = writePrivate(filepath.Join(s.profile, "previous-profile.json"), savedProfile{old, oldPrefs}); err != nil {
			return err
		}
		if err = s.stopEnvironment(); err != nil {
			return err
		}
	}
	// A project is a live directory grant in the same VM, never a lifecycle change.
	if v.Preferences.Workspace != "" && s.vmRunning() {
		s.setMessage("正在连接项目文件夹…")
		if _, err = s.connectProject(v.Preferences.Workspace, true); err != nil {
			return err
		}
	}
	if err = writePrivate(s.configPath, v.Config); err != nil {
		return err
	}
	if err = writePrivate(filepath.Join(s.profile, "workbench.json"), v.Preferences); err != nil {
		_ = writePrivate(s.configPath, old)
		return err
	}
	s.mu.Lock()
	s.preferences = v.Preferences
	token, ready := s.credentials.Token, s.ready
	s.mu.Unlock()
	if !changed && ready && browserHomeURL(oldPrefs) != browserHomeURL(v.Preferences) {
		ctx, cancel := s.controlContext()
		defer cancel()
		if err = s.setBrowserHome(ctx, token, browserHomeURL(v.Preferences)); err != nil {
			return fmt.Errorf("设置已保存，但浏览器暂时未能应用默认网站，请恢复连接后重试：%w", err)
		}
	}
	if changed && wasReady {
		if err = s.startEnvironment(false); err != nil {
			return fmt.Errorf("新设置已保存，但环境未能启动。可以重试启动，或恢复上一份配置：%w", err)
		}
	}
	return nil
}

// Interface recovery uses the ordinary cancellable job queue. Location changes
// remain governed by the saved exit policy and never replace its baseline.
func (s *Server) monitorNetwork(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if s.op.TryLock() {
			s.monitorProxyInterface()
			s.checkProxyHealth()
			s.monitorSafety()
			s.recoverNetwork()
			s.refreshProxySubscriptions()
			s.op.Unlock()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func vmAlive(profile, workspace string) bool {
	if containerenv.Configured(profile) {
		environment, err := containerenv.Load(profile)
		if err != nil {
			return false
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		state, err := environment.State(ctx)
		return err == nil && state.Running
	}
	if workspace == "" {
		return false
	}
	b, e := os.ReadFile(filepath.Join(profile, "state", "lima", cage.InstanceName(profile, workspace), "cage-running.json"))
	if e != nil {
		return false
	}
	var record struct {
		PID int `json:"pid"`
	}
	if json.Unmarshal(b, &record) != nil || record.PID < 1 {
		return false
	}
	return processExists(record.PID)
}
func (s *Server) vmRunning() bool { return vmAlive(s.profile, s.environmentDir()) }
