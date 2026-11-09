package containerenv

import "testing"

func TestMountPropagationDistinguishesPrivateAndIsolatedSharedRoot(t *testing.T) {
	host := "24 1 8:1 / / rw,relatime shared:1 - ext4 /dev/root rw\n"
	private := "755 705 0:65 / / rw,relatime - overlay overlay rw\n"
	state, err := rootMountPropagation(private, host)
	if err != nil || state.SharedGroup != 0 {
		t.Fatalf("private root: %+v %v", state, err)
	}
	shared := "755 705 0:65 / / rw,relatime shared:457 - overlay overlay rw\n"
	state, err = rootMountPropagation(shared, host)
	if err != nil || state.SharedGroup != 457 || !state.DetachedFromHost {
		t.Fatalf("isolated root: %+v %v", state, err)
	}
	for _, unsafe := range []string{
		"755 705 0:65 / / rw,relatime shared:457 master:1 - overlay overlay rw\n",
		"755 705 0:65 / / rw,relatime shared:1 - overlay overlay rw\n",
		"755 705 0:65 / / rw,relatime shared:broken - overlay overlay rw\n",
	} {
		if _, err := rootMountPropagation(unsafe, host); err == nil {
			t.Fatalf("accepted unsafe propagation: %s", unsafe)
		}
	}
}
