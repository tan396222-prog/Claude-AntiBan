package workbench

import (
	"context"
	"os/exec"

	"al.essio.dev/pkg/shellescape"
)

func (s *Server) macOSPhoneBrowserClient(parent context.Context) *windowsBrowserClient {
	ctx, cancel := context.WithCancel(parent)
	return &windowsBrowserClient{ctx: ctx, cancel: cancel, start: func(ctx context.Context) (*exec.Cmd, error) {
		const base = "/usr/local/libexec/cage-workbench-browser-4.145.0-pw1.65.0/"
		configuration, err := loadConfig(s.configPath)
		if err != nil {
			return nil, err
		}
		command := "exec " + shellescape.QuoteCommand([]string{"env", "LANG=" + configuration.Locale, "LC_ALL=" + configuration.Locale, base + "editor/lib/node", base + "controller/macos-phone-browser.mjs"})
		return s.macOSSSHCommand(ctx, "-T", s.sshTarget(), "--", command), nil
	}}
}
