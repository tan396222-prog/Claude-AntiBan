package workbench

import (
	"context"
	"io"
	"path/filepath"

	"al.essio.dev/pkg/shellescape"
)

// RunEnvironmentCommand shares the real SSH transport used by the workbench.
// Arguments are explicitly supplied by a local CLI user, never by a web page.
func RunEnvironmentCommand(ctx context.Context, configPath string, args []string, stdin io.Reader, stdout, stderr io.Writer, interactive bool) error {
	s := &Server{profile: filepath.Dir(configPath), configPath: configPath}
	command := "cd -- " + shellescape.Quote(guestPath(s.environmentDir())) + " && exec " + shellescape.QuoteCommand(args)
	terminal := "-T"
	if interactive {
		terminal = "-tt"
	}
	cmd := s.sshCommand(ctx, terminal, s.sshTarget(), "--", "exec /bin/bash -lc "+shellescape.Quote(command))
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	return cmd.Run()
}
