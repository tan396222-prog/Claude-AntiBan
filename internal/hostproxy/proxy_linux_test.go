package hostproxy

import (
	"net/http"
	"testing"
)

func TestGnomeProxyManualAndPACFailure(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://cloud.debian.org/image", nil)
	u, err := gnomeProxy(req, "org.gnome.system.proxy mode 'manual'\norg.gnome.system.proxy.https host '127.0.0.1'\norg.gnome.system.proxy.https port 7890\norg.gnome.system.proxy ignore-hosts ['localhost', '127.0.0.1']")
	if err != nil || u == nil || u.String() != "http://127.0.0.1:7890" {
		t.Fatalf("proxy=%v err=%v", u, err)
	}
	if _, err := gnomeProxy(req, "org.gnome.system.proxy mode 'auto'"); err == nil {
		t.Fatal("unsupported PAC became direct")
	}
}
