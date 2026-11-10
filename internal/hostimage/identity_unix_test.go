//go:build !windows

package hostimage

import "testing"

func TestUbuntu2204HostIdentity(t *testing.T) {
	identity, err := linuxIdentity("ID=ubuntu\nVERSION_ID=\"22.04\"\nPRETTY_NAME=\"Ubuntu 22.04.5 LTS\"\n")
	if err != nil || identity.System != "ubuntu-22.04" || identity.Version != "22.04" {
		t.Fatalf("identity: %+v %v", identity, err)
	}
}
