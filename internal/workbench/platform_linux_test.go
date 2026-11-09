package workbench

import "testing"

func TestLinuxUsesNativeKeyboardByDefault(t *testing.T) {
	if got := defaultKeyboard(); got != "native" {
		t.Fatalf("Linux desktop must use native Ctrl shortcuts, got %q", got)
	}
}
