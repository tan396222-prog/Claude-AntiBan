package workbench

import (
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/host"
)

// The entry and the local workload must never share a controller lock,
// shutdown hook or service cookie, even with an explicitly selected profile.
func desktopConfigPath(fullConfig string) string {
	return filepath.Join(filepath.Dir(fullConfig), "desktop-entry", "config.json")
}

// DesktopHelperConfig routes native calls that captured a selected connection.
// Plain CLI operations and older clients using the legacy "local" identity
// continue to address the original workload profile.
func DesktopHelperConfig(fullConfig, connection string) string {
	if connection != "" && connection != "local" {
		entry := desktopConfigPath(fullConfig)
		if _, err := os.Lstat(filepath.Join(filepath.Dir(entry), "control.json")); !os.IsNotExist(err) {
			return entry
		}
	}
	return fullConfig
}

func DesktopEntry(fullConfig string, opts DesktopOptions) (DesktopResult, error) {
	// Explicit legacy recovery opens its own already-authenticated UI so the
	// user can save active work before switching backend versions.
	if opts.UseExisting {
		c, err := connectController(filepath.Dir(fullConfig))
		if err == nil {
			defer c.close()
			want, err := currentController()
			if err != nil {
				return DesktopResult{}, err
			}
			if !want.matches(c.launch.Controller) {
				return DesktopResult{Status: "existing", URL: c.launch.URL, Controller: c.launch.Controller, Legacy: c.launch.Controller.Protocol == 0}, nil
			}
		} else if !errors.Is(err, errControllerUnavailable) {
			return DesktopResult{}, err
		}
	}
	if opts.CompleteUpdate {
		if _, err := PrepareDesktopUpdate(fullConfig, true); err != nil {
			return DesktopResult{}, err
		}
		opts.CompleteUpdate = false
	}
	profile := filepath.Dir(desktopConfigPath(fullConfig))
	owner := 0
	if value := os.Getenv("CAGE_DESKTOP_OWNER_PID"); value != "" {
		var err error
		if owner, err = strconv.Atoi(value); err != nil || owner < 2 {
			return DesktopResult{}, errors.New("桌面窗口进程标识无效")
		}
	}
	result, err := desktopLaunch(profile, true, opts, func() error {
		if err := host.EnsurePrivateDir(profile); err != nil {
			return err
		}
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		cmd := exec.Command(exe, "serve-desktop")
		cmd.Dir = profile
		cmd.Env = append(os.Environ(), cage.ConfigEnv+"="+fullConfig)
		detachController(cmd)
		if err := cmd.Start(); err != nil {
			return err
		}
		return cmd.Process.Release()
	})
	if err != nil || owner == 0 || result.URL == "" {
		return result, err
	}
	c, err := connectController(profile)
	if err != nil {
		return DesktopResult{}, err
	}
	defer c.close()
	if !c.launch.DesktopGateway || c.launch.Controller.PID != result.Controller.PID {
		return DesktopResult{}, errors.New("桌面连接服务身份不匹配")
	}
	code, err := c.call("POST", "/api/desktop-owner", map[string]any{"token": c.record.Token, "pid": owner}, nil)
	if err != nil || code != http.StatusOK {
		return DesktopResult{}, errors.New("无法关联桌面窗口和连接服务")
	}
	return result, nil
}

func (s *Server) registerDesktopOwner(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Token string `json:"token"`
		PID   int    `json:"pid"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	if !equalSecret(input.Token, s.token) || input.PID < 2 || !processExists(input.PID) {
		http.Error(w, "桌面窗口身份无效", http.StatusForbidden)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		http.Error(w, "桌面入口正在退出", http.StatusConflict)
		return
	}
	if s.windowOwners == nil {
		s.windowOwners = make(map[int]bool)
	}
	s.windowOwners[input.PID] = true
	jsonReply(w, map[string]bool{"ok": true})
}

func (s *Server) watchDesktopOwners(done <-chan struct{}) {
	// The native process also supplies its PID to a newly launched gateway.
	// This covers a window killed before it can finish registering via HTTP.
	initial, _ := strconv.Atoi(os.Getenv("CAGE_DESKTOP_OWNER_PID"))
	if initial > 1 {
		s.mu.Lock()
		if s.windowOwners == nil {
			s.windowOwners = make(map[int]bool)
		}
		s.windowOwners[initial] = true
		s.mu.Unlock()
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			s.mu.Lock()
			hadOwners := len(s.windowOwners) > 0
			for pid := range s.windowOwners {
				if !processExists(pid) {
					delete(s.windowOwners, pid)
				}
			}
			exit := hadOwners && len(s.windowOwners) == 0
			if exit {
				s.closing = true
			}
			s.mu.Unlock()
			if exit {
				s.shutdown()
				return
			}
		}
	}
}

func (s *Server) handoffEntry(w http.ResponseWriter, r *http.Request) {
	if !s.launcherAuthenticated(w, r) {
		return
	}
	jsonReply(w, map[string]bool{"stopping": true})
	_ = http.NewResponseController(w).Flush()
	go s.shutdown()
}

func (c *controllerClient) releaseDesktopEntry() error {
	if !c.launch.DesktopGateway {
		return errors.New("无法核对桌面连接服务身份")
	}
	status, err := c.call("POST", "/api/handoff", map[string]string{"token": c.record.Token}, nil)
	if err != nil || status != http.StatusOK {
		return errors.New("桌面连接服务未能退出")
	}
	return c.waitForExit()
}

// Inspect/stop the actual local workload before replacing the installed
// executable. The gateway release is always scoped to client connections.
func PrepareDesktopUpdate(fullConfig string, stop bool) (DesktopResult, error) {
	result, err := PrepareUpdate(fullConfig, stop)
	if err != nil || result.Status != "prepared" {
		return result, err
	}
	profile := filepath.Dir(desktopConfigPath(fullConfig))
	c, err := connectController(profile)
	if errors.Is(err, errControllerUnavailable) {
		// Absence and an unresponsive but locked gateway are different.
		return PrepareUpdate(desktopConfigPath(fullConfig), false)
	}
	if err != nil {
		return DesktopResult{}, err
	}
	defer c.close()
	if err = c.releaseDesktopEntry(); err != nil {
		return DesktopResult{}, err
	}
	return result, nil
}
