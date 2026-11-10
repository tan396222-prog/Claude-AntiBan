//go:build !windows && !darwin && !linux

package hostproxy

import (
	"net/http"
	"net/url"
)

// Unix downloads use the captured proxy environment or normal host routing/TUN.
func systemProxy(*http.Request) (*url.URL, error) { return nil, nil }
