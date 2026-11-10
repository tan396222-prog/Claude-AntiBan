// Package mobilelink connects a paired mobile client to the mobile
// operations of a single Cage host. It never exposes the full workbench API.
package mobilelink

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	maxRequest  = 72 << 10
	maxResponse = 4 << 20
	linkOrigin  = "https://cage-mobile.invalid"
)

var sessionPath = regexp.MustCompile(`^/api/mobile/cli/sessions/[A-Fa-f0-9]{8}-[A-Fa-f0-9]{4}-[A-Fa-f0-9]{4}-[A-Fa-f0-9]{4}-[A-Fa-f0-9]{12}(/messages|/media|/stop|/approval|/open|/metadata|/account|/delete|/archive|/restore|/purge)?$`)

var surfacePath = regexp.MustCompile(`^/api/mobile/(?:browser|desktop)/[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}/(frame|action|close)$`)

var epochID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

var frameQuery = regexp.MustCompile(`^after=([0-9a-f]{32})$`)

// FrameAfter accepts a single opaque frame tag in its canonical wire form.
func FrameAfter(query string) (string, bool) {
	match := frameQuery.FindStringSubmatch(query)
	if match == nil {
		return "", false
	}
	return match[1], true
}

func allowed(method string, u *url.URL) bool {
	if u == nil || u.RawPath != "" || u.Opaque != "" || u.User != nil || u.Fragment != "" || u.ForceQuery {
		return false
	}
	if u.Path == "/api/mobile/cli/sessions" && u.RawQuery == "archived=true" {
		return method == http.MethodGet
	}
	if u.RawQuery == "" {
		switch u.Path {
		case "/api/mobile/browser/open", "/api/mobile/desktop/open", "/api/mobile/device":
			return method == http.MethodPost
		case "/api/mobile/cli/projects":
			return method == http.MethodGet
		case "/api/mobile/cli/sessions", "/api/mobile/browser/favorites":
			return method == http.MethodGet || method == http.MethodPost
		}
	}
	if match := surfacePath.FindStringSubmatch(u.Path); match != nil {
		if u.RawQuery == "" {
			return (match[1] == "frame" && method == http.MethodGet) || (match[1] != "frame" && method == http.MethodPost)
		}
		_, ok := FrameAfter(u.RawQuery)
		return ok && match[1] == "frame" && method == http.MethodGet && strings.HasPrefix(u.Path, "/api/mobile/browser/")
	}
	match := sessionPath.FindStringSubmatch(u.Path)
	if match == nil {
		return false
	}
	if match[1] != "" {
		return method == http.MethodPost && u.RawQuery == ""
	}
	if method != http.MethodGet {
		return false
	}
	if u.RawQuery == "" {
		return true
	}
	fields := strings.Split(u.RawQuery, "&")
	if len(fields) > 2 {
		return false
	}
	var after string
	var hasEpoch bool
	for _, field := range fields {
		key, value, _ := strings.Cut(field, "=")
		switch key {
		case "after":
			if after != "" || value == "" {
				return false
			}
			after = value
		case "epoch":
			if hasEpoch || !epochID.MatchString(value) {
				return false
			}
			hasEpoch = true
		default:
			return false
		}
	}
	if after == "" {
		return false
	}
	for _, digit := range after {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	_, err := strconv.ParseUint(after, 10, 64)
	return err == nil
}

// Reply carries only application data. Browser cookies, redirects, hop headers
// and upstream authentication fields never cross the device link.
type Reply struct {
	Status      int
	ContentType string
	Body        []byte
}

type performFunc func(context.Context, string, string, []byte) (*Reply, error)

func limited(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("payload exceeds limit or could not be read")
	}
	return data, nil
}

// operations checks the method and route before reading data. Callers establish
// the peer identity or the local browser session before invoking this handler.
func operations(do performFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if !allowed(r.Method, r.URL) {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodPost {
			kind, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if kind != "application/json" {
				http.Error(w, "JSON is required", http.StatusUnsupportedMediaType)
				return
			}
		}
		body, err := limited(r.Body, maxRequest)
		if err != nil {
			http.Error(w, "request body too large or unreadable", http.StatusRequestEntityTooLarge)
			return
		}
		if r.Method == http.MethodGet && len(body) != 0 {
			http.Error(w, "GET must not have a body", http.StatusBadRequest)
			return
		}
		reply, err := do(r.Context(), r.Method, r.URL.RequestURI(), body)
		if err != nil {
			http.Error(w, "与电脑的连接已中断。请检查手机网络和电脑上的 Cage，再重新读取会话。", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", reply.ContentType)
		w.WriteHeader(reply.Status)
		if reply.Status != http.StatusNoContent {
			_, _ = w.Write(reply.Body)
		}
	})
}

func request(ctx context.Context, client http.RoundTripper, origin, method, path string, body []byte, localOrigin bool) (*Reply, error) {
	u, err := url.ParseRequestURI(path)
	if err != nil || u.IsAbs() || u.Host != "" || !allowed(method, u) || len(body) > maxRequest || (method == http.MethodGet && len(body) > 0) {
		return nil, errors.New("mobile operation refused")
	}
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, origin+path, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("invalid mobile operation")
	}
	req.GetBody = nil // A mutation with a lost response is never replayed here.
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	if localOrigin {
		req.Header.Set("Origin", origin)
	}
	res, err := client.RoundTrip(req)
	if err != nil {
		return nil, errors.New("mobile operation interrupted")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || (res.StatusCode >= 300 && res.StatusCode < 400) {
		return nil, errors.New("unexpected mobile response status")
	}
	data, err := limited(res.Body, maxResponse)
	if err != nil {
		return nil, err
	}
	contentType := "text/plain; charset=utf-8"
	if kind, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type")); kind == "application/json" {
		contentType = "application/json"
	}
	return &Reply{res.StatusCode, contentType, data}, nil
}
