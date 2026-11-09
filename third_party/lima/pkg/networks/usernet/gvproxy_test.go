// SPDX-FileCopyrightText: Copyright The Lima Authors
// SPDX-License-Identifier: Apache-2.0

package usernet

import (
	"bufio"
	"net/netip"
	"os"
	"path"
	"testing"

	"gotest.tools/v3/assert"
)

func TestSearchDomain(t *testing.T) {
	t.Run("search domain", func(t *testing.T) {
		resolvFile := path.Join(t.TempDir(), "resolv.conf")
		createResolveFile(t, resolvFile, `
search test.com lima.net
nameserver 192.168.0.100
nameserver 8.8.8.8`)

		dns := resolveSearchDomain(resolvFile)
		assert.DeepEqual(t, dns, []string{"test.com", "lima.net"})
	})

	t.Run("empty search domain", func(t *testing.T) {
		resolvFile := path.Join(t.TempDir(), "resolv.conf")
		createResolveFile(t, resolvFile, `
nameserver 192.168.0.100
nameserver 8.8.8.8`)

		dns := resolveSearchDomain(resolvFile)
		var expected []string
		assert.DeepEqual(t, dns, expected)
	})
}

func createResolveFile(t *testing.T, file, content string) {
	f, err := os.Create(file)
	assert.NilError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	writer := bufio.NewWriter(f)
	_, err = writer.WriteString(content)
	assert.NilError(t, err)
	err = writer.Flush()
	assert.NilError(t, err)
}

// A host service is reached on 127.0.0.1, over its network and port only.
func TestOutboundHostServices(t *testing.T) {
	saved := Outbound
	Outbound = func(string, netip.AddrPort) bool { return false }
	t.Cleanup(func() { Outbound = saved })
	AllowHostService("udp", 54321)
	t.Cleanup(func() { hostServices.Delete(hostService{"udp", 54321}) })

	for _, tc := range []struct {
		network, dst string
		want         bool
	}{
		{"udp", "127.0.0.1:54321", true},
		{"udp", "[::ffff:127.0.0.1]:54321", true},
		{"tcp", "127.0.0.1:54321", false},
		{"udp", "127.0.0.1:54322", false},
		{"udp", "127.0.0.2:54321", false},
		{"udp", "[::1]:54321", false},
	} {
		assert.Equal(t, outbound(tc.network, netip.MustParseAddrPort(tc.dst)), tc.want, "%s %s", tc.network, tc.dst)
	}
}
