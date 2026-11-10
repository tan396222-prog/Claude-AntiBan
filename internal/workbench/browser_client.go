package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/sylvinus/agent-vm/internal/diagnostics"
	"github.com/sylvinus/agent-vm/internal/usererror"
)

const browserResponseLimit = 1 << 20
const browserOrigin = "http://127.0.0.1:18500"

// browserClient owns host connections to the guest browser controller. Closing
// it cancels those connections; the guest browser process remains untouched.
type browserClient struct {
	ctx    context.Context
	cancel context.CancelFunc
	http   *http.Client
}

func newBrowserClient(parent context.Context) *browserClient {
	ctx, cancel := context.WithCancel(parent)
	return &browserClient{ctx: ctx, cancel: cancel, http: &http.Client{
		Transport:     &http.Transport{Proxy: nil, IdleConnTimeout: 30 * time.Second, MaxIdleConnsPerHost: 4, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext},
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") },
	}}
}

func (c *browserClient) close() {
	c.cancel()
	c.http.CloseIdleConnections()
}

// The response owns cancellation until its body is closed. This also covers
// event subscriptions, whose lifetime extends beyond receiving HTTP headers.
func (c *browserClient) open(parent context.Context, token, method, path, contentType string, body io.Reader, length int64, timeout time.Duration) (*http.Response, error) {
	if err := c.ctx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(c.ctx, cancel)
	finish := func() { stop(); cancel() }
	if timeout > 0 {
		var cancelTimeout context.CancelFunc
		ctx, cancelTimeout = context.WithTimeout(ctx, timeout)
		finish = func() { stop(); cancelTimeout(); cancel() }
	}
	req, err := http.NewRequestWithContext(ctx, method, browserOrigin+path, body)
	if err != nil {
		finish()
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", contentType)
		req.ContentLength = length
	}
	response, err := c.http.Do(req)
	if err != nil {
		finish()
		return nil, err
	}
	response.Body = &browserResponseBody{ReadCloser: response.Body, finish: finish}
	return response, nil
}

type browserResponseBody struct {
	io.ReadCloser
	finish func()
}

func (b *browserResponseBody) Close() error {
	b.finish()
	return b.ReadCloser.Close()
}

func (c *browserClient) request(ctx context.Context, token, method, path string, body []byte) ([]byte, error) {
	timeout := 5 * time.Second
	switch path {
	case "/audit", "/paste-image", "/tailnet":
		timeout = 20 * time.Second
	case "/editor-restart":
		timeout = 30 * time.Second
	case "/extensions":
		timeout = 25 * time.Second
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	response, err := c.open(ctx, token, method, path, "application/json", reader, int64(len(body)), timeout)
	if err != nil {
		diagnostics.Log("warn", "browser", fmt.Sprintf("operation=%s transport=%v", path, err))
		return nil, usererror.Wrap(browserOperation(path), err, "请确认环境仍在运行，并重新连接浏览器；持续失败时在设置中导出诊断包。")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, browserResponseLimit+1))
	if err != nil {
		return nil, usererror.Wrap(browserOperation(path), err, "浏览器结果没有完整返回，请重新连接后核对本次操作是否已生效。")
	}
	if len(data) > browserResponseLimit {
		return nil, errors.New(browserOperation(path) + "失败：浏览器返回的数据超过 1 MiB，已停止读取。请关闭不再使用的标签页或扩展后再试；持续出现时导出诊断包。")
	}
	if response.StatusCode != http.StatusOK {
		diagnostics.Log("warn", "browser", fmt.Sprintf("operation=%s http=%d", path, response.StatusCode))
		var result struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &result)
		known := map[string]string{
			"Tab no longer exists": "标签页已关闭。请重新打开网页后再操作。",
			"Browser is starting":  "浏览器仍在启动。请等待启动完成后再操作。",
			"Input target changed": "接收内容的页面已切换。请回到原页面并重新点击输入框。",
			"Image target changed": "接收图片的页面已切换。请回到原页面并重新点击输入框。",
			"Copy target changed":  "要复制内容的页面已切换。请回到原页面重新复制。",
			"Image too large":      "图片超过允许大小。请缩小图片后重新粘贴。",
			"Invalid PNG":          "图片不是有效的 PNG 文件。请重新复制或导出图片。",
			"Only HTTP(S) URLs without credentials are accepted":         "网址中包含账号或密码。请使用不带登录凭据的 http 或 https 网址。",
			"Only HTTP(S) URLs and supported browser pages are accepted": "不支持这个网址类型。请使用 http 或 https 网址。",
			"Unknown browser action":                                     "浏览器组件不支持这次操作。请更新环境组件后再试。",
			"Split controller unavailable":                               "分屏组件没有就绪。请更新环境组件后重新打开分屏。",
			"Editor authentication is unavailable":                       "编辑器的本地登录会话不可用。请从 Cage 重新打开编辑器。",
			"文件接收网页已切换，请返回原网页重试":                                         "文件接收网页已切换，请返回原网页重试。",
			"浏览器组件未能恢复，请重启环境":                                            "浏览器组件未能恢复，请保存工作后重启环境。",
			"不同浏览会话不能分屏":                                                 "所选页面属于不同浏览会话，无法放在同一个分屏中。请在同一会话中打开两个网页。",
			"无痕标签暂不支持双网页分屏":                                              "无痕标签暂不支持双网页分屏。请使用普通标签页。",
			"设置页请在单个浏览器窗格内使用":                                            "设置页请在单个浏览器窗格内使用。请先退出分屏。",
			"扩展 ZIP 不能超过 32 MB":                                          "扩展 ZIP 不能超过 32 MB。请缩小扩展包后重新导入。",
		}
		if reason := known[result.Error]; reason != "" {
			return nil, errors.New(browserOperation(path) + "失败：" + reason)
		}
		return nil, serviceHTTPFailure(browserOperation(path), response.StatusCode)
	}
	return data, nil
}

func (c *browserClient) events(ctx context.Context, token string) (io.ReadCloser, error) {
	response, err := c.open(ctx, token, http.MethodGet, "/events", "", nil, 0, 0)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, serviceHTTPFailure("接收浏览器状态", response.StatusCode)
	}
	return response.Body, nil
}

func (c *browserClient) file(ctx context.Context, token, method, path string, body io.Reader, length int64) (*http.Response, error) {
	return c.open(ctx, token, method, path, "application/octet-stream", body, length, 30*time.Minute)
}
