package workbench

import (
	"context"
	"errors"
	"io"
	"net/http"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"al.essio.dev/pkg/shellescape"
	"github.com/sylvinus/agent-vm/internal/containerenv"
	"golang.org/x/net/http/httpguts"
)

// This namespace exists only in the Linux guest. Never resolve its paths on
// the server host or persist them as grants across connections.
const clientMountRoot = "/run/cage-client-projects"

var clientMountID = regexp.MustCompile(`^[0-9a-f]{32}$`)
var errClientMountGone = errors.New("本机项目共享已断开，请在本机重新选择文件夹")

type guestClientMount struct {
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	ready  bool // protected by Server.mu
}

// Caller holds Server.mu. Cancellation closes admission before unmount cleanup.
func (m *guestClientMount) live() bool { return m.ready && m.ctx.Err() == nil }

func isClientMountPath(path string) bool {
	return path == clientMountRoot || strings.HasPrefix(path, clientMountRoot+"/")
}

func (s *Server) clientMountWorkspace(path string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if mount := s.clientMounts[path]; mount != nil && mount.live() {
		return path, nil
	}
	return "", errClientMountGone
}

func (s *Server) appendClientMounts(paths []string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var live []string
	for path, mount := range s.clientMounts {
		if mount.live() && !slices.Contains(paths, path) {
			live = append(live, path)
		}
	}
	sort.Strings(live)
	return append(paths, live...)
}

func (s *Server) closeClientMounts() {
	s.mu.Lock()
	var mounts []*guestClientMount
	for _, m := range s.clientMounts {
		m.cancel()
		mounts = append(mounts, m)
	}
	s.mu.Unlock()
	for _, m := range mounts {
		<-m.done
	}
}

// After a controller crash a disconnected FUSE mount may outlive its stream.
// Only retire exact Cage names in the guest's reserved, root-owned directory.
// rmdir preserves any unexpected nonempty directory.
func (s *Server) recoverClientMounts(ctx context.Context) error {
	s.mu.Lock()
	initialized := s.clientMounts != nil
	s.mu.Unlock()
	if initialized {
		return nil
	}
	const recoverScript = `set -eu
root=` + clientMountRoot + `
[ ! -L "$root" ] || exit 1
[ -d "$root" ] || exit 0
for p in "$root"/*; do
  [[ ${p##*/} =~ ^[a-f0-9]{32}$ ]] || continue
  [ ! -L "$p" ] || continue
  kind=$(findmnt -rn --nocanonicalize -o FSTYPE --mountpoint "$p") || kind=
  case "$kind" in
    fuse.sshfs) sudo -n umount -l -- "$p";;
    "") ;;
    *) continue;;
  esac
  sudo -n rmdir -- "$p" || true
done`
	if _, err := s.guest(ctx, recoverScript); err != nil {
		return errors.New("无法清理上次连接遗留的本机项目挂载")
	}
	s.mu.Lock()
	if s.clientMounts == nil {
		s.clientMounts = make(map[string]*guestClientMount)
	}
	s.mu.Unlock()
	return nil
}

func (s *Server) guestClientShare(w http.ResponseWriter, r *http.Request) {
	if runtime.GOOS != "linux" || !containerenv.Configured(s.profile) {
		http.NotFound(w, r)
		return
	}
	id := r.URL.Query().Get("id")
	if !clientMountID.MatchString(id) {
		http.Error(w, "本机项目标识无效", http.StatusBadRequest)
		return
	}
	path := clientMountRoot + "/" + id
	if r.Method == http.MethodGet {
		if _, err := s.clientMountWorkspace(path); err != nil {
			http.Error(w, "本机项目尚未连接", http.StatusConflict)
			return
		}
		jsonReply(w, map[string]string{"workspace": path})
		return
	}
	if !httpguts.HeaderValuesContainsToken(r.Header.Values("Connection"), "upgrade") || !strings.EqualFold(r.Header.Get("Upgrade"), "cage-sftp-v1") || r.ContentLength != 0 {
		http.Error(w, "需要项目共享连接", http.StatusBadRequest)
		return
	}
	if !s.op.TryLock() {
		http.Error(w, "环境正在处理其他操作", http.StatusConflict)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	mount := &guestClientMount{ctx: ctx, cancel: cancel, done: make(chan struct{})}
	s.mu.Lock()
	available := s.ready && !s.closing && len(s.clientMounts) < 16 && s.clientMounts[path] == nil && !s.busy()
	if available {
		if s.clientMounts == nil {
			s.clientMounts = make(map[string]*guestClientMount)
		}
		s.clientMounts[path] = mount
	}
	s.mu.Unlock()
	s.op.Unlock()
	if !available {
		http.Error(w, "环境未就绪、共享已存在或已达共享上限", http.StatusConflict)
		return
	}
	defer func() {
		s.mu.Lock()
		delete(s.clientMounts, path)
		// Keep a revoked active selection until another project is selected.
		// New tasks must fail, not silently start in the previous cloud folder.
		s.mu.Unlock()
		close(mount.done)
	}()

	// A disconnected mount exposes an inaccessible underlying directory until
	// teardown removes it. Never let an old cwd accept writes into the cloud.
	target := shellescape.Quote(path)
	setup, finishSetup := context.WithTimeout(ctx, 10*time.Second)
	_, err := s.guest(setup, "sudo -n install -d -m 0755 "+clientMountRoot+" && sudo -n mkdir -m 000 -- "+target)
	finishSetup()
	if err != nil {
		http.Error(w, "无法准备云端容器挂载位置", http.StatusConflict)
		return
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = s.guest(ctx, "if findmnt -rn --nocanonicalize --mountpoint "+target+" >/dev/null; then sudo -n umount -l -- "+target+"; fi; sudo -n rmdir -- "+target)
	}()
	conn, buffer, err := http.NewResponseController(w).Hijack()
	if err != nil {
		http.Error(w, "无法建立项目共享数据流", http.StatusServiceUnavailable)
		return
	}
	defer conn.Close()
	stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopClose()
	if _, err = buffer.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: cage-sftp-v1\r\n\r\n"); err != nil || buffer.Flush() != nil {
		return
	}
	// sshfs runs inside the guest. Its only SFTP transport is this stream.
	// Keep normal mmap/read-ahead support for compilers and search tools.
	// Present files as the guest user so ownership checks (e.g. Git) work
	// when the workstation uses a different UID or Windows ACLs.
	script := "exec sudo -n sshfs -f :/ " + target + ` -o "slave,allow_other,cache=no,nodev,nosuid,uid=$(id -u),gid=$(id -g)"`
	cmd := s.sshCommand(ctx, "-T", s.sshTarget(), "--", script)
	cmd.Stdout = conn
	input, err := cmd.StdinPipe()
	if err != nil {
		return
	}
	defer input.Close()
	if err = cmd.Start(); err != nil {
		return
	}
	ingressDone := make(chan struct{})
	go func() {
		defer close(ingressDone)
		_, _ = io.Copy(input, buffer.Reader)
		cancel()
	}()
	// Wait for the actual kernel mount and a successful SFTP stat before
	// admitting any terminal, editor or Claude session to this workspace.
	probed := make(chan struct{})
	go func() {
		defer close(probed)
		probe, end := context.WithTimeout(ctx, 15*time.Second)
		defer end()
		verify := "until [ \"$(findmnt -n -o FSTYPE --mountpoint " + target + ")\" = fuse.sshfs ] && stat -- " + target + "/. >/dev/null 2>&1; do sleep 0.1; done"
		if _, err := s.guest(probe, verify); err != nil {
			cancel()
			return
		}
		s.mu.Lock()
		mount.ready = ctx.Err() == nil
		s.mu.Unlock()
	}()
	_ = cmd.Wait()
	cancel()
	<-ingressDone
	<-probed
}
