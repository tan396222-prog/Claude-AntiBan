//go:build !windows

package hostimage

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

func Host(ctx context.Context) (Identity, error) {
	if runtime.GOOS == "darwin" {
		output, err := exec.CommandContext(ctx, "/usr/bin/sw_vers").Output()
		if err != nil {
			return Identity{}, err
		}
		identity := Identity{System: "macos", Arch: runtime.GOARCH}
		for _, line := range strings.Split(string(output), "\n") {
			key, value, _ := strings.Cut(line, ":")
			switch key {
			case "ProductVersion":
				identity.Version = strings.TrimSpace(value)
			case "BuildVersion":
				identity.Build = strings.TrimSpace(value)
			}
		}
		return identity, nil
	}
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return Identity{}, err
	}
	return linuxIdentity(string(data))
}

func linuxIdentity(data string) (Identity, error) {
	values := make(map[string]string)
	for _, line := range strings.Split(data, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if strings.HasPrefix(value, `"`) {
			unquoted, err := strconv.Unquote(value)
			if err != nil {
				return Identity{}, err
			}
			value = unquoted
		} else {
			value = strings.Trim(value, "'")
		}
		values[key] = value
	}
	version := values["VERSION_ID"]
	if values["ID"] == "debian" {
		version = strings.SplitN(version, ".", 2)[0]
	}
	identity := Identity{System: values["ID"] + "-" + version, Version: version, Arch: runtime.GOARCH}
	switch identity.System {
	case "debian-12", "debian-13", "ubuntu-22.04", "ubuntu-24.04":
		return identity, nil
	default:
		return Identity{}, fmt.Errorf("当前尚未适配 %s 的本机系统创建，仍可选择 Debian 13", values["PRETTY_NAME"])
	}
}
