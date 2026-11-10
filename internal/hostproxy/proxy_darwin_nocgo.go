//go:build darwin && !cgo

package hostproxy

import (
	"errors"
	"net/http"
	"net/url"
)

func systemProxy(*http.Request) (*url.URL, error) {
	return nil, errors.New("此构建无法读取 macOS 系统代理，请使用正式原生构建或设置 HTTPS_PROXY；未切换直连")
}
