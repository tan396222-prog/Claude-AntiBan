package mobilelink

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const bootstrapJS = `(async()=>{try {
 const destination=new URLSearchParams(location.search).get("page")==="archived"?"/?page=archived":"/";
 const token=location.hash.slice(1); history.replaceState(null,"",location.pathname);
 const res=await fetch("/link-session",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({token})});
 if (!res.ok) throw Error(); location.replace(destination);
}catch{document.body.textContent="连接入口已失效，请使用本机新生成的入口。";}})();`

type Browser struct {
	mu        sync.Mutex
	origin    *url.URL
	launch    [32]byte
	launchTTL time.Time
	cookie    string
	root      *os.Root
	api       http.Handler
}

func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

type OperationClient interface {
	Do(context.Context, string, string, []byte) (*Reply, error)
}

// NewBrowser serves trusted mobile build assets and the restricted client
// bridge on an explicitly loopback-only listener. The caller places launchURL
// into a private local launch file. It must never be printed or logged.
func NewBrowser(origin, directory string, client OperationClient) (browser *Browser, launchURL string, err error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || !loopback(u.Hostname()) || !explicitPort(u) || u.User != nil ||
		u.Path != "" || u.Fragment != "" || u.RawQuery != "" || u.ForceQuery || client == nil {
		return nil, "", errors.New("local UI requires a loopback origin and a connected peer")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, "", errors.New("cannot open local mobile build directory")
	}
	if info, err := root.Stat("mobile.html"); err != nil || !info.Mode().IsRegular() {
		root.Close()
		return nil, "", errors.New("build directory must contain mobile.html")
	}
	token := randomToken()
	b := &Browser{origin: u, root: root, api: operations(client.Do), launch: sha256.Sum256([]byte(token)), launchTTL: time.Now().Add(2 * time.Minute), cookie: randomToken()}
	return b, origin + "/link-bootstrap#" + token, nil
}

func (b *Browser) Close() error { return b.root.Close() }

func (b *Browser) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; media-src 'self' blob:; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; object-src 'none'")
	if r.Host != b.origin.Host || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != b.origin.String()) ||
		(r.Method != http.MethodGet && r.Header.Get("Origin") != b.origin.String()) || r.URL.RawPath != "" {
		http.Error(w, "origin refused", http.StatusForbidden)
		return
	}
	if r.Method == http.MethodGet && (r.URL.RawQuery == "" || (r.URL.Path == "/link-bootstrap" && r.URL.RawQuery == "page=archived")) {
		switch r.URL.Path {
		case "/link-bootstrap":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>Cage</title><body>正在连接 Cage…<script src="/link-bootstrap.js"></script></body></html>`))
			return
		case "/link-bootstrap.js":
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = w.Write([]byte(bootstrapJS))
			return
		}
	}
	name := "cage_mobile_link_" + b.origin.Port()
	if r.Method == http.MethodPost && r.URL.Path == "/link-session" && r.URL.RawQuery == "" {
		var in struct{ Token string }
		data, err := limited(r.Body, 512)
		if err != nil || json.Unmarshal(data, &in) != nil {
			http.Error(w, "invalid entry", http.StatusBadRequest)
			return
		}
		digest := sha256.Sum256([]byte(in.Token))
		b.mu.Lock()
		valid := time.Now().Before(b.launchTTL) && subtle.ConstantTimeCompare(digest[:], b.launch[:]) == 1
		if valid {
			b.launchTTL = time.Time{}
			clear(b.launch[:])
		}
		b.mu.Unlock()
		if !valid {
			http.Error(w, "entry expired", http.StatusUnauthorized)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: name, Value: b.cookie, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
		w.WriteHeader(http.StatusNoContent)
		return
	}
	cookie, err := r.Cookie(name)
	if err != nil || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(b.cookie)) != 1 {
		http.Error(w, "open the private local launch file", http.StatusUnauthorized)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		b.api.ServeHTTP(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	file := strings.TrimPrefix(r.URL.Path, "/")
	if file == "" {
		file = "mobile.html"
	}
	if (file != "mobile.html" && !strings.HasPrefix(file, "assets/")) || !fs.ValidPath(file) || strings.Contains(file, "\\") {
		http.NotFound(w, r)
		return
	}
	f, err := b.root.Open(file)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}
