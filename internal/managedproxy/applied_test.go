package managedproxy

import "testing"

func TestEndpointAloneCannotAuthorizeNetworking(t *testing.T) {
	profile := t.TempDir()
	p, _ := Load(profile)
	if _, err := Applied(profile, "socks5://127.0.0.1:17896", ""); err == nil {
		t.Fatal("old endpoint accepted without nodes")
	}
	if err := p.Import("qa", "", []byte(`proxies: [{name: qa, type: socks5, server: 127.0.0.1, port: 1080}]`), "qa", 0, ""); err != nil {
		t.Fatal(err)
	}
	p.Revision = "applied"
	p.Selected = p.Sources[0].Nodes[0].ID
	if err := p.Save(profile); err != nil {
		t.Fatal(err)
	}
	for _, input := range [][2]string{{p.Endpoint(), ""}, {p.Endpoint(), "old"}, {"socks5://127.0.0.1:7890", p.Revision}} {
		if _, err := Applied(profile, input[0], input[1]); err == nil {
			t.Fatal("unapplied endpoint accepted")
		}
	}
	if _, err := Applied(profile, p.Endpoint(), p.Revision); err != nil {
		t.Fatal(err)
	}
	p.Selected = ""
	_ = p.Save(profile)
	if _, err := Applied(profile, p.Endpoint(), p.Revision); err == nil {
		t.Fatal("removed node retained networking authorization")
	}
}
