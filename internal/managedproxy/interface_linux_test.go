package managedproxy

import "testing"

func TestLinuxPhysicalRouteSkipsTUNAndUsesLowestMetric(t *testing.T) {
	const routes = `Iface Destination Gateway Flags RefCnt Use Metric Mask MTU Window IRTT
tun0 00000000 00000000 0001 0 0 0 00000000 0 0 0
ens5 00000000 0100000A 0003 0 0 100 00000000 0 0 0
eth1 00000000 0100000A 0003 0 0 20 00000000 0 0 0
eth2 00000000 0100000A 0203 0 0 1 00000000 0 0 0
eth3 00000000 0100000A 0002 0 0 1 00000000 0 0 0
eth4 00000000 0100000A 0003 0 0 1 0000FFFF 0 0 0
`
	for _, tc := range []struct {
		name   string
		usable map[string]bool
		want   string
	}{
		{"physical metric", map[string]bool{"ens5": true, "eth1": true, "eth2": true, "eth3": true, "eth4": true}, "eth1"},
		{"cloud virtio", map[string]bool{"ens5": true}, "ens5"},
		{"disconnected physical interfaces", map[string]bool{}, ""},
		{"reject and inactive routes", map[string]bool{"eth2": true, "eth3": true, "eth4": true}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := selectLinuxPhysicalInterface(routes, tc.usable)
			if got != tc.want || (err != nil) != (tc.want == "") {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
