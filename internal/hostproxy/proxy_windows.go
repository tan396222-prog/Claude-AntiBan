package hostproxy

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var winhttp = windows.NewLazySystemDLL("winhttp.dll")
var globalFree = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalFree")

type ieConfig struct {
	AutoDetect         int32
	URL, Proxy, Bypass *uint16
}
type autoOptions struct {
	Flags, DetectFlags uint32
	URL                *uint16
	Reserved           uintptr
	ReservedFlags      uint32
	AutoLogon          int32
}
type proxyInfo struct {
	AccessType    uint32
	Proxy, Bypass *uint16
}

func freeString(p *uint16) {
	if p != nil {
		globalFree.Call(uintptr(unsafe.Pointer(p)))
	}
}
func proxyString(p *uint16) string { return windows.UTF16PtrToString(p) }

func systemProxy(req *http.Request) (*url.URL, error) {
	var config ieConfig
	ok, _, err := winhttp.NewProc("WinHttpGetIEProxyConfigForCurrentUser").Call(uintptr(unsafe.Pointer(&config)))
	if ok == 0 {
		if err == windows.ERROR_FILE_NOT_FOUND {
			return nil, nil
		}
		return nil, fmt.Errorf("无法读取主机系统代理，未切换直连：%w", err)
	}
	defer freeString(config.URL)
	defer freeString(config.Proxy)
	defer freeString(config.Bypass)
	if config.URL != nil || config.AutoDetect != 0 {
		handle, _, err := winhttp.NewProc("WinHttpOpen").Call(0, 1, 0, 0, 0)
		if handle == 0 {
			return nil, fmt.Errorf("无法解析主机自动代理：%w", err)
		}
		defer winhttp.NewProc("WinHttpCloseHandle").Call(handle)
		winhttp.NewProc("WinHttpSetTimeouts").Call(handle, 15000, 15000, 15000, 15000)
		options := autoOptions{URL: config.URL}
		if config.URL != nil {
			options.Flags = 2
		} else {
			options.Flags = 1
			options.DetectFlags = 3
		}
		target, err := windows.UTF16PtrFromString(req.URL.String())
		if err != nil {
			return nil, err
		}
		var info proxyInfo
		ok, _, err := winhttp.NewProc("WinHttpGetProxyForUrl").Call(handle, uintptr(unsafe.Pointer(target)), uintptr(unsafe.Pointer(&options)), uintptr(unsafe.Pointer(&info)))
		defer freeString(info.Proxy)
		defer freeString(info.Bypass)
		if ok != 0 {
			if info.AccessType == 1 {
				return nil, nil
			} // PAC explicitly selected DIRECT.
			if info.Proxy == nil {
				return nil, fmt.Errorf("主机自动代理返回空地址，未切换直连")
			}
			return windowsProxy(req, proxyString(info.Proxy), proxyString(info.Bypass))
		}
		// Windows commonly enables WPAD even when this network has no PAC.
		// An explicitly configured PAC that fails must never become DIRECT.
		if config.URL != nil || err != windows.Errno(12180) {
			return nil, fmt.Errorf("主机 PAC/自动代理解析失败，未切换直连，请检查代理软件：%w", err)
		}
	}
	return windowsProxy(req, proxyString(config.Proxy), proxyString(config.Bypass))
}

func windowsProxy(req *http.Request, list, bypass string) (*url.URL, error) {
	if list == "" {
		return nil, nil
	}
	var fallback, httpProxy, httpsProxy string
	for _, entry := range strings.Split(list, ";") {
		key, value, specific := strings.Cut(strings.TrimSpace(entry), "=")
		if !specific {
			if fallback == "" {
				fallback = key
			}
			continue
		}
		switch strings.ToLower(key) {
		case "http":
			httpProxy = value
		case "https":
			httpsProxy = value
		case "socks":
			fallback = "socks5://" + value
		}
	}
	if httpProxy == "" {
		httpProxy = fallback
	}
	if httpsProxy == "" {
		httpsProxy = fallback
	}
	return staticProxy(req, httpProxy, httpsProxy, bypass)
}
