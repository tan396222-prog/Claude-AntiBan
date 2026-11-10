package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/sylvinus/agent-vm/internal/cage"
	"io"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/sylvinus/agent-vm/internal/guestos"
	"github.com/sylvinus/agent-vm/internal/managedproxy"
)

type windowsGuestState struct {
	Version              int                              `json:"version"`
	OS                   string                           `json:"os"`
	VMID                 string                           `json:"vm_id"`
	SID                  string                           `json:"gui_user_sid"`
	Session              string                           `json:"session"`
	Available            bool                             `json:"available"`
	Reason               string                           `json:"reason"`
	Capabilities         []string                         `json:"capabilities"`
	Browser              windowsBrowserState              `json:"browser"`
	Components           map[string]windowsComponentState `json:"components"`
	Running              bool                             `json:"-"`
	ComponentsChecked    time.Time                        `json:"-"`
	ComponentsRefreshing bool                             `json:"-"`
}

type windowsComponentState struct {
	Supported                 bool            `json:"supported,omitempty"`
	Registered                bool            `json:"registered"`
	Installed                 bool            `json:"installed"`
	Ready                     bool            `json:"ready"`
	Reason                    string          `json:"reason"`
	Stage                     string          `json:"stage,omitempty"`
	NativeCode                string          `json:"native_code,omitempty"`
	AudioServiceNativeCode    string          `json:"audio_service_native_code,omitempty"`
	EndpointServiceNativeCode string          `json:"endpoint_service_native_code,omitempty"`
	Dependencies              map[string]bool `json:"dependencies,omitempty"`
	CheckedAt                 int64           `json:"checked_at_ms,omitempty"`
}

type windowsBrowserState struct {
	Installed              bool `json:"installed"`
	NetworkConfigured      bool `json:"network_configured"`
	Ready                  bool `json:"ready"`
	EditorInstalled        bool `json:"editor_installed"`
	PhoneInstalled         bool `json:"phone_installed"`
	PhoneNetworkConfigured bool `json:"phone_network_configured"`
}

func windowsWorkbenchRoute(path string) bool {
	if nativeWorkbenchRoute(path) || path == "/api/claude-desktop" {
		return true
	}
	return strings.HasPrefix(path, "/api/claude-desktop/") || strings.HasPrefix(path, "/api/safety/") || strings.HasPrefix(path, "/api/settings/") || strings.HasPrefix(path, "/api/input/")
}

var (
	errWindowsAgentPending  = errors.New("Windows 客机已启动，正在等待图形用户代理连接")
	errWindowsLoginRequired = errors.New("请在虚拟 Windows 的登录界面解锁已绑定的客机账户，再返回 Cage")
)

func (s *Server) windowsStatus(ctx context.Context) (windowsGuestState, error) {
	if runtime.GOOS != "windows" {
		return windowsGuestState{}, errors.New("Windows 客机需要 Windows Hyper-V 主机")
	}
	binding := s.windowsGuest
	report, err := guestos.InspectHyperV(ctx, binding.VMID)
	state := windowsGuestState{Running: err == nil && report.VM != nil && report.VM.State == "Running"}
	if err != nil || report.Lookup != "found" || report.VM == nil {
		return state, errors.New("无法核验已绑定的 Windows 客机")
	}
	vm := report.VM
	if vm.Generation != 2 || !vm.SecureBoot || vm.SecureBootTemplate != "MicrosoftWindows" || !vm.VirtualTPM {
		return state, errors.New("Windows 客机安全启动与虚拟 TPM 尚未就绪")
	}
	if vm.NetworkAdapterCount != 0 {
		return state, errors.New("Windows 客机受控网络出口尚未就绪，请使用已隔离的离线客机")
	}
	if !state.Running {
		return state, errors.New("Windows 客机尚未启动")
	}
	cmd, err := guestos.WindowsRelayCommand(ctx, s.profile, "status")
	if err != nil {
		return state, err
	}
	var output bytes.Buffer
	cmd.Stdout = &boundedWindowsStatusWriter{buffer: &output, remaining: 64 << 10}
	cmd.Stderr = io.Discard
	if err = cmd.Run(); err != nil {
		return state, errWindowsAgentPending
	}
	running := state.Running
	decoder := json.NewDecoder(&output)
	if decoder.Decode(&state) != nil || decoder.Decode(new(any)) != io.EOF {
		return windowsGuestState{Running: running}, errors.New("Windows 客机状态响应无效")
	}
	state.Running = running
	if state.Version != 1 || state.OS != "windows" || state.VMID != binding.VMID.String() || state.SID != binding.GUIUserSID || state.Session == "" || len(state.Session) > 256 {
		return state, errors.New("Windows 客机或图形用户身份不匹配")
	}
	state.ComponentsChecked = time.Now()
	if !state.Available {
		return state, errWindowsLoginRequired
	}
	return state, nil
}

type boundedWindowsStatusWriter struct {
	buffer    *bytes.Buffer
	remaining int
}

func (w *boundedWindowsStatusWriter) Write(data []byte) (int, error) {
	if len(data) > w.remaining {
		return 0, errors.New("Windows 客机状态超过限制")
	}
	w.remaining -= len(data)
	return w.buffer.Write(data)
}

func (s *Server) attachWindowsGuest() error {
	return s.attachWindowsGuestContext(s.lifetimeContext())
}

func (s *Server) attachWindowsGuestContext(parent context.Context) error {
	if err := s.requireStorageReady(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	state, err := s.windowsStatus(ctx)
	if err == nil {
		err = s.requireStorageReady()
	}
	restartedNetwork := false
	if err == nil && s.windowsGuest.NetworkPairingFile != "" {
		// An alive helper alone does not prove its packet connection survived.
		// The GUI reports the protected SYSTEM receipt, including its freshness.
		// Retire only this controller's owned helper when that receipt is lost.
		if !state.Browser.NetworkConfigured {
			s.mu.Lock()
			restartedNetwork = s.windowsNetwork != nil
			s.mu.Unlock()
			if restartedNetwork {
				s.closeWindowsNetwork()
			}
		}
		err = s.startWindowsNetwork()
	}
	if err == nil {
		err = s.ensureProjectShare(s.environmentDir())
	}
	s.mu.Lock()
	if s.storageRecoveryError != "" {
		err = errors.New(s.storageRecoveryError)
		s.mu.Unlock()
		return err
	}
	s.windowsState = state
	newlyReady := !s.ready && err == nil
	s.ready = err == nil
	s.guestUpdateError = ""
	if err != nil {
		s.guestUpdateError = err.Error()
	}
	s.mu.Unlock()
	if newlyReady {
		s.restoreProjectShares()
	}
	if err == nil && (newlyReady || restartedNetwork) {
		s.mu.Lock()
		checkWanted := !s.closing && !s.networkPaused && !s.jobs.snapshot().Running
		s.mu.Unlock()
		if checkWanted {
			// A new connection starts closed. Use the normal queue and a fresh
			// full check; an earlier connection's report never reopens this one.
			_, _ = s.submitJob("network-recovery", s.checkNetworkIfWanted)
		}
	}
	return err
}

// Hyper-V reports Running as soon as virtual CPUs start. Windows can still be
// booting, or its limited interactive logon task may not have connected yet.
func waitWindowsBoot(ctx context.Context, interval time.Duration, attach func(context.Context) error) error {
	for {
		err := attach(ctx)
		if !errors.Is(err, errWindowsAgentPending) {
			return err
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return errors.New("Windows 客机尚未连接。请确认已在虚拟 Windows 中登录绑定的账户，随后在 Cage 点击恢复连接")
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (s *Server) startWindowsGuest() error {
	if err := s.requireStorageReady(); err != nil {
		return err
	}
	if err := requireHostGit(s.operationContext()); err != nil {
		return err
	}
	cfg, err := loadConfig(s.configPath)
	if err != nil {
		return err
	}
	if err = cfg.RequireManagedProxy(s.profile); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(s.operationContext(), 5*time.Minute)
	defer cancel()
	state, err := guestos.WindowsVMResources(ctx, s.profile, 0, 0)
	if err != nil {
		return err
	}
	// An absent resource file selects the default automatic allocation too.
	// Apply it on a cold start after the installation's fixed-memory phase.
	if state.State == "Off" {
		memory, cpus, err := s.configuredWindowsResources()
		if err != nil {
			return err
		}
		if !state.MatchesAllocation(memory, cpus) {
			if _, err = guestos.WindowsVMResources(ctx, s.profile, memory, cpus); err != nil {
				return err
			}
		}
	}
	if err := guestos.WindowsVMAction(ctx, s.profile, "start"); err != nil {
		return err
	}
	s.setMessage("正在等待 Windows 客机启动并连接账户…")
	bootCtx, finishBoot := context.WithTimeout(ctx, 90*time.Second)
	err = waitWindowsBoot(bootCtx, 2*time.Second, s.attachWindowsGuestContext)
	finishBoot()
	if err != nil {
		return err
	}
	proxy, err := managedproxy.Load(s.profile)
	if err != nil {
		return err
	}
	name, err := physicalProxyInterface(s.operationContext())
	if err != nil {
		return err
	}
	bound, _ := managedproxy.BoundInterface(filepath.Join(managedproxy.Dir(s.profile), "config.yaml"))
	if bound != name || !managedproxy.Running(s.operationContext(), s.profile, proxy) {
		if err = s.activateProxy(proxy, cfg); err != nil {
			return err
		}
	}
	return s.checkNetworkIfWanted()
}
func (s *Server) stopWindowsGuest() error {
	s.mu.Lock()
	s.ready = false
	s.mu.Unlock()
	s.terminals.closeAll()
	s.closeWindowsBrowser()
	s.closeWindowsNetwork()
	s.closeProjectShares()
	ctx, cancel := context.WithTimeout(s.operationContext(), 5*time.Minute)
	defer cancel()
	err := guestos.WindowsVMAction(ctx, s.profile, "stop")
	if err == nil {
		s.mu.Lock()
		s.windowsState = windowsGuestState{}
		s.mu.Unlock()
	}
	return err
}
func (s *Server) createWindowsTerminal(input terminalRequest) (*terminalSession, error) {
	return s.createWindowsCLITerminal(input)
}
func (s *Server) windowsCapabilitiesLocked() map[string]any {
	available := map[string]bool{}
	installed := map[string]bool{}
	registered := map[string]bool{}
	fresh := !s.windowsState.ComponentsChecked.IsZero() && time.Since(s.windowsState.ComponentsChecked) <= 30*time.Second
	for _, name := range s.windowsState.Capabilities {
		registered[name] = true
		available[name] = false
	}
	for name, component := range s.windowsState.Components {
		installed[name] = component.Installed
		available[name] = registered[name] && s.ready && s.windowsState.Available && fresh && component.Installed && component.Ready
	}
	for alias, name := range map[string]string{"claude-cli": "cli", "claude-desktop": "desktop", "projects": "project-mount"} {
		available[alias], installed[alias] = available[name], installed[name]
	}
	available["browser"] = available["browser"] && s.windowsState.Browser.Installed && s.windowsState.Browser.NetworkConfigured
	installed["editor"] = s.windowsState.Components["editor"].Installed
	available["editor"] = available["browser"] && installed["editor"] && s.windowsState.Components["editor"].Ready && available["projects"]
	for name, operation := range map[string]string{"desktop-file-chooser": "desktop", "desktop-downloads": "desktop", "phone-browser": "browser", "phone-desktop": "desktop", "legacy-mobile-migration": "cli"} {
		component := s.windowsState.Components[name]
		installed[name], available[name] = component.Installed, registered[operation] && s.ready && s.windowsState.Available && fresh && component.Registered && component.Installed && component.Ready
	}
	phone := s.windowsState.Components["phone-browser"]
	installed["phone-browser"] = phone.Installed && s.windowsState.Browser.PhoneInstalled
	registered["phone-browser"] = registered["browser"] && phone.Supported && phone.Registered
	available["phone-browser"] = available["phone-browser"] && registered["phone-browser"] && installed["phone-browser"] && s.windowsState.Browser.PhoneNetworkConfigured && !s.closing && !s.networkPaused && !s.proxyNetworkLost
	desktop := s.windowsState.Components["phone-desktop"]
	registered["phone-desktop"] = registered["desktop"] && desktop.Supported && desktop.Registered
	available["phone-desktop"] = available["phone-desktop"] && registered["phone-desktop"] && !s.closing && !s.networkPaused && !s.proxyNetworkLost
	gate := s.safety.Gate
	if available["phone-browser"] || available["phone-desktop"] {
		cfg, err := loadConfig(s.configPath)
		now := time.Now()
		open := err == nil && gate != nil && gate.Configuration == cfg.Fingerprint() && gate.Mode == cage.GateOpen && gate.PolicyVerified && !gate.Fault && gate.Until.After(now) && gate.Report != nil && gate.Report.Authorizes(gate.RunID, gate.Configuration, now) == nil
		available["phone-browser"] = available["phone-browser"] && open
		available["phone-desktop"] = available["phone-desktop"] && open
	}
	unsupported := []string{}
	networkMode := "offline"
	if s.safety.Gate != nil && s.safety.Gate.PolicyVerified {
		available["network"] = s.ready && s.windowsNetwork != nil
		networkMode = s.safety.Gate.Mode
	}
	for _, name := range []string{"clipboard", "terminal", "claude-desktop", "browser", "editor", "projects", "network", "claude-cli", "audio", "desktop-file-chooser", "desktop-downloads", "phone-browser", "phone-desktop", "legacy-mobile-migration"} {
		if _, exists := available[name]; !exists {
			available[name] = false
		}
		if !available[name] {
			unsupported = append(unsupported, name)
		}
	}
	components := make(map[string]windowsComponentState, len(s.windowsState.Components)+10)
	for name, component := range s.windowsState.Components {
		if !fresh {
			component.Ready, component.Reason = false, "component_status_stale"
		}
		components[name] = component
	}
	for _, name := range []string{"clipboard", "terminal", "cli", "desktop", "audio", "browser", "editor", "project-mount"} {
		if _, exists := components[name]; !exists {
			components[name] = windowsComponentState{Registered: registered[name], Reason: "component_status_unavailable"}
		}
	}
	if _, exists := components["phone-browser"]; !exists {
		components["phone-browser"] = windowsComponentState{Installed: s.windowsState.Browser.PhoneInstalled, Reason: "component_status_unavailable"}
	}
	for _, name := range []string{"desktop-file-chooser", "desktop-downloads", "phone-desktop", "legacy-mobile-migration"} {
		if _, exists := components[name]; !exists {
			components[name] = windowsComponentState{Reason: "not_implemented"}
		}
	}
	return map[string]any{"registered": registered, "installed": installed, "available": available, "components": components, "unavailable": unsupported, "network": networkMode, "browser_running": s.ready && s.windowsState.Available && fresh && s.windowsState.Browser.Ready}
}
func (s *Server) windowsUnavailable(w http.ResponseWriter) {
	http.Error(w, "Windows 客机尚未接通此功能", http.StatusNotImplemented)
}
