package managedproxy

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func linuxPhysicalInterface(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	routes, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return "", errors.New("无法读取 Linux IPv4 路由，请检查主机网络后重试")
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	usable := map[string]bool{}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		// PCI, USB and virtio NICs have a device parent. TUN, WireGuard,
		// bridges and container veth pairs do not. Virtio cloud NICs qualify.
		if _, err := os.Stat(filepath.Join("/sys/class/net", iface.Name, "device")); err != nil {
			continue
		}
		addresses, _ := iface.Addrs()
		for _, address := range addresses {
			ip, _, err := net.ParseCIDR(address.String())
			if err == nil && ip.To4() != nil && !ip.IsLinkLocalUnicast() && !ip.IsUnspecified() {
				usable[iface.Name] = true
			}
		}
	}
	return selectLinuxPhysicalInterface(string(routes), usable)
}

func selectLinuxPhysicalInterface(routes string, usable map[string]bool) (string, error) {
	selected := ""
	var best uint64
	for _, line := range strings.Split(routes, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 8 || !usable[fields[0]] || fields[1] != "00000000" || fields[7] != "00000000" {
			continue
		}
		flags, flagErr := strconv.ParseUint(fields[3], 16, 32)
		metric, metricErr := strconv.ParseUint(fields[6], 10, 64)
		// Require an active default route; reject unreachable routes.
		if flagErr != nil || metricErr != nil || flags&1 == 0 || flags&0x200 != 0 {
			continue
		}
		if selected == "" || metric < best || metric == best && fields[0] < selected {
			selected, best = fields[0], metric
		}
	}
	if selected == "" {
		return "", errors.New("未找到带 IPv4 默认路由的物理或云主机网卡；Cage 外网保持关闭，请连接网络后重试")
	}
	return selected, nil
}
