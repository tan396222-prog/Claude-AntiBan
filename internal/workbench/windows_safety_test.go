package workbench

import (
	"testing"

	"github.com/sylvinus/agent-vm/internal/cage"
)

func TestWindowsSettingsKeepIndependentBrowserLanguage(t *testing.T) {
	cfg := cage.DefaultConfig()
	cfg.Locale = "zh_CN.UTF-8"
	cfg.BrowserLanguage = "en-GB"
	input := windowsGuestSettingsInput(cfg)
	if input["locale"] != cfg.Locale || input["browser_language"] != cfg.BrowserLanguage {
		t.Fatal("system locale replaced the independently selected browser language")
	}
}

func TestWindowsObservationRejectsRPCFailure(t *testing.T) {
	for _, data := range []string{`{"error":"guest_operation_failed"}`, `{}`, `{"error":"safety_probe_failed","stage":"shared-read","error_code":"file_io"}`} {
		if _, err := decodeWindowsObservation([]byte(data)); err == nil {
			t.Fatal("failed or empty RPC became a successful observation")
		}
	}
	data := []byte(`{"tz":"Etc/UTC","system_timezone":"Etc/UTC","process_locale":"en-US","system_locale":"en-US","locale_valid":true,"shared_file":true}`)
	result, err := decodeWindowsObservation(data)
	if err != nil || !result.SharedFile {
		t.Fatal("actual system result rejected")
	}
}
