package workbench

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"al.essio.dev/pkg/shellescape"
	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/containerenv"
	"github.com/sylvinus/agent-vm/internal/paths"
	"go.yaml.in/yaml/v3"
)

//go:embed safety_probe.py
var guestSafetyProbe string

type safetyState struct {
	pauseVersion uint64
	monitorError string
	Gate         *cage.GateStatus   `json:"gate,omitempty"`
	Report       *cage.SafetyReport `json:"report,omitempty"`
	Error        string             `json:"error,omitempty"`
	ExitIP       string             `json:"exit_ip,omitempty"`
	VerifiedAt   time.Time          `json:"verified_at,omitempty"`
}

type networkObservation struct {
	Status int    `json:"status"`
	IP     string `json:"ip"`
}
type guestObservation struct {
	TZ                  string             `json:"tz"`
	Lang                string             `json:"lang"`
	LCAll               string             `json:"lc_all"`
	SystemTimezone      string             `json:"system_timezone"`
	LocaleValid         bool               `json:"locale_valid"`
	PublicConnected     bool               `json:"public_connected"`
	ManagementConnected bool               `json:"management_connected"`
	IPv6Connected       bool               `json:"ipv6_connected"`
	IPv6DefaultRoutes   int                `json:"ipv6_default_routes"`
	UDPAttempted        bool               `json:"udp_attempted"`
	PublicDNSAnswered   bool               `json:"public_dns_answered"`
	PrivateFile         string             `json:"private_file"`
	SharedFile          bool               `json:"shared_file"`
	Egress              networkObservation `json:"egress"`
	DNS                 networkObservation `json:"dns"`
}
type browserObservation struct {
	Timezone         string             `json:"timezone"`
	ExpectedTimezone string             `json:"expected_timezone"`
	Language         string             `json:"language"`
	Platform         string             `json:"platform"`
	Network          networkObservation `json:"network"`
}

func (s *Server) gateClient() (*cage.GateClient, error) {
	return cage.OpenGateClient(s.instanceDir())
}

func (s *Server) publishGate(state cage.GateStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.safety.Gate
	if old != nil && old.RunID == state.RunID && old.Epoch > state.Epoch {
		return
	}
	s.safety.Gate = &state
	if state.Mode == cage.GateOpen && state.Report != nil {
		s.safety.Report = state.Report
		// A repeated status poll of the old grant is not a new authorization.
		if old == nil || old.RunID != state.RunID || old.Epoch != state.Epoch {
			s.safety.Error = ""
			s.safety.monitorError = ""
		}
	}
}

// A gate grant resolves a monitoring failure. Independent diagnostics, such
// as a location lookup failure, are cleared by the operation that owns them.
func (s *Server) currentNetworkErrorLocked() string {
	if s.safety.monitorError != "" {
		return s.safety.monitorError
	}
	return s.networkError
}

func (s *Server) publishReport(report cage.SafetyReport) {
	copy := report
	copy.Checks = append([]cage.SafetyCheck(nil), report.Checks...)
	s.mu.Lock()
	s.safety.Report = &copy
	s.mu.Unlock()
}

func (s *Server) safetyCheck(w http.ResponseWriter, r *http.Request) {
	s.launch(w, "safety", s.runSafetyCheck)
}

func (s *Server) pauseNetwork(w http.ResponseWriter, r *http.Request) {
	if err := s.pauseNetworkNow("已手动暂停外网；离线终端和文件仍可使用"); err != nil {
		requestError(w, err)
		return
	}
	jsonReply(w, map[string]bool{"paused": true})
}

func (s *Server) pauseNetworkNow(reason string) error {
	return s.revokeNetworkNow(reason, true)
}

func (s *Server) revokeNetworkNow(reason string, pause bool) error {
	var intentErr error
	s.mu.Lock()
	if pause {
		s.recovery = networkRecovery{}
		s.networkPaused = true
		s.safety.pauseVersion++
		intentErr = s.saveNetworkIntentLocked(true)
	}
	if s.checkCancel != nil {
		s.checkCancel()
	}
	if s.monitorCancel != nil {
		s.monitorCancel()
	}
	s.mu.Unlock()
	s.safetyOp.Lock()
	defer s.safetyOp.Unlock()
	client, err := s.gateClient()
	if err != nil {
		return errors.Join(intentErr, err)
	}
	state, err := client.Close(context.Background(), reason)
	if err == nil {
		s.publishGate(state)
	}
	return errors.Join(intentErr, err)
}

func (s *Server) safetyExport(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	report := s.safety.Report
	s.mu.Unlock()
	if report == nil {
		http.Error(w, "尚无检测报告", 404)
		return
	}
	// Exports cannot authorize networking and contain no proxy hash, address,
	// credentials, launch ticket or host filesystem paths.
	jsonReply(w, map[string]any{"policy": report.Policy, "created_at": report.CreatedAt, "checks": report.Checks, "scope": "采样与运行时策略检查；不代表平台账号资格或完整隔离审计"})
}

// The probe runs in Linux, while its files are created on the host. Use the
// same guest path mapping as the VM's mounts for both sides of the boundary.
func boundaryProbeInput(port int, nonce, privateFile, sharedFile, challenge string) map[string]any {
	return map[string]any{"stage": "boundary", "control_port": port, "nonce": nonce,
		"management_host": "192.168.5.2",
		"private_file":    guestPath(privateFile), "shared_file": guestPath(sharedFile), "challenge": challenge}
}

func (s *Server) guestObservation(ctx context.Context, input map[string]any) (guestObservation, error) {
	var observation guestObservation
	data, err := json.Marshal(input)
	if err != nil {
		return observation, err
	}
	command := "python3 -c " + shellescape.Quote(guestSafetyProbe) + " " + shellescape.Quote(base64.StdEncoding.EncodeToString(data))
	output, err := s.guest(ctx, "exec bash -lc "+shellescape.Quote(command))
	if err != nil || json.Unmarshal(output, &observation) != nil {
		return observation, errors.New("客机检测未返回完整结果")
	}
	return observation, nil
}

func (s *Server) browserObservation(ctx context.Context, cfg cage.Config, network bool) (browserObservation, error) {
	var observation browserObservation
	s.mu.Lock()
	token := s.credentials.Token
	s.mu.Unlock()
	body, _ := json.Marshal(map[string]any{"network": network, "timezone": cfg.Timezone})
	data, err := s.guestAPI(ctx, token, "POST", "/audit", body)
	if err != nil || json.Unmarshal(data, &observation) != nil {
		return observation, errors.New("浏览器检测未返回完整结果，请更新或重启环境")
	}
	return observation, nil
}

func guestSettingsMatch(v guestObservation, cfg cage.Config) bool {
	return v.LocaleValid && v.TZ == cfg.Timezone && v.SystemTimezone == cfg.Timezone && v.Lang == cfg.Locale && v.LCAll == cfg.Locale
}
func browserSettingsMatch(v browserObservation, cfg cage.Config) bool {
	return v.Timezone != "" && v.Timezone == v.ExpectedTimezone && strings.EqualFold(v.Language, cfg.BrowserLanguage) && strings.HasPrefix(v.Platform, "Linux")
}

func (s *Server) inspectVMLayout() (bool, bool) {
	if containerenv.Configured(s.profile) {
		environment, err := containerenv.Load(s.profile)
		if err != nil {
			return false, false
		}
		ctx, cancel := s.controlContext()
		defer cancel()
		inspection, err := environment.Inspect(ctx)
		if err != nil {
			return false, false
		}
		return inspection.FilesystemConfined, inspection.NetworkConfined
	}
	workspace, err := validateWorkspace(s.environmentDir(), s.profile)
	if err != nil {
		return false, false
	}
	data, err := os.ReadFile(filepath.Join(s.profile, "state", "lima", cage.InstanceName(s.profile, workspace), "lima.yaml"))
	if err != nil {
		return false, false
	}
	var spec struct {
		Mounts []struct {
			Location   string `yaml:"location"`
			MountPoint string `yaml:"mountPoint"`
			Writable   bool   `yaml:"writable"`
			SSHFS      struct {
				Driver        string   `yaml:"sftpDriver"`
				ReadonlyNames []string `yaml:"readonlyNames"`
			} `yaml:"sshfs"`
		} `yaml:"mounts"`
		Networks  []any  `yaml:"networks"`
		MountType string `yaml:"mountType"`
	}
	if yaml.Unmarshal(data, &spec) != nil {
		return false, false
	}
	shares := len(spec.Mounts) == 1 && spec.MountType == "reverse-sshfs"
	if shares {
		m := spec.Mounts[0]
		shares = paths.Equal(paths.Host(m.Location), paths.Host(workspace)) && m.MountPoint == guestPath(workspace) && m.Writable && m.SSHFS.Driver == "builtin"
		gitProtected := false
		for _, name := range m.SSHFS.ReadonlyNames {
			if name == ".git" {
				gitProtected = true
			}
		}
		shares = shares && gitProtected
	}
	return shares, len(spec.Networks) == 0
}

func (s *Server) managementRequiresSession(ctx context.Context) bool {
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}
	defer client.CloseIdleConnections()
	req, _ := http.NewRequestWithContext(ctx, "GET", s.origin+"/api/settings", nil)
	response, err := client.Do(req)
	if err != nil {
		return false
	}
	response.Body.Close()
	return response.StatusCode == http.StatusUnauthorized
}

// A location is cached by its measured address, never borrowed from the last
// proxy request. The provider's free quota makes per-heartbeat lookups wasteful.
func (s *Server) locationForIP(ctx context.Context, cfg cage.Config, ip string) (Location, error) {
	s.mu.Lock()
	loc, ok := s.locations[ip]
	if !ok && s.location != nil && s.location.IP == ip {
		loc, ok = *s.location, true
	}
	s.mu.Unlock()
	if ok && time.Since(loc.CheckedAt) < time.Hour && loc.CountryCode != "" {
		return loc, nil
	}
	loc, err := probeLocationForIP(ctx, cfg, ip)
	if err != nil {
		return Location{}, err
	}
	s.mu.Lock()
	if s.locations == nil {
		s.locations = map[string]Location{}
	}
	if len(s.locations) >= 64 {
		for key, value := range s.locations {
			if time.Since(value.CheckedAt) >= time.Hour {
				delete(s.locations, key)
			}
		}
		if len(s.locations) >= 64 {
			for key := range s.locations {
				delete(s.locations, key)
				break
			}
		}
	}
	s.locations[ip] = loc
	s.mu.Unlock()
	return loc, nil
}

func (s *Server) checkExitPolicy(ctx context.Context, cfg cage.Config, ip string) (cage.CheckState, string) {
	policy := cfg.ExitPolicy.Normalized()
	if policy.Mode == "static" || policy.Scope == "any" {
		return policy.Evaluate(ip, nil)
	}
	loc, err := s.locationForIP(ctx, cfg, ip)
	if err != nil {
		return cage.CheckUnknown, "出口定位暂时不可用，无法确认允许的地区范围"
	}
	observed := loc.exitLocation()
	state, message := policy.Evaluate(ip, &observed)
	s.mu.Lock()
	s.location = &loc
	s.mu.Unlock()
	return state, message
}

func (s *Server) runSafetyCheck() (returned error) {
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.networkPaused {
			returned = context.Canceled
			s.safety.Error = ""
		}
	}()
	if err := s.attach(); err != nil {
		return err
	}
	cfg, err := loadConfig(s.configPath)
	if err != nil {
		return err
	}
	if err = cfg.RequireManagedProxy(s.profile); err != nil {
		_ = s.closeProxyGate(err.Error())
		return err
	}
	client, err := s.gateClient()
	if err != nil {
		s.mu.Lock()
		s.safety.Error = err.Error()
		s.mu.Unlock()
		return err
	}
	ctx, cancel := context.WithTimeout(s.operationContext(), 170*time.Second)
	defer cancel()
	s.safetyOp.Lock()
	s.mu.Lock()
	paused := s.networkPaused
	if !paused {
		s.checkCancel = cancel
	}
	s.mu.Unlock()
	if paused {
		s.safetyOp.Unlock()
		return errors.New("外网已手动暂停；可重新检测以恢复联网")
	}
	defer func() { s.mu.Lock(); s.checkCancel = nil; s.mu.Unlock() }()
	state, err := client.BeginCheck(ctx)
	if err == nil {
		s.publishGate(state)
	}
	s.safetyOp.Unlock()
	if err != nil {
		_, _ = client.Close(context.Background(), "检测未启动，外网保持关闭")
		return err
	}
	granted := false
	defer func() {
		s.safetyOp.Lock()
		defer s.safetyOp.Unlock()
		s.mu.Lock()
		paused := s.networkPaused
		s.mu.Unlock()
		if !granted && !paused {
			closed, e := client.Close(context.Background(), "关键检查未通过或证据不完整，外网保持关闭")
			if e == nil {
				s.publishGate(closed)
			}
		}
		s.mu.Lock()
		if returned != nil {
			s.safety.Error = returned.Error()
		} else {
			s.safety.Error = ""
		}
		s.mu.Unlock()
	}()
	report := cage.SafetyReport{Policy: cage.SafetyPolicyVersion, ID: randomToken()[:32], RunID: state.RunID, CheckID: state.CheckID, Configuration: cfg.Fingerprint(), CreatedAt: time.Now()}
	for _, item := range [][2]string{{"egress_policy", "环境外出站约束"}, {"proxy_transport", "终端检测通道"}, {"dns_transport", "DNS 与解析约束"}, {"guest_settings", "系统时区与语言"}, {"browser_settings", "浏览器时区与语言"}, {"filesystem_boundary", "共享文件与宿主私有目录"}, {"management_boundary", "宿主管理接口"}, {"fail_closed", "代理故障关闭"}, {"udp_policy", "UDP 出站约束"}, {"ipv6_policy", "IPv6 出站约束"}, {"exit_consistency", "出口符合已保存规则"}} {
		report.Checks = append(report.Checks, cage.SafetyCheck{ID: item[0], Title: item[1], Critical: true, State: cage.CheckUnknown, Evidence: "尚未取得证据", Remedy: "完成检测后查看结果；未知项目不会自动通过", CheckedAt: report.CreatedAt})
	}
	set := func(id string, result cage.CheckState, evidence, remedy string) {
		for i := range report.Checks {
			if report.Checks[i].ID == id {
				report.Checks[i].State = result
				report.Checks[i].Evidence = evidence
				report.Checks[i].Remedy = remedy
				report.Checks[i].CheckedAt = time.Now()
			}
		}
		s.publishReport(report)
	}
	s.publishReport(report)
	shares, networkLayout := s.inspectVMLayout()
	private, err := os.CreateTemp(s.profile, ".cage-private-probe-*")
	if err != nil {
		return err
	}
	defer os.Remove(private.Name())
	defer private.Close()
	shared, err := os.CreateTemp(s.environmentDir(), ".cage-share-probe-*")
	if err != nil {
		return err
	}
	defer os.Remove(shared.Name())
	defer shared.Close()
	challenge := randomToken()
	if _, err = shared.WriteString(challenge); err != nil {
		return err
	}
	if err = shared.Sync(); err != nil {
		return err
	}
	control, _ := url.Parse(s.origin)
	port, _ := strconv.Atoi(control.Port())
	s.setMessage("正在验证离线约束、共享文件与系统配置…")
	boundaryInput := boundaryProbeInput(port, report.ID, private.Name(), shared.Name(), challenge)
	if containerenv.Configured(s.profile) {
		boundaryInput["management_host"] = containerenv.GatewayIP
	}
	boundary, boundaryErr := s.guestObservation(ctx, boundaryInput)
	after, err := client.Status(ctx)
	if err != nil {
		return err
	}
	if boundaryErr == nil {
		if guestSettingsMatch(boundary, cfg) {
			set("guest_settings", cage.CheckPassed, "登录终端的时区、LANG、LC_ALL 与系统时区均符合已应用配置", "")
		} else {
			set("guest_settings", cage.CheckFailed, "系统读回结果与已应用配置不一致", "重新应用时区与语言后重启环境")
		}
		_, _ = shared.Seek(0, io.SeekStart)
		sharedData, _ := io.ReadAll(io.LimitReader(shared, 256))
		if shares && boundary.PrivateFile == "unreachable" && boundary.SharedFile && string(sharedData) == challenge+":guest" {
			set("filesystem_boundary", cage.CheckPassed, "共享读写探针往返成功；宿主私有目录探针不可读；环境基础共享目录与明确打开的项目使用受目录约束的传输，并保护 Git 元数据", "")
		} else {
			set("filesystem_boundary", cage.CheckFailed, "共享读写、私有目录探针或挂载范围未符合预期", "检查共享目录；不要共享主目录或 Cage 配置目录")
		}
		if after.PolicyVerified && networkLayout && after.Configuration == cfg.Fingerprint() && !boundary.PublicConnected && after.Counters.TCPDenied > state.Counters.TCPDenied {
			set("egress_policy", cage.CheckPassed, "运行中策略检查符合预期；未配置额外网卡；检查期间的公网 TCP 请求被 环境外网络层拒绝", "")
		}
		if !boundary.ManagementConnected && after.Counters.PrivateDenied > state.Counters.PrivateDenied && s.managementRequiresSession(ctx) {
			set("management_boundary", cage.CheckPassed, "guest 到宿主管理端口的连接被拒绝；本机接口无会话访问被拒绝", "")
		} else {
			result := cage.CheckUnknown
			if boundary.ManagementConnected {
				result = cage.CheckFailed
			}
			set("management_boundary", result, "管理接口拒绝访问的证据不完整", "停止环境并重新启动；不要开放宿主控制端口")
		}
		if boundary.UDPAttempted && after.Counters.UDPDenied > state.Counters.UDPDenied && after.PolicyVerified {
			set("udp_policy", cage.CheckPassed, "客机 UDP 探针已发起，环境外网络层记录了拒绝；无响应本身不作为通过依据", "")
		}
		if !boundary.IPv6Connected && after.PolicyVerified && (boundary.IPv6DefaultRoutes == 0 || after.Counters.IPv6Denied > state.Counters.IPv6Denied) {
			set("ipv6_policy", cage.CheckPassed, "客机公网 IPv6 策略为拒绝；连接采样未建立，路由或拒绝记录与策略一致。当前版本验证 IPv4 出口", "")
		}
	}
	s.setMessage("正在通过固定检测通道检查代理与 DNS…")
	network, networkErr := s.guestObservation(ctx, map[string]any{"stage": "network"})
	normalState, err := client.Status(ctx)
	if err != nil {
		return err
	}
	if networkErr == nil && network.Egress.Status == 200 && network.Egress.IP != "" && normalState.Counters.ProbeTCP > after.Counters.ProbeTCP {
		set("proxy_transport", cage.CheckPassed, "终端请求经过 环境网络层的固定检测通道，并由指定代理完成 HTTPS 出口查询；未放开任意网站", "")
	} else {
		set("proxy_transport", cage.CheckUnknown, "终端未能完成固定检测请求，或缺少 环境外通道记录", "检查指定代理和环境组件，然后重新检测")
	}
	if networkErr == nil && boundaryErr == nil && !boundary.PublicDNSAnswered && after.Counters.DNSDenied > state.Counters.DNSDenied && network.DNS.Status == 200 && normalState.Counters.ProbeDNS > after.Counters.ProbeDNS {
		set("dns_transport", cage.CheckPassed, "检查期间公共解析被拒绝；固定 DNS 查询经指定 DoH 与代理完成", "")
	} else {
		set("dns_transport", cage.CheckUnknown, "公共解析拒绝记录或加密 DNS 查询证据不完整", "检查 DNS 配置、代理连通性与环境组件")
	}
	s.setMessage("正在读取隔离浏览器的实际配置…")
	browser, browserErr := s.browserObservation(ctx, cfg, true)
	if browserErr == nil {
		if browserSettingsMatch(browser, cfg) {
			set("browser_settings", cage.CheckPassed, "隐藏诊断页面读到 Linux 浏览器；实际时区与首选语言符合已应用配置", "")
		} else {
			set("browser_settings", cage.CheckFailed, "浏览器实际时区、语言或系统标识与配置不一致", "重新应用设置并重启环境")
		}
	} else {
		set("browser_settings", cage.CheckUnknown, browserErr.Error(), "停止并重启环境以加载最新浏览器组件，再重新检测")
	}
	s.setMessage("正在演练代理失败；现有代理进程保持运行…")
	faultStart, err := client.SetFault(ctx, true)
	if err != nil {
		return err
	}
	fault, faultErr := s.guestObservation(ctx, map[string]any{"stage": "network"})
	faultBrowser, faultBrowserErr := s.browserObservation(ctx, cfg, true)
	faultEnd, err := client.Status(ctx)
	if err != nil {
		return err
	}
	if faultErr == nil && faultBrowserErr == nil && fault.Egress.Status == 503 && fault.DNS.Status == 503 && faultBrowser.Network.Status == 503 && faultEnd.Counters.FaultTCP >= faultStart.Counters.FaultTCP+2 && faultEnd.Counters.FaultDNS > faultStart.Counters.FaultDNS {
		set("fail_closed", cage.CheckPassed, "在检测专用连接中注入代理不可用：终端、浏览器和 DNS 均失败，未改为直连；未停止用户的代理进程", "")
	} else {
		set("fail_closed", cage.CheckUnknown, "故障演练缺少终端、浏览器或 DNS 的失败记录", "修复检测通道或浏览器组件后重试；未知不等于已验证安全")
	}
	if _, err = client.SetFault(ctx, false); err != nil {
		return err
	}
	s.setMessage("正在核对静态 IP 或动态地区范围…")
	hostIP, hostErr := cage.ProbeExitIP(ctx, cfg)
	result, message := cage.CheckPassed, "终端、浏览器与宿主检测请求均符合已保存的出口规则；此结论限于检测采样"
	if hostErr != nil || networkErr != nil || browserErr != nil || network.Egress.Status != 200 || browser.Network.Status != 200 {
		result, message = cage.CheckUnknown, "出口采样不完整，无法确认配置范围"
	} else {
		for _, ip := range []string{hostIP, network.Egress.IP, browser.Network.IP} {
			observed, why := s.checkExitPolicy(ctx, cfg, ip)
			if observed == cage.CheckFailed {
				result, message = observed, why
				break
			}
			if observed == cage.CheckUnknown {
				result, message = observed, why
			}
		}
	}
	set("exit_consistency", result, message, "在网络设置中核对静态 IP 或允许地区；已有基准不会随重检改变")
	report.Checks = append(report.Checks, cage.SafetyCheck{ID: "third_party_reputation", Title: "第三方 IP 信誉", State: cage.CheckUnknown, CheckedAt: time.Now(), Evidence: "未自动获取社区评分。可在 Cage 浏览器中打开补充检查；结果不替代底层隔离证据。"})
	s.mu.Lock()
	autoLanguage, location := s.preferences.AutoLanguage, s.location
	s.mu.Unlock()
	if autoLanguage && (location == nil || location.SuggestedLocale == "") {
		report.Checks = append(report.Checks, cage.SafetyCheck{ID: "language_suggestion", Title: "地区语言建议", State: cage.CheckUnknown, CheckedAt: time.Now(), Evidence: "当前地区没有可用的自动语言建议，已保留配置中的语言。", Remedy: "可在时区与语言设置中关闭自动，并手动选择"})
	}
	s.publishReport(report)
	s.mu.Lock()
	s.safety.ExitIP = hostIP
	s.mu.Unlock()
	_ = writePrivate(filepath.Join(s.profile, "last-safety-report.json"), report)
	if err = report.Authorizes(state.RunID, cfg.Fingerprint(), time.Now()); err != nil {
		return err
	}
	s.safetyOp.Lock()
	defer s.safetyOp.Unlock()
	s.mu.Lock()
	paused = s.networkPaused
	s.mu.Unlock()
	if paused || ctx.Err() != nil {
		return errors.New("检测已取消，外网保持关闭")
	}
	state, err = client.Grant(ctx, report)
	if err != nil {
		return err
	}
	granted = true
	s.publishGate(state)
	s.mu.Lock()
	s.safety.VerifiedAt = time.Now()
	s.mu.Unlock()
	return nil
}

// Monitoring renews a short host-owned lease without replaying the disruptive
// failure drill. Lost supervision eventually closes existing guest connections.
func (s *Server) networkMonitorBlockedLocked() bool {
	job := s.jobs.snapshot()
	return s.closing || s.networkPaused || (job.Running && job.Kind != "environment-tools" && job.Kind != "claude-desktop-install")
}

func (s *Server) monitorSafety() {
	s.mu.Lock()
	ready, busy, last := s.ready, s.networkMonitorBlockedLocked(), s.safety.VerifiedAt
	s.mu.Unlock()
	if !ready || busy || time.Since(last) < 30*time.Second {
		return
	}
	client, err := s.gateClient()
	if err != nil {
		s.mu.Lock()
		s.safety.Gate = nil
		s.safety.Error = err.Error()
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithTimeout(s.lifetimeContext(), 45*time.Second)
	s.mu.Lock()
	s.monitorCancel = cancel
	s.mu.Unlock()
	defer func() { cancel(); s.mu.Lock(); s.monitorCancel = nil; s.mu.Unlock() }()
	state, err := client.Status(ctx)
	if err != nil {
		s.mu.Lock()
		s.safety.Gate = nil
		s.safety.Error = err.Error()
		s.mu.Unlock()
		return
	}
	s.publishGate(state)
	if state.Mode != cage.GateOpen || state.Report == nil {
		if state.Mode == cage.GateClosed && state.Reason == "检查或监测授权已到期" && state.Report != nil {
			s.mu.Lock()
			pending := s.recovery.RunID != ""
			s.mu.Unlock()
			if !pending {
				s.scheduleNetworkRecovery(state)
			}
		}
		return
	}
	var issue string
	var recoverable bool
	cfg, err := loadConfig(s.configPath)
	if err != nil || cfg.Fingerprint() != state.Configuration {
		issue = "配置与运行环境不一致，请应用设置并重新检测"
	}
	if issue == "" {
		if err = state.Report.Authorizes(state.RunID, state.Configuration, time.Now()); err != nil {
			issue = err.Error()
		}
	}
	var ip string
	if issue == "" {
		ip, err = cage.ProbeExitIP(ctx, cfg)
		if err != nil {
			issue = "当前无法经指定代理确认出口，外网已暂停"
			recoverable = true
		} else {
			result, message := s.checkExitPolicy(ctx, cfg, ip)
			if result != cage.CheckPassed {
				issue = message
			}
		}
	}
	if issue == "" {
		guest, e := s.guestObservation(ctx, map[string]any{"stage": "environment"})
		if e != nil || !guestSettingsMatch(guest, cfg) {
			issue = "系统时区或语言的运行状态无法确认，请重新检测"
			recoverable = e != nil
		}
	}
	if issue == "" {
		browser, e := s.browserObservation(ctx, cfg, false)
		if e != nil || !browserSettingsMatch(browser, cfg) {
			issue = "浏览器时区或语言的运行状态无法确认，请重新检测"
			recoverable = e != nil
		}
	}
	s.finishSafetyObservation(ctx, client, state, ip, issue, recoverable)
}

// Recheck the user operation and configuration at the actual lease write.
func (s *Server) finishSafetyMonitor(ctx context.Context, client *cage.GateClient, state cage.GateStatus, ip, issue string) {
	s.finishSafetyObservation(ctx, client, state, ip, issue, false)
}

func (s *Server) finishSafetyObservation(ctx context.Context, client *cage.GateClient, state cage.GateStatus, ip, issue string, recoverable bool) {
	s.safetyOp.Lock()
	defer s.safetyOp.Unlock()
	s.mu.Lock()
	busy := s.networkMonitorBlockedLocked()
	s.mu.Unlock()
	if busy {
		return
	} // A user operation cancelled this monitor; it owns the next transition.
	current, e := loadConfig(s.configPath)
	if e != nil || current.Fingerprint() != state.Configuration {
		issue = "运行配置已变化，请重新应用设置"
		recoverable = false
	}
	if issue != "" {
		closed, e := client.Close(context.Background(), issue)
		if e == nil {
			s.publishGate(closed)
			if recoverable {
				s.scheduleNetworkRecovery(closed)
			}
		}
		s.mu.Lock()
		s.safety.Error = issue
		s.safety.ExitIP = ip
		s.safety.monitorError = issue
		s.mu.Unlock()
		return
	}
	renewed, err := client.Renew(ctx, state.Report.ID)
	if err != nil {
		closed, e := client.Close(context.Background(), "运行监测未能续期，请重新检测")
		if e == nil {
			s.publishGate(closed)
			s.scheduleNetworkRecovery(closed)
		}
		s.mu.Lock()
		s.safety.Error = "运行监测未能续期，请重新检测"
		s.mu.Unlock()
		return
	}
	s.publishGate(renewed)
	s.mu.Lock()
	s.safety.VerifiedAt = time.Now()
	s.safety.Error = ""
	s.safety.monitorError = ""
	s.safety.ExitIP = ip
	s.mu.Unlock()
}
