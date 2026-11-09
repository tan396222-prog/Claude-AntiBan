package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/host"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type DesktopOptions struct{ CompleteUpdate, UseExisting bool }
type DesktopResult struct {
	Status     string             `json:"status"`
	URL        string             `json:"url,omitempty"`
	Message    string             `json:"message,omitempty"`
	Legacy     bool               `json:"legacy,omitempty"`
	Controller ControllerIdentity `json:"controller"`
}
type controllerClient struct {
	profile, origin string
	connectionID    string
	record          controlFile
	launch          launchResponse
	client          *http.Client
}

var errControllerUnavailable = errors.New("control service is unavailable")

func openController(profile string) (*controllerClient, error) {
	path := filepath.Join(profile, "control.json")
	if err := host.CheckPrivateFile(path); err != nil {
		if os.IsNotExist(err) {
			return nil, errControllerUnavailable
		}
		return nil, err
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, errControllerUnavailable
	}
	if err != nil {
		return nil, err
	}
	var record controlFile
	if json.Unmarshal(b, &record) != nil || record.Port < 1024 || record.Port > 65535 || len(record.Token) != 64 || record.PID < 1 {
		return nil, errors.New("后台记录无效，请保留现有环境并检查私有配置。")
	}
	jar, _ := cookiejar.New(nil)
	c := &controllerClient{profile: profile, record: record, origin: fmt.Sprintf("http://localhost:%d", record.Port), client: &http.Client{Timeout: 5 * time.Second, Jar: jar, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}}
	return c, nil
}

func connectController(profile string) (*controllerClient, error) {
	c, err := openController(profile)
	if err != nil {
		return nil, err
	}
	record := c.record
	status, err := c.call("POST", "/api/launch", map[string]string{"token": record.Token}, &c.launch)
	if err != nil {
		c.close()
		var networkError *url.Error
		if errors.As(err, &networkError) {
			return nil, errControllerUnavailable
		}
		return nil, err
	}
	if status != http.StatusOK {
		c.close()
		return nil, errors.New("无法认证已有后台，未尝试终止或替换它。")
	}
	u, err := url.Parse(c.launch.URL)
	if err != nil || u.Scheme != "http" || u.Host != fmt.Sprintf("localhost:%d", record.Port) || u.User != nil || u.Fragment == "" {
		c.close()
		return nil, errors.New("后台返回了无效入口，未连接旧工作台。")
	}
	if c.launch.Controller.Protocol != 0 && c.launch.Controller.PID != record.PID {
		c.close()
		return nil, errors.New("后台身份已变化，请重试连接。")
	}
	return c, nil
}
func (c *controllerClient) close() { c.client.CloseIdleConnections() }
func (c *controllerClient) call(method, path string, input, output any) (int, error) {
	return c.callContext(context.Background(), method, path, input, output)
}

func (c *controllerClient) callContext(ctx context.Context, method, path string, input, output any) (int, error) {
	var body []byte
	var err error
	if input != nil {
		body, err = json.Marshal(input)
		if err != nil {
			return 0, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.origin+path, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Origin", c.origin)
	req.Header.Set("Content-Type", "application/json")
	if c.connectionID != "" {
		req.Header.Set(connectionHeader, c.connectionID)
	}
	res, err := c.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusOK && output != nil {
		if err = json.NewDecoder(io.LimitReader(res.Body, 256<<10)).Decode(output); err != nil {
			return res.StatusCode, errors.New("后台响应无效，未继续更新。")
		}
	}
	return res.StatusCode, nil
}
func pendingUpdate(c *controllerClient) DesktopResult {
	return DesktopResult{Status: "upgrade_pending", Legacy: c.launch.Controller.Protocol == 0, Controller: c.launch.Controller, Message: "新版已准备好，当前后台仍在运行。请先打开现有环境保存文件，再选择“保存后停止环境并完成更新”。终端任务会结束，项目和登录状态会保留。"}
}

// Desktop never silently loads a different build's UI. Completing an update is
// an explicit user action because it stops the existing environment's work.
func Desktop(configPath string, options DesktopOptions) (DesktopResult, error) {
	return desktopLaunch(filepath.Dir(configPath), false, options, func() error { return startController(configPath) })
}

func desktopLaunch(profile string, gateway bool, options DesktopOptions, start func() error) (DesktopResult, error) {
	want, err := currentController()
	if err != nil {
		return DesktopResult{}, err
	}
	current, err := connectController(profile)
	if err == nil {
		defer current.close()
		if current.launch.DesktopGateway != gateway {
			return DesktopResult{}, errors.New("此目录已被另一种 Cage 服务使用，请核对配置路径")
		}
		if options.CompleteUpdate {
			if current.launch.DesktopGateway {
				err = current.releaseDesktopEntry()
			} else {
				err = current.stopAndHandoff()
			}
			if err != nil {
				return DesktopResult{}, err
			}
		} else if want.matches(current.launch.Controller) || options.UseExisting {
			status := "ready"
			if !want.matches(current.launch.Controller) {
				status = "existing"
			}
			return DesktopResult{Status: status, URL: current.launch.URL, Controller: current.launch.Controller, Legacy: current.launch.Controller.Protocol == 0}, nil
		} else {
			if current.launch.Controller.Protocol != controlProtocol {
				return pendingUpdate(current), nil
			}
			status, handoffErr := current.call("POST", "/api/handoff", map[string]string{"token": current.record.Token}, nil)
			if handoffErr != nil {
				return DesktopResult{}, handoffErr
			}
			if status != http.StatusOK {
				return pendingUpdate(current), nil
			}
			if err = current.waitForExit(); err != nil {
				return DesktopResult{}, err
			}
		}
	} else if !errors.Is(err, errControllerUnavailable) {
		return DesktopResult{}, err
	}
	if err = start(); err != nil {
		return DesktopResult{}, err
	}
	for i := 0; i < 60; i++ {
		c, readErr := connectController(profile)
		if readErr == nil {
			c.close()
			if c.launch.DesktopGateway != gateway {
				return DesktopResult{}, errors.New("打开期间控制服务身份发生变化，请重新选择")
			}
			if want.matches(c.launch.Controller) {
				return DesktopResult{Status: "ready", URL: c.launch.URL, Controller: c.launch.Controller}, nil
			}
			return pendingUpdate(c), nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return DesktopResult{}, errors.New("工作台未能启动，请查看私有配置中的 control.log 后重试。")
}

// PrepareUpdate is shared by the explicit migration button and the installer.
// It returns only after the old service has released its profile lock.
func PrepareUpdate(configPath string, stop bool) (DesktopResult, error) {
	c, err := connectController(filepath.Dir(configPath))
	if errors.Is(err, errControllerUnavailable) {
		lock, lockErr := os.OpenFile(filepath.Join(filepath.Dir(configPath), "control.lock"), os.O_RDWR, 0600)
		if lockErr == nil {
			defer lock.Close()
			if lockController(lock) != nil {
				return DesktopResult{}, errors.New("后台暂时无法连接且尚未退出，请恢复连接后重试更新。")
			}
			defer unlockController(lock)
		} else if !os.IsNotExist(lockErr) {
			return DesktopResult{}, lockErr
		}
		if controllerHasVM(filepath.Dir(configPath)) {
			return DesktopResult{}, errors.New("后台无法连接，但仍有环境进程。请恢复工作台连接并保存停止后再更新。")
		}
		return DesktopResult{Status: "prepared"}, nil
	}
	if err != nil {
		return DesktopResult{}, err
	}
	defer c.close()
	if stop {
		if err = c.stopAndHandoff(); err != nil {
			return DesktopResult{}, err
		}
		return DesktopResult{Status: "prepared"}, nil
	}
	if c.launch.Controller.Protocol == controlProtocol {
		status, err := c.call("POST", "/api/handoff", map[string]string{"token": c.record.Token}, nil)
		if err != nil {
			return DesktopResult{}, err
		}
		if status == http.StatusOK {
			if err = c.waitForExit(); err != nil {
				return DesktopResult{}, err
			}
			return DesktopResult{Status: "prepared"}, nil
		}
	}
	return DesktopResult{Status: "stop_required", Message: "请保存文件后确认停止环境并安装更新。终端任务会结束，项目和登录状态会保留。"}, nil
}
func (c *controllerClient) stopAndHandoff() error {
	legacy := c.launch.Controller.Protocol == 0
	var owner legacyProcessIdentity
	var err error
	if legacy {
		owner, err = identifyLegacyController(c.profile, c.record)
		if err != nil {
			return err
		}
	} else if c.launch.Controller.Protocol != controlProtocol {
		return errors.New("当前后台协议不受此版本支持，已保留运行环境。请使用匹配的 Cage 版本停止环境。")
	}
	u, _ := url.Parse(c.launch.URL)
	status, err := c.call("POST", "/api/session", map[string]string{"token": u.Fragment}, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return errors.New("无法建立停止会话，请重试。")
	}
	var clientContext ClientContext
	contextStatus, contextErr := c.call("GET", "/api/client/context", nil, &clientContext)
	if contextErr == nil && contextStatus == http.StatusOK && (clientContext.Mode == "remote" || clientContext.ClientOnly) {
		path := "/api/client/local"
		if clientContext.ClientOnly {
			path = "/api/client/disconnect"
		}
		status, err = c.call("POST", path, map[string]string{"connection_id": clientContext.ConnectionID}, nil)
		if err != nil || status != http.StatusOK {
			return errors.New("无法断开本地远程连接，未继续更新")
		}
		if clientContext.ClientOnly {
			status, err = c.call("POST", "/api/handoff", map[string]string{"token": c.record.Token}, nil)
			if err != nil || status != http.StatusOK {
				return errors.New("本地连接服务尚未退出，请稍后重试")
			}
			return c.waitForExit()
		}
	}
	var requested Job
	status, err = c.call("POST", "/api/stop", map[string]any{}, &requested)
	if err != nil {
		return err
	}
	if status != http.StatusOK || requested.ID == "" {
		return errors.New("后台未接受停止请求，已取消本次更新。")
	}
	deadline := time.Now().Add(2 * time.Minute)
	for {
		var state struct {
			VMRunning *bool `json:"vm_running"`
			Job       Job   `json:"job"`
		}
		status, err = c.call("GET", "/api/status", nil, &state)
		if err != nil {
			return err
		}
		if status != http.StatusOK || state.VMRunning == nil || state.Job.ID != requested.ID {
			return errors.New("停止状态发生变化，未终止后台；请确认现有任务后重试。")
		}
		if !state.Job.Running {
			if state.Job.Error != "" || *state.VMRunning {
				return errors.New("环境未能安全停止，未继续更新。请在原工作台查看停止结果。")
			}
			break
		}
		if time.Now().After(deadline) {
			return errors.New("停止环境超时，后台保持运行，请稍后重试更新。")
		}
		time.Sleep(200 * time.Millisecond)
	}
	var terminals []struct {
		Closed bool `json:"closed"`
	}
	status, err = c.call("GET", "/api/terminals", nil, &terminals)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return errors.New("无法确认终端已停止，未继续更新。")
	}
	for _, terminal := range terminals {
		if !terminal.Closed {
			return errors.New("仍有终端运行，未继续更新。")
		}
	}
	if legacy {
		verified, err := identifyLegacyController(c.profile, c.record)
		if err != nil || verified != owner {
			return errors.New("旧后台进程身份已变化，未发送退出信号；请重试更新。")
		}
		var final struct {
			VMRunning *bool `json:"vm_running"`
			Job       Job   `json:"job"`
		}
		status, err = c.call("GET", "/api/status", nil, &final)
		if err != nil || status != http.StatusOK || final.VMRunning == nil || *final.VMRunning || final.Job.ID != requested.ID || final.Job.Running || final.Job.Error != "" || controllerHasVM(c.profile) {
			return errors.New("停止后又有环境工作，未终止旧后台；请保存停止后重试。")
		}
		if err = terminateLegacy(owner.PID); err != nil {
			return errors.New("旧后台未能退出，未替换应用。")
		}
	} else {
		status, err = c.call("POST", "/api/handoff", map[string]string{"token": c.record.Token}, nil)
		if err != nil {
			return err
		}
		if status != http.StatusOK {
			return errors.New("后台仍在执行工作，未继续更新。请保存并停止后重试。")
		}
	}
	return c.waitForExit()
}
func (c *controllerClient) waitForExit() error {
	f, err := os.OpenFile(filepath.Join(c.profile, "control.lock"), os.O_RDWR, 0600)
	if err != nil {
		return errors.New("无法确认后台已退出，未继续更新。")
	}
	defer f.Close()
	for i := 0; i < 100; i++ {
		if lockController(f) == nil {
			_ = unlockController(f)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("后台尚未退出，未强制结束进程。请稍后重试更新。")
}
func startController(configPath string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return startControllerExecutable(configPath, exe)
}

func startControllerExecutable(configPath, exe string, extraEnv ...string) error {
	profile := filepath.Dir(configPath)
	if err := host.EnsurePrivateDir(profile); err != nil {
		return err
	}
	cmd := exec.Command(exe, "log-relay", "controller")
	cmd.Dir = profile
	cmd.Env = append(os.Environ(), cage.ConfigEnv+"="+configPath)
	cmd.Env = append(cmd.Env, extraEnv...)
	detachController(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
