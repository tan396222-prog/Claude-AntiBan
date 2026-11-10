package mobilelink

import (
	"net/url"
	"testing"
)

func TestDeviceModel(t *testing.T) {
	for _, tc := range []struct{ platform, raw, want string }{
		{"ios", "iPhone17,1", "iPhone 16 Pro"},
		{"ios", "iPhone18,1", "iPhone 17 Pro"},
		{"ios", "iPhone99,1", "iPhone (iPhone99,1)"},
		{"ios", "iPad99,1", "iPad (iPad99,1)"},
		{"android", " Google Pixel 9 Pro ", "Google Pixel 9 Pro"},
	} {
		if got := DeviceModel(tc.platform, tc.raw); got != tc.want {
			t.Errorf("%s %q: got %q, want %q", tc.platform, tc.raw, got, tc.want)
		}
	}
}

func TestDeviceReportOperationBoundary(t *testing.T) {
	u := &url.URL{Path: "/api/mobile/device"}
	if !allowed("POST", u) || allowed("GET", u) || allowed("DELETE", u) {
		t.Fatal("device report must only accept POST")
	}
	u.RawQuery = "model=other"
	if allowed("POST", u) {
		t.Fatal("device report must not accept query parameters")
	}
}
