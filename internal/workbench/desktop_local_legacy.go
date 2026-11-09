package workbench

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
)

const previousLocalWorkbench = "请用 cage desktop --existing 打开已有本机工作台，保存并停止任务后重试"

// Only explicit local selection calls this path. An older full controller
// can release an idle profile via its own authenticated update handoff. Its
// process and workloads remain under its control throughout this exchange.
func (d *desktopLocal) handoffOldLocal(ctx context.Context) (bool, error) {
	c, err := connectController(d.profile)
	if err != nil {
		return false, errors.New("无法认证已有本机后台。" + previousLocalWorkbench)
	}
	defer c.close()
	id := c.launch.Controller
	if c.launch.DesktopGateway || id.Protocol != controlProtocol || id.PID != c.record.PID || !id.matches(c.record.Controller) || !remoteInstanceID.MatchString(id.Build) {
		return false, errors.New("已有本机后台身份或协议不兼容。" + previousLocalWorkbench)
	}
	u, _ := url.Parse(c.launch.URL) // connectController verified the origin and ticket.
	status, err := c.callContext(ctx, "POST", "/api/session", map[string]string{"token": u.Fragment}, nil)
	if err != nil || status != http.StatusOK || len(c.client.Jar.Cookies(u)) == 0 {
		return false, errors.New("无法建立已有本机后台的会话。" + previousLocalWorkbench)
	}
	// Old controllers can answer 401 before the cookie and 423 while locked,
	// even for an unknown route. A working modern route must never be replaced.
	status, err = c.callContext(ctx, "POST", "/api/desktop-local/status", desktopLocalRequest{Token: c.record.Token}, nil)
	if err != nil {
		return false, err
	}
	switch status {
	case http.StatusOK:
		return false, nil
	case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusLocked:
	default:
		return false, errors.New("无法确认已有本机后台版本。" + previousLocalWorkbench)
	}
	f, err := os.Open(d.executable)
	if err != nil {
		return false, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return false, err
	}
	if id.Build == fmt.Sprintf("%x", h.Sum(nil)) {
		return false, errors.New("本机后台的启动器接口不可用。" + previousLocalWorkbench)
	}
	var selection ClientContext
	status, err = c.callContext(ctx, "GET", "/api/client/context", nil, &selection)
	if err != nil || (status != http.StatusOK && status != http.StatusNotFound && status != http.StatusLocked) {
		return false, errors.New("无法确认旧工作台的连接状态。" + previousLocalWorkbench)
	}
	if status == http.StatusOK && (selection.ClientOnly || selection.Mode != "local") {
		return false, errors.New("已有工作台正在管理其他连接。" + previousLocalWorkbench)
	}
	var ack struct {
		Stopping bool `json:"stopping"`
	}
	status, err = c.callContext(ctx, "POST", "/api/handoff", map[string]string{"token": c.record.Token}, &ack)
	if err != nil || status != http.StatusOK || !ack.Stopping {
		return false, errors.New("旧版本机后台仍在使用，或尚未确认空闲退出。" + previousLocalWorkbench)
	}
	if err := c.waitForExit(); err != nil {
		return false, err
	}
	return true, nil
}
