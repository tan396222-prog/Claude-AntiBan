package mobilelink

import (
	"net/url"
	"strings"
	"testing"
)

func TestSnapshotPollingQuery(t *testing.T) {
	const id = "12345678-1234-4321-aaaa-123456789abc"
	for _, tc := range []struct {
		query string
		want  bool
	}{
		{"", true}, {"after=0", true}, {"after=18446744073709551615", true},
		{"after=2&epoch=" + id, true}, {"epoch=" + id + "&after=2", true},
		{"epoch=" + id, false}, {"after=2&epoch=", false}, {"after=2&epoch=" + strings.ToUpper(id), false},
		{"after=2&epoch=12345678-1234-5321-aaaa-123456789abc", false},
		{"after=2&epoch=12345678-1234-4321-0aaa-123456789abc", false},
		{"after=-1&epoch=" + id, false}, {"after=0&after=1", false}, {"after=1&other=x", false},
		{"after=1&epoch=" + id + "&epoch=" + id, false}, {"after=%31&epoch=" + id, false},
		{"after=18446744073709551616", false},
	} {
		u := &url.URL{Path: "/api/mobile/cli/sessions/" + id, RawQuery: tc.query}
		if got := allowed("GET", u); got != tc.want {
			t.Errorf("poll query %q allowed=%v, want %v", tc.query, got, tc.want)
		}
	}
}

func TestBrowserFrameQuery(t *testing.T) {
	const tag = "b542cd94286a2489bc2df1241b043c76"
	const id = "12345678-1234-4321-aaaa-123456789abc"
	for _, tc := range []struct {
		method, surface, operation, query string
		want                              bool
	}{
		{"GET", "browser", "frame", "after=" + tag, true},
		{"GET", "browser", "frame", "after=" + tag + "&after=" + tag, false},
		{"GET", "browser", "frame", "after=" + strings.ToUpper(tag), false},
		{"POST", "browser", "action", "after=" + tag, false},
		{"GET", "desktop", "frame", "after=" + tag, false},
	} {
		u := &url.URL{Path: "/api/mobile/" + tc.surface + "/" + id + "/" + tc.operation, RawQuery: tc.query}
		if allowed(tc.method, u) != tc.want {
			t.Errorf("unexpected result for %s %s", tc.method, u.RequestURI())
		}
	}
}

func TestSessionListMutations(t *testing.T) {
	for _, operation := range []string{"metadata", "delete", "archive", "restore", "purge"} {
		u := &url.URL{Path: "/api/mobile/cli/sessions/12345678-1234-4321-aaaa-123456789abc/" + operation}
		if !allowed("POST", u) || allowed("GET", u) || allowed("DELETE", u) {
			t.Fatal("mutation method boundary", operation)
		}
		u.RawQuery = "after=1"
		if allowed("POST", u) {
			t.Fatal("mutation query accepted", operation)
		}
	}
}

func TestArchivedSessionQuery(t *testing.T) {
	u := &url.URL{Path: "/api/mobile/cli/sessions", RawQuery: "archived=true"}
	if !allowed("GET", u) || allowed("POST", u) {
		t.Fatal("archive query method")
	}
	for _, query := range []string{"archived=false", "archived=true&other=1", "archived=true&archived=true"} {
		u.RawQuery = query
		if allowed("GET", u) {
			t.Fatal("unexpected archive query", query)
		}
	}
}
