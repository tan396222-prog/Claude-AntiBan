package remotehost

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"sync"
	"syscall"
	"time"
)

// LocalSession attaches the gateway to a controller after its private launch
// ticket has been exchanged. It shares the SSH transport's cookie, origin,
// redirect and stream cancellation rules. Closing it only closes this view.
func LocalSession(ctx context.Context, origin string, manifest PublicManifest, jar http.CookieJar) (*Session, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.Hostname() != "localhost" || u.Port() == "" || u.User != nil || origin != "http://"+u.Host || jar == nil || len(jar.Cookies(u)) == 0 {
		return nil, errors.New("本机控制服务会话无效")
	}
	endpoint := net.JoinHostPort("127.0.0.1", u.Port())
	dial := &net.Dialer{Timeout: 5 * time.Second}
	life, cancel := context.WithCancel(ctx)
	base := &http.Transport{MaxIdleConnsPerHost: 16, IdleConnTimeout: time.Minute, ResponseHeaderTimeout: 30 * time.Second, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" || address != u.Host {
			return nil, errors.New("本机服务地址已改变")
		}
		conn, err := dial.DialContext(ctx, "tcp4", endpoint)
		if errors.Is(err, syscall.ECONNREFUSED) {
			cancel()
		}
		return conn, err
	}}
	bound := &sessionTransport{base: base, jar: jar, origin: origin, ctx: life}
	transport := localViewTransport{bound}
	s := &Session{Manifest: manifest, Origin: origin, Context: life, Transport: transport}
	var once sync.Once
	s.close = func() error {
		once.Do(func() { cancel(); base.CloseIdleConnections() })
		return nil
	}
	bound.invalidate = s.Close
	s.Client = &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("本机服务重定向已拒绝") }}
	context.AfterFunc(life, func() { _ = s.Close() })
	return s, nil
}

type localViewTransport struct{ base *sessionTransport }

func (t localViewTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	// An older full controller can also route remote sessions. Pin the local
	// side of this hop even if some other caller changes that controller's mode.
	req := r.Clone(r.Context())
	req.Header.Set("X-Cage-Connection", "local")
	return t.base.RoundTrip(req)
}
