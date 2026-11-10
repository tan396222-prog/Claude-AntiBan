package workbench

import "strings"

// These operations are owned by shared workbench services. Guest-specific
// routes add only operations with a platform-specific receiver.
func nativeWorkbenchRoute(path string) bool {
	if !strings.HasPrefix(path, "/api/") {
		return true
	}
	switch path {
	case "/api/agent/settings", "/api/agent/routes":
		return true
	case "/api/account", "/api/session", "/api/launch", "/api/handoff", "/api/remote/manifest",
		"/api/appearance", "/api/favorites", "/api/status", "/api/settings", "/api/settings/probe",
		"/api/start", "/api/stop", "/api/restore", "/api/host/git", "/api/projects/open", "/api/projects/remove", "/api/projects/directories",
		"/api/environment/initialization", "/api/environment/versions", "/api/environment/update", "/api/environment/tools", "/api/environment/resources", "/api/environment/storage",
		"/api/environment/storage/recover",
		"/api/terminals", "/api/claude-accounts", "/api/cli/sessions", "/api/proxy", "/api/vault", "/api/vault/browser", "/api/client/browser-extensions",
		"/api/input", "/api/input/text", "/api/input/image", "/api/input/paste/start", "/api/input/paste/cancel", "/api/input/files/start", "/api/input/files/finish", "/api/input/files/cancel", "/api/input/files/directory", "/api/input/files/download-retry",
		"/api/vnc", "/api/vnc-credentials", "/api/audio", "/api/clipboard", "/api/clipboard/copy", "/api/clipboard/files",
		"/api/tailscale", "/api/mobile-pair", "/api/mobile/device", "/api/safety/check", "/api/safety/close", "/api/safety/report":
		return true
	}
	for _, prefix := range []string{
		"/api/agent/jobs/",
		"/api/account/", "/api/terminals/", "/api/claude-accounts/", "/api/cli/sessions/", "/api/proxy/",
		"/api/browser/", "/api/clipboard/files/", "/api/input/files/browser/", "/api/input/files/terminal/",
		"/api/tailscale/", "/api/mobile/cli/", "/api/mobile/browser/", "/api/mobile/desktop/", "/api/diagnostics",
	} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}
