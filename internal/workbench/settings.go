// Package workbench owns the trusted host UI; guest web content never runs in it.
package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/host"
	"github.com/sylvinus/agent-vm/internal/mounts"
	"github.com/sylvinus/agent-vm/internal/paths"
)

type Preferences struct {
	Workspace             string `json:"workspace"`
	BrowserHomeURL        string `json:"browser_home_url"`
	ClaudeTerminalCommand string `json:"claude_terminal_command"`
	Keyboard              string `json:"keyboard"`
	AutoTimezone          bool   `json:"auto_timezone"`
	AutoLanguage          bool   `json:"auto_language"`
}

const defaultBrowserHomeURL = "https://www.bing.com/"

func claudeTerminalCommand(p Preferences) string {
	if strings.TrimSpace(p.ClaudeTerminalCommand) == "" {
		return "claude"
	}
	return strings.TrimSpace(p.ClaudeTerminalCommand)
}

func browserHomeURL(p Preferences) string {
	if p.BrowserHomeURL == "" {
		return defaultBrowserHomeURL
	}
	return p.BrowserHomeURL
}

// The environment workspace is separate from the private control profile.
func environmentWorkspace(profile string) string { return cage.EnvironmentDir(profile) }

func guestPath(path string) string { return paths.Guest(paths.Host(path)) }

func (s *Server) prepareEnvironment() error {
	path := environmentWorkspace(s.profile)
	// Promote an existing installation's VM once, preserving its disk and login state.
	if _, err := os.Stat(filepath.Join(s.profile, "environment.json")); os.IsNotExist(err) {
		previous := s.workspace()
		if previous != "" {
			if _, err := os.Stat(filepath.Join(s.profile, "state", "lima", cage.InstanceName(s.profile, previous), "lima.yaml")); err == nil {
				path = previous
			}
		}
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	path, err := validateWorkspace(path, s.profile)
	if err != nil {
		return err
	}
	if err = writePrivate(filepath.Join(s.profile, "environment.json"), map[string]string{"directory": path}); err != nil {
		return err
	}
	s.mu.Lock()
	p := s.preferences
	s.mu.Unlock()
	if p.Workspace == "" {
		p.Workspace = path
	}
	if err := writePrivate(filepath.Join(s.profile, "workbench.json"), p); err != nil {
		return err
	}
	s.mu.Lock()
	s.preferences = p
	s.mu.Unlock()
	return nil
}

type Settings struct {
	Preferences
	DNSURL          string          `json:"dns_url"`
	Timezone        string          `json:"timezone"`
	Locale          string          `json:"locale"`
	BrowserLanguage string          `json:"browser_language"`
	ExitPolicy      cage.ExitPolicy `json:"exit_policy"`
}
type Location struct {
	IP                       string    `json:"ip"`
	Country                  string    `json:"country"`
	CountryCode              string    `json:"country_code"`
	Region                   string    `json:"region"`
	RegionCode               string    `json:"region_code,omitempty"`
	SuggestedLocale          string    `json:"suggested_locale,omitempty"`
	SuggestedBrowserLanguage string    `json:"suggested_browser_language,omitempty"`
	City                     string    `json:"city"`
	Timezone                 string    `json:"timezone"`
	CheckedAt                time.Time `json:"checked_at"`
}
type savedProfile struct {
	Config      cage.Config `json:"config"`
	Preferences Preferences `json:"preferences"`
}

func writePrivate(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err = host.EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".cage-write-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = host.ProtectFile(f.Name()); err == nil {
		_, err = f.Write(append(b, '\n'))
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
func settingsView(c cage.Config, p Preferences) Settings {
	p.BrowserHomeURL = browserHomeURL(p)
	p.ClaudeTerminalCommand = claudeTerminalCommand(p)
	return Settings{Preferences: p, DNSURL: c.DNSURL, Timezone: c.Timezone, Locale: c.Locale, BrowserLanguage: c.BrowserLanguage, ExitPolicy: c.ExitPolicy.Normalized()}
}
func proposedSettings(input Settings, old cage.Config, profile string) (cage.Config, Preferences, error) {
	if input.ExitPolicy == (cage.ExitPolicy{}) {
		input.ExitPolicy = old.ExitPolicy
	}
	c := cage.Config{ProxyURL: old.ProxyURL, ProxyRevision: old.ProxyRevision, DNSURL: input.DNSURL, Timezone: input.Timezone, Locale: input.Locale, BrowserLanguage: input.BrowserLanguage, ExitPolicy: input.ExitPolicy.Normalized()}
	if err := c.Validate(); err != nil {
		return c, input.Preferences, err
	}
	if input.Keyboard != "mac" && input.Keyboard != "native" {
		return c, input.Preferences, errors.New("请选择 Mac 或原生 Linux 按键习惯")
	}
	p := input.Preferences
	if len(p.ClaudeTerminalCommand) > 4096 || strings.ContainsAny(p.ClaudeTerminalCommand, "\x00\r\n") {
		return c, p, errors.New("Claude 终端命令必须为一行，且不超过 4096 字节")
	}
	p.ClaudeTerminalCommand = claudeTerminalCommand(p)
	p.BrowserHomeURL = strings.TrimSpace(browserHomeURL(p))
	home, e := url.Parse(p.BrowserHomeURL)
	if e != nil || home == nil || (home.Scheme != "https" && home.Scheme != "http") || home.Hostname() == "" || home.User != nil {
		return c, p, errors.New("浏览器默认网站必须是完整的 HTTP 或 HTTPS 网址，且不能包含用户名或密码")
	}
	if p.Workspace != "" {
		workspace, err := validateWorkspace(p.Workspace, profile)
		if err != nil {
			return c, p, err
		}
		p.Workspace = workspace
	}
	return c, p, nil
}
func validateWorkspace(path, profile string) (string, error) {
	home, _ := os.UserHomeDir()
	if strings.HasPrefix(path, "~/") {
		path = filepath.Join(home, path[2:])
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", errors.New("项目路径无效")
	}
	abs, err = paths.Resolve(abs)
	if err != nil {
		return "", errors.New("项目目录不存在或无法访问")
	}
	st, err := os.Stat(abs)
	if err != nil || !st.IsDir() {
		return "", errors.New("请选择一个项目文件夹")
	}
	if realHome, err := paths.Resolve(home); err == nil {
		home = realHome
	}
	if executable, e := os.Executable(); e == nil {
		if real, e := paths.Resolve(executable); e == nil && cage.StorageContains(abs, real) {
			return "", errors.New("不能共享正在运行的工作台程序所在目录。请将安装版 Cage 放在项目外，并重启控制服务后选择此项目。")
		}
	}
	if mounts.UnderName(filepath.ToSlash(abs), nil) {
		return "", errors.New("请选择项目根目录，不要共享 Git 等版本控制元数据目录")
	}
	if paths.Equal(abs, home) || filepath.Dir(abs) == abs {
		return "", errors.New("请选择具体项目，不要共享主目录、Cage 配置或包含配置的上级目录")
	}
	private, err := privateProjectDirectories(profile)
	if err != nil {
		return "", err
	}
	for _, dir := range private {
		if cage.StoragePathsOverlap(abs, dir) {
			return "", errors.New("Cage 配置、环境存储、迁移备份及其上级目录不能共享给虚拟机")
		}
	}
	return abs, nil
}

// probeLocation uses the same fail-closed transport as the VM. No host resolver,
// environment proxy, or direct fallback participates, including redirects.
func probeLocation(ctx context.Context, c cage.Config) (Location, error) {
	ip, err := cage.ProbeExitIP(ctx, c)
	if err != nil {
		return Location{}, err
	}
	loc, err := probeLocationForIP(ctx, c, ip)
	if err != nil {
		return Location{IP: ip, CheckedAt: time.Now()}, err
	}
	return loc, nil
}

// Manual timezone/language and an IP-only exit rule need no geolocation.
// The proxy's exit IP is still measured and checked against the saved rule.
func probeEnvironmentLocation(ctx context.Context, c cage.Config, p Preferences) (Location, error) {
	policy := c.ExitPolicy.Normalized()
	if p.AutoTimezone || p.AutoLanguage || (policy.Mode != "static" && policy.Scope != "any") {
		return probeLocation(ctx, c)
	}
	ip, err := cage.ProbeExitIP(ctx, c)
	if err != nil {
		return Location{}, err
	}
	return Location{IP: ip, CheckedAt: time.Now()}, nil
}

func (l Location) exitLocation() cage.ExitLocation {
	return cage.ExitLocation{IP: l.IP, CountryCode: l.CountryCode, Region: l.Region, City: l.City}
}

func probeLocationForIP(ctx context.Context, c cage.Config, ip string) (Location, error) {
	address := net.ParseIP(ip)
	if address == nil {
		return Location{}, errors.New("出口地址无效")
	}
	e, err := cage.NewEgress(c)
	if err != nil {
		return Location{}, err
	}
	transport := &http.Transport{Proxy: nil, DialContext: e.DialContext, ForceAttemptHTTP2: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}
	return lookupLocation(ctx, client, address)
}
