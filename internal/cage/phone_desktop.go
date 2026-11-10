package cage

import (
	"embed"
	"fmt"
	"strings"
)

//go:embed phone-desktop/control.py phone-desktop/session.sh phone-desktop/xdg-open phone-desktop/view.py
//go:embed phone-desktop/handler.desktop phone-desktop/openbox.xml phone-desktop/network.mjs browser-phone-desktop.mjs
var phoneDesktopFiles embed.FS

// PhoneDesktopProvisionScript installs capture and input for the shared
// desktop window. The desktop service owns the app and its login data.
func PhoneDesktopProvisionScript() string {
	var s strings.Builder
	s.WriteString(`
sudo install -d -m 0755 /usr/local/lib/cage-phone-desktop
if [ -x /usr/bin/claude-desktop ] && { ! command -v xsettingsd >/dev/null || ! /usr/bin/python3 -c 'from PIL import Image; from Xlib.ext import composite,xtest' 2>/dev/null; }; then
  sudo env DEBIAN_FRONTEND=noninteractive APT_CONFIG="${CAGE_APT_CONFIG:-/dev/null}" apt-get install -y --no-install-recommends python3-pil python3-xlib xsettingsd ||
    printf '%s\n' '手机 Desktop 组件暂未就绪，可在电脑上修复 Desktop 安装。' >&2
fi
`)
	for _, name := range []string{"control.py", "session.sh", "xdg-open", "handler.desktop", "openbox.xml", "view.py", "network.mjs", "browser-phone-desktop.mjs"} {
		source, target := "phone-desktop/"+name, "/usr/local/lib/cage-phone-desktop/"+name
		if name == "browser-phone-desktop.mjs" {
			source, target = name, "/usr/local/lib/"+name
		}
		b, err := phoneDesktopFiles.ReadFile(source)
		if err != nil {
			panic(err)
		}
		fmt.Fprintf(&s, "sudo tee %s >/dev/null <<'CAGE_PHONE_DESKTOP_FILE'\n%s\nCAGE_PHONE_DESKTOP_FILE\n", target, b)
	}
	s.WriteString(`sudo chmod 0755 /usr/local/lib/cage-phone-desktop/{control.py,session.sh,xdg-open}
printf '%s\n' 'cage-browser ALL=(root) NOPASSWD: /usr/local/lib/cage-phone-desktop/control.py ""' | sudo tee /etc/sudoers.d/cage-phone-desktop >/dev/null
sudo chmod 0440 /etc/sudoers.d/cage-phone-desktop
`)
	return s.String()
}
