package workbench

import (
	"encoding/json"
	"github.com/sylvinus/agent-vm/internal/paths"
	"testing"
)

func TestWindowsGuestPathsMatchVMSharePaths(t *testing.T) {
	previous := paths.Windows
	paths.Windows = func() bool { return true }
	t.Cleanup(func() { paths.Windows = previous })
	for _, path := range []string{`C:\Users\甲 用户\project`, "C:/Users/甲 用户/project"} {
		if got := guestPath(path); got != "/c/Users/甲 用户/project" {
			t.Fatalf("guestPath(%q) = %q", path, got)
		}
	}
}

func TestBoundaryProbeUsesGuestFilePaths(t *testing.T) {
	previous := paths.Windows
	t.Cleanup(func() { paths.Windows = previous })
	for _, tc := range []struct {
		name                                     string
		windows                                  bool
		private, shared, wantPrivate, wantShared string
	}{
		{"Windows backslashes and another drive", true, `C:\Users\甲 用户\Cage\private-probe`, `D:\项目 工作区\shared-probe`, "/c/Users/甲 用户/Cage/private-probe", "/d/项目 工作区/shared-probe"},
		{"Windows forward slashes", true, "C:/Users/user/Cage/private-probe", "C:/Users/user/work/shared-probe", "/c/Users/user/Cage/private-probe", "/c/Users/user/work/shared-probe"},
		{"POSIX", false, "/Users/user/Cage/private-probe", "/Users/user/work/shared-probe", "/Users/user/Cage/private-probe", "/Users/user/work/shared-probe"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths.Windows = func() bool { return tc.windows }
			wire, err := json.Marshal(boundaryProbeInput(17777, "check-id", tc.private, tc.shared, "challenge"))
			if err != nil {
				t.Fatal(err)
			}
			var input struct {
				Stage     string `json:"stage"`
				Port      int    `json:"control_port"`
				Nonce     string `json:"nonce"`
				Private   string `json:"private_file"`
				Shared    string `json:"shared_file"`
				Challenge string `json:"challenge"`
			}
			if err := json.Unmarshal(wire, &input); err != nil {
				t.Fatal(err)
			}
			if input.Private != tc.wantPrivate || input.Shared != tc.wantShared {
				t.Fatalf("Linux file probe received host paths: private=%q shared=%q", input.Private, input.Shared)
			}
			if input.Stage != "boundary" || input.Port != 17777 || input.Nonce != "check-id" || input.Challenge != "challenge" {
				t.Fatalf("path conversion changed the probe identity: %+v", input)
			}
		})
	}
}
