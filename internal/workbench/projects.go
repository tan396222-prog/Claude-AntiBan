package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"al.essio.dev/pkg/shellescape"
	"github.com/lima-vm/sshocker/pkg/reversesshfs"
	"github.com/sylvinus/agent-vm/internal/mounts"
)

// connectProject is the shared project entry point. Callers hold the lifecycle
// lock. Consumers may reconnect an existing grant; only adding a project grants
// access to a new host directory.
func (s *Server) connectProject(path string, add bool) (string, error) {
	if path == "" {
		path = s.workspace()
	}
	path, err := validateWorkspace(path, s.profile)
	if err != nil {
		return "", err
	}
	if !add {
		if path, err = s.projectWorkspace(path); err != nil {
			return "", err
		}
	}
	if err = s.ensureProjectShare(path); err != nil {
		return "", err
	}
	if add {
		if err = s.rememberSharedProject(path); err != nil {
			return "", err
		}
	}
	return path, nil
}

func (s *Server) openProject(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Workspace string `json:"workspace"`
		Add       bool   `json:"add"`
		Activate  bool   `json:"activate"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	if input.Workspace == "" {
		http.Error(w, "请选择项目文件夹", 400)
		return
	}
	if !s.op.TryLock() {
		http.Error(w, "另一个操作仍在进行，请稍候", 409)
		return
	}
	defer s.op.Unlock()
	s.mu.Lock()
	ready, closing, preferences := s.ready, s.closing, s.preferences
	s.mu.Unlock()
	if closing || !ready || s.busy() {
		http.Error(w, "环境尚未就绪或正在处理其他操作，请稍后重试", 409)
		return
	}
	workspace, err := s.connectProject(input.Workspace, input.Add)
	if err != nil {
		requestError(w, err)
		return
	}
	if input.Activate {
		preferences.Workspace = workspace
		if err = writePrivate(filepath.Join(s.profile, "workbench.json"), preferences); err != nil {
			requestError(w, err)
			return
		}
		s.mu.Lock()
		s.preferences.Workspace = workspace
		s.mu.Unlock()
	}
	jsonReply(w, map[string]string{"workspace": workspace})
}

type projectTransport struct {
	io.ReadCloser
	io.WriteCloser
}

func (p projectTransport) Close() error { _ = p.ReadCloser.Close(); return p.WriteCloser.Close() }

type projectShare struct {
	cmd       *exec.Cmd
	transport projectTransport
	done      chan struct{}
	once      sync.Once
}

func (p *projectShare) stop() {
	p.once.Do(func() {
		if p.cmd != nil && p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		if p.transport.ReadCloser != nil {
			_ = p.transport.Close()
		}
	})
}

func (p *projectShare) close() {
	p.stop()
	if p.done != nil {
		<-p.done
	}
}

// Called under the lifecycle lock. Each SFTP server is rooted in exactly the
// directory the user opened; switching projects keeps existing mounts alive.
func (s *Server) ensureProjectShare(path string) (returned error) {
	s.mu.Lock()
	closing := s.closing
	s.mu.Unlock()
	if closing {
		return errJobsClosed
	}
	defer func() {
		if returned == nil {
			s.clearProjectError(path)
		}
	}()
	path, err := validateWorkspace(path, s.profile)
	if err != nil {
		return err
	}
	if path == s.environmentDir() {
		return nil
	}
	s.mu.Lock()
	current := s.projectShares[path]
	s.mu.Unlock()
	if current != nil {
		select {
		case <-current.done:
			current.close()
		default:
			return nil
		}
	}
	ctx, cancel := context.WithTimeout(s.operationContext(), 15*time.Second)
	defer cancel()
	quoted := shellescape.Quote(guestPath(path))
	// Stale FUSE mounts can remain after a control-service restart. Only reconnect
	// this explicitly authorized path, without changing other projects' mounts.
	if _, err = s.guest(ctx, "if mountpoint -q "+quoted+"; then fusermount3 -uz "+quoted+"; fi; sudo mkdir -p "+quoted+" && sudo chown \"$(id -u):$(id -g)\" "+quoted); err != nil {
		return errors.New("无法在 Cage 中准备项目目录")
	}
	probe, err := os.CreateTemp(path, ".cage-mount-probe-*")
	if err != nil {
		return fmt.Errorf("无法验证项目目录的读写权限：%w", err)
	}
	defer os.Remove(probe.Name())
	challenge := randomToken()
	if _, err = probe.WriteString(challenge); err != nil {
		_ = probe.Close()
		return err
	}
	if err = probe.Close(); err != nil {
		return err
	}
	script := "exec sshfs " + shellescape.Quote(":"+filepath.ToSlash(path)) + " " + quoted + " -o slave,allow_other,cache=no,nodev,nosuid"
	cmd := s.sshCommand(s.lifetimeContext(), "-T", s.sshTarget(), "--", script)
	output, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	input, err := cmd.StdinPipe()
	if err != nil {
		_ = output.Close()
		return err
	}
	share := &projectShare{cmd: cmd, transport: projectTransport{output, input}, done: make(chan struct{})}
	if err = cmd.Start(); err != nil {
		_ = share.transport.Close()
		return err
	}
	go func() {
		defer close(share.done)
		_ = reversesshfs.ServeConfined(share.transport, path, false, mounts.BaseNames)
		share.stop()
		_ = cmd.Wait()
	}()
	for {
		if _, err = s.guest(ctx, "mountpoint -q "+quoted+" && test \"$(cat "+shellescape.Quote(guestPath(probe.Name()))+")\" = "+shellescape.Quote(challenge)); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			share.close()
			return errors.New("项目文件夹连接超时，原有环境与会话保持运行")
		case <-share.done:
			share.close()
			return errors.New("项目文件夹未能连接，原有环境与会话保持运行")
		case <-time.After(100 * time.Millisecond):
		}
	}
	s.mu.Lock()
	if s.projectShares == nil {
		s.projectShares = map[string]*projectShare{}
	}
	s.projectShares[path] = share
	s.mu.Unlock()
	return nil
}

type sharedProjects struct {
	Paths []string `json:"paths"`
}

type projectShareError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

func (s *Server) clearProjectError(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, failure := range s.projectErrors {
		if failure.Path == path {
			s.projectErrors = append(s.projectErrors[:i], s.projectErrors[i+1:]...)
			return
		}
	}
}

// The saved directory grants are the project list. Include the default and
// current directories for profiles created before projects.json existed.
func (s *Server) projectPaths() ([]string, error) {
	var saved sharedProjects
	if b, err := os.ReadFile(filepath.Join(s.profile, "projects.json")); err == nil {
		if err = json.Unmarshal(b, &saved); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	paths := []string{s.environmentDir()}
	for _, path := range append(saved.Paths, s.workspace()) {
		found := path == ""
		for _, existing := range paths {
			found = found || path == existing
		}
		if !found {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

func (s *Server) projectWorkspace(path string) (string, error) {
	if path == "" {
		path = s.workspace()
	}
	paths, err := s.projectPaths()
	if err != nil {
		return "", errors.New("无法读取项目列表，请检查项目配置")
	}
	for _, authorized := range paths {
		if path == authorized {
			return validateWorkspace(path, s.profile)
		}
	}
	return "", errors.New("请先通过打开项目添加该文件夹")
}

func (s *Server) rememberSharedProject(path string) error {
	if path == s.environmentDir() {
		return nil
	}
	var saved sharedProjects
	if b, err := os.ReadFile(filepath.Join(s.profile, "projects.json")); err == nil {
		if err = json.Unmarshal(b, &saved); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	for _, p := range saved.Paths {
		if p == path {
			return nil
		}
	}
	saved.Paths = append(saved.Paths, path)
	return writePrivate(filepath.Join(s.profile, "projects.json"), saved)
}
func (s *Server) restoreProjectShares() {
	var saved sharedProjects
	if b, err := os.ReadFile(filepath.Join(s.profile, "projects.json")); err == nil {
		_ = json.Unmarshal(b, &saved)
	}
	current := s.workspace()
	if current != "" && current != s.environmentDir() {
		found := false
		for _, p := range saved.Paths {
			found = found || p == current
		}
		if !found {
			saved.Paths = append(saved.Paths, current)
		}
	}
	var failures []projectShareError
	for _, path := range saved.Paths {
		if err := s.ensureProjectShare(path); err != nil {
			failures = append(failures, projectShareError{Path: path, Message: err.Error()})
		} else if err := s.rememberSharedProject(path); err != nil {
			failures = append(failures, projectShareError{Path: path, Message: "项目已连接，但未能保存项目记录：" + err.Error()})
		}
	}
	s.mu.Lock()
	s.projectErrors = failures
	s.mu.Unlock()
}
func (s *Server) closeProjectShares() {
	s.mu.Lock()
	shares := s.projectShares
	s.projectShares = nil
	s.mu.Unlock()
	for _, share := range shares {
		share.stop()
	}
	for _, share := range shares {
		share.close()
	}
}
