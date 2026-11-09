package workbench

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"time"
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
	case "/audit", "/paste-image":
		timeout = 20 * time.Second
	case "/editor-restart":
		timeout = 30 * time.Second
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	response, err := c.open(ctx, token, method, path, "application/json", reader, int64(len(body)), timeout)
	if err != nil {
		return nil, errors.New("浏览器服务暂时无法连接")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, browserResponseLimit+1))
	if err != nil {
		return nil, errors.New("浏览器响应中断")
	}
	if len(data) > browserResponseLimit {
		return nil, errors.New("浏览器响应超出大小限制")
	}
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("浏览器操作失败，请刷新标签后重试")
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
		return nil, errors.New("Browser unavailable")
	}
	return response.Body, nil
}

func (c *browserClient) file(ctx context.Context, token, method, path string, body io.Reader, length int64) (*http.Response, error) {
	return c.open(ctx, token, method, path, "application/octet-stream", body, length, 30*time.Minute)
}
