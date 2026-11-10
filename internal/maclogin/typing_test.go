package maclogin

import "testing"

func TestPasswordKeysKeepPrintableKeysSeparateFromSubmit(t *testing.T) {
	keys, ok := passwordKeys("Aa7! _-\\\"[]{}")
	if !ok || len(keys) != 13 {
		t.Fatalf("password encoding failed: %v %d", ok, len(keys))
	}
	if keys[0] != (loginKey{0, true}) || keys[1] != (loginKey{0, false}) || keys[3] != (loginKey{18, true}) {
		t.Fatal("shifted keys lost their physical code")
	}
	for _, key := range keys {
		if key.code == 36 || key.code == 48 || key.code == 53 {
			t.Fatal("password emitted a submit or navigation key")
		}
	}
	for _, value := range []string{"password\nreturn", "password\x00", "密码password"} {
		if _, ok := passwordKeys(value); ok {
			t.Fatal("accepted control or unsupported character")
		}
	}
	if _, ok := passwordKeys("short"); ok {
		t.Fatal("accepted invalid length")
	}
}
