package hostproxy

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func systemProxy(req *http.Request) (*url.URL, error) {
	if _, err := exec.LookPath("gsettings"); err != nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(req.Context(), 3*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "gsettings", "list-recursively", "org.gnome.system.proxy").Output()
	if err != nil {
		return nil, errors.New("无法读取主机桌面代理设置，请检查代理软件或设置 HTTPS_PROXY；未切换直连")
	}
	return gnomeProxy(req, string(output))
}

func gnomeProxy(req *http.Request, settings string) (*url.URL, error) {
	values := map[string]string{}
	for _, line := range strings.Split(settings, "\n") {
		parts := strings.SplitN(line, " ", 3)
		if len(parts) == 3 {
			values[parts[0]+" "+parts[1]] = strings.Trim(parts[2], " '")
		}
	}
	switch values["org.gnome.system.proxy mode"] {
	case "auto":
		return nil, errors.New("此 Linux 构建暂不解析桌面 PAC，请启用主机 TUN 或设置 HTTPS_PROXY 后继续下载")
	case "manual":
		address := func(protocol string) string {
			prefix := "org.gnome.system.proxy." + protocol + " "
			host, port := values[prefix+"host"], values[prefix+"port"]
			if host == "" || port == "0" || port == "" {
				return ""
			}
			scheme := "http://"
			if protocol == "socks" {
				scheme = "socks5://"
			}
			return scheme + net.JoinHostPort(host, port)
		}
		httpProxy, httpsProxy, socks := address("http"), address("https"), address("socks")
		if values["org.gnome.system.proxy use-same-proxy"] == "true" {
			httpsProxy = httpProxy
		}
		if httpProxy == "" {
			httpProxy = socks
		}
		if httpsProxy == "" {
			httpsProxy = socks
		}
		bypass := strings.Trim(values["org.gnome.system.proxy ignore-hosts"], "[]")
		var hosts []string
		for _, entry := range strings.Split(bypass, ",") {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			if value, err := strconv.Unquote(strings.ReplaceAll(entry, "'", "\"")); err == nil {
				hosts = append(hosts, value)
			}
		}
		return staticProxy(req, httpProxy, httpsProxy, strings.Join(hosts, ","))
	default:
		return nil, nil
	}
}
