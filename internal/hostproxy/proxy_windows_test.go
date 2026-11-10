package hostproxy

import (
	"net/http"
	"testing"
)

func TestWindowsSystemProxyMapping(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://cloud.debian.org/image", nil)
	for _, list := range []string{"127.0.0.1:7890", "http=127.0.0.1:7891;https=127.0.0.1:7890"} {
		u, err := windowsProxy(req, list, "localhost;127.*")
		if err != nil || u == nil || u.String() != "http://127.0.0.1:7890" {
			t.Fatalf("proxy=%v err=%v", u, err)
		}
	}
}
