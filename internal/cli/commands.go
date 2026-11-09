package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sylvinus/agent-vm/internal/paths"
	"github.com/sylvinus/agent-vm/internal/state"
	"github.com/sylvinus/agent-vm/internal/ui"
	"github.com/sylvinus/agent-vm/internal/vm"
	"github.com/sylvinus/agent-vm/internal/vmname"
)

// session is what runs in the VM once it is up: an agent, a shell, a
// command. It returns the exit status.
type session func(s *start) int

// endedBySignal is ctx for a run that a kill or a closed terminal (SIGTERM,
// SIGHUP) must end: the command in the VM stops with it, and the cleanup
// that follows (--rm, --scratch) runs on a context that does not end. A
// Ctrl-C is not one: see inVM. Signals after the first are swallowed until
// stop, so that the cleanup is not cut short.
func endedBySignal(parent context.Context) (ctx context.Context, stop func()) {
	ctx, cancel := context.WithCancel(parent)
	sig := make(chan os.Signal, 4)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGHUP)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-sig:
				cancel()
			case <-done:
				return
			}
		}
	}()
	return ctx, func() {
		signal.Stop(sig)
		close(done)
		cancel()
	}
}

// inVM starts this directory's VM with opts, runs fn, then deletes the VM
// when --rm asked: after fn whatever it returns, and after a start that
// failed when this run made the VM (a VM that was there before is not
// deleted for a start that did not go through). A kill or a closed terminal
// (SIGTERM, SIGHUP) ends the command and still deletes. --scratch: see
// scratch.
func (e *app) inVM(ctx context.Context, opts VMOpts, fn session) int {
	dir, err := vmname.AbsDir("")
	if err != nil {
		fmt.Fprintf(e.io.Stderr, "Error: %v\n", err)
		return 1
	}
	cleanup := context.WithoutCancel(ctx)
	ctx, stop := endedBySignal(ctx)
	defer stop()
	if opts.Scratch {
		return e.scratch(ctx, cleanup, opts, dir, fn)
	}
	s := &start{app: e, ctx: ctx, opts: opts, dir: dir, name: e.instanceName(dir)}
	if os.Getenv("CLAUDE_CAGE_CONFIG") != "" {
		b, err := s.backend()
		if err != nil {
			fmt.Fprintln(e.io.Stderr, err)
			return 1
		}
		instances, err := b.List(ctx)
		if err != nil {
			fmt.Fprintln(e.io.Stderr, err)
			return 1
		}
		for _, inst := range instances {
			if inst.Status == vm.Running && inst.Name != s.name {
				fmt.Fprintf(e.io.Stderr, "Cage uses one persistent environment per profile; stop legacy VM %s before connecting it.\n", inst.Name)
				return 1
			}
		}
	}
	st := 1
	started := s.ensureRunning() == nil
	if started {
		s.printResources()
		// A Ctrl-C is the command's, in the VM, as a shell leaves it to the job
		// it waits on: this run goes on to --rm and the terminal's reset.
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt)
		st = fn(s)
		signal.Stop(sig)
		ui.ResetTermModes(os.Stdout)
	}
	if opts.RM && (started || s.created) && !e.removeVM(cleanup, s.name) && st == 0 {
		st = 1
	}
	return st
}

// printResources says what the VM has, and when the base it was cloned
// from was built: its agents and packages are that old.
func (s *start) printResources() {
	if inst, err := s.get(s.name); err == nil && inst != nil {
		fmt.Fprintf(s.io.Stdout, "  Resources: CPUs: %d, Memory: %d GiB, Disk: %d GiB\n", inst.CPUs, inst.Memory>>30, inst.Disk>>30)
	}
	if built, err := strconv.ParseInt(s.state.ReadMarker(state.VersionOf, s.name), 10, 64); err == nil && built >= 0 {
		fmt.Fprintf(s.io.Stdout, "  Base VM: built %s\n", time.Unix(built, 0).Format("2006-01-02"))
	}
}

// lima runs args in the VM through a login zsh, from the project: agent PATH
// entries and ~/.agent-vm.env are in ~/.zshenv. The command and its
// arguments are positional parameters, never re-parsed; `env --` runs it,
// leading VAR=value assignments working and a command starting with - not
// taken for env's option. A command that is not there is said so, with
// env's status (an agent installed only as an editor extension has none);
// a path is env's to run, and to say why it cannot (126).
func (s *start) lima(tty bool, args ...string) int {
	b, err := s.backend()
	if err != nil {
		return 1
	}
	code, err := b.Shell(s.ctx, s.name, vm.ShellOpts{
		Workdir: paths.Guest(s.dir),
		Args: append([]string{"zsh", "-l", "-c",
			`case "$1" in *=*|*/*) ;; *) command -v -- "$1" >/dev/null || { print -r -- "agent-vm: $1 is not installed in this VM." >&2; exit 127; } ;; esac; exec env -- "$@"`,
			"agent-vm"}, args...),
		Stdin: s.io.Stdin, Stdout: s.io.Stdout, Stderr: s.io.Stderr, Interactive: tty,
	})
	if err != nil {
		s.warnf("Error: %v", err)
		return 1
	}
	return code
}

// The agents, with the flag that lets each work unattended (safe in the
// VM), and a terminal for the full-screen ones.
var agents = map[string]struct {
	cmd []string
	tty bool
}{
	// The VM also enforces bypass mode with managed settings: Claude Code
	// drops the flag when it relaunches itself.
	"claude": {[]string{"claude", "--dangerously-skip-permissions"}, false},
	// Approves every prompt that is not explicitly denied.
	"opencode": {[]string{"opencode", "--auto"}, true},
	"codex":    {[]string{"codex", "--dangerously-bypass-approvals-and-sandbox"}, false},
	"vibe":     {[]string{"vibe", "--agent", "auto-approve"}, true},
	// No permission prompts: it runs every tool as asked.
	"pi": {[]string{"pi"}, true},
}

func (e *app) agentCmd(ctx context.Context, agent string, opts VMOpts, args []string) int {
	a := agents[agent]
	rest, tty, err := opts.split(args)
	if err != nil {
		fmt.Fprintf(e.io.Stderr, "Error: %v\n", err)
		return exitUsage
	}
	cmd := append(append([]string(nil), a.cmd...), rest...)
	return e.inVM(ctx, opts, func(s *start) int { return s.lima(a.tty || tty, cmd...) })
}

// `run [--tty] <command> [args]`: --tty gives the command a terminal, which
// full-screen programs need to draw.
func (e *app) runCmd(ctx context.Context, opts VMOpts, args []string) int {
	rest, tty, err := opts.split(args)
	if err != nil {
		fmt.Fprintf(e.io.Stderr, "Error: %v\n", err)
		return exitUsage
	}
	if len(rest) == 0 {
		fmt.Fprintln(e.io.Stderr, "Usage: agent-vm run [--tty] <command> [args]")
		return exitUsage
	}
	return e.inVM(ctx, opts, func(s *start) int { return s.lima(tty, rest...) })
}

// `shell [-c "command"]`: a login zsh in the VM, ~/.zshenv loaded. A word
// it does not know is an error, not something skipped.
func (e *app) shellCmd(ctx context.Context, opts VMOpts, args []string) int {
	command := ""
	for len(args) > 0 {
		n, err := opts.take(args)
		if err != nil {
			fmt.Fprintf(e.io.Stderr, "Error: %v\n", err)
			return exitUsage
		}
		if n > 0 {
			args = args[n:]
			continue
		}
		switch args[0] {
		case "-c", "--command":
			if len(args) < 2 || args[1] == "" {
				fmt.Fprintln(e.io.Stderr, "Error: -c/--command requires a command string.")
				return exitUsage
			}
			command, args = args[1], args[2:]
		default:
			fmt.Fprintf(e.io.Stderr, "Error: unknown argument for shell: %s\nUsage: agent-vm [vm options] shell [-c \"command\"]\n", args[0])
			return exitUsage
		}
	}
	return e.inVM(ctx, opts, func(s *start) int {
		if command != "" {
			return s.shell(false, "zsh", "-l", "-c", command)
		}
		fmt.Fprintf(s.io.Stdout, "VM: %s | Dir: %s\n", s.name, s.dir)
		if s.opts.RM {
			fmt.Fprintln(s.io.Stdout, "Type 'exit' to leave. VM will be destroyed after exit.")
		} else {
			fmt.Fprintln(s.io.Stdout, "Type 'exit' to leave (VM keeps running). Use 'agent-vm stop' to stop it.")
		}
		return s.shell(true, "zsh", "-l")
	})
}

// shellTo runs args in the VM from the project, on the terminal.
func (s *start) shellTo(tty bool, args ...string) (int, error) {
	b, err := s.backend()
	if err != nil {
		return 1, err
	}
	return b.Shell(s.ctx, s.name, vm.ShellOpts{Workdir: paths.Guest(s.dir), Args: args,
		Stdin: s.io.Stdin, Stdout: s.io.Stdout, Stderr: s.io.Stderr, Interactive: tty})
}

// shell is shellTo's exit status. When the shell could not run at all (the
// VM is not running, ssh fails), the reason is said and the status is 1: a
// caller must not read that as the command's success.
func (s *start) shell(tty bool, args ...string) int {
	code, err := s.shellTo(tty, args...)
	if err != nil {
		s.warnf("Error: %v", err)
		return 1
	}
	return code
}

// scratch: a new VM of its own, sharing nothing, deleted once fn returns,
// whatever it returns, unless the user keeps it a while to look inside. A
// Ctrl-C ends the command in the VM, not this run, which goes on to the
// deletion. A run killed outright leaves its VM recorded with its pid, and
// the next scratch run deletes it. cleanup is the context of the deletions,
// which a kill of this run (ctx) does not end.
func (e *app) scratch(ctx, cleanup context.Context, opts VMOpts, dir string, fn session) int {
	dead, unknown := e.scratchLeftovers()
	for _, left := range dead {
		fmt.Fprintf(e.io.Stdout, "Deleting scratch VM '%s', left by a run that did not finish...\n", left)
		e.deleteVM(cleanup, left)
	}
	for _, left := range unknown {
		fmt.Fprintf(e.io.Stderr, "Note: scratch VM '%s' has a record without a process number, so it is not known whether a run still uses it: left alone. 'agent-vm rm %s' deletes it.\n", left, left)
	}
	name, err := e.names.Scratch(dir)
	if err != nil {
		fmt.Fprintf(e.io.Stderr, "Error: %v\n", err)
		return 1
	}
	if err := e.state.Write(state.ScratchOf+name, []byte(strconv.Itoa(os.Getpid())+"\n")); err != nil {
		fmt.Fprintf(e.io.Stderr, "Error: could not record scratch VM '%s': %v\n", name, err)
		return 1
	}
	opts.RM = true
	s := &start{app: e, ctx: ctx, opts: opts, dir: dir, name: name}
	st := 1
	if s.ensureRunning() == nil {
		s.printResources()
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt)
		st = fn(s)
		signal.Stop(sig)
		ui.ResetTermModes(os.Stdout)
		// Asked before the deletion, when it can be (yes by default): a no
		// opens a shell in the VM, to look at what the command left, and
		// asks again on its exit. No terminal, a question cut short, or a
		// kill, deletes.
		for ctx.Err() == nil && e.ui.CanAsk() && !e.ui.AskYN(fmt.Sprintf("Delete scratch VM '%s'?", name), true) {
			fmt.Fprintf(e.io.Stdout, "A shell in scratch VM '%s'. Type 'exit' to be asked again.\n", name)
			s.shell(true, "zsh", "-l")
			ui.ResetTermModes(os.Stdout)
		}
	}
	fmt.Fprintf(e.io.Stdout, "Deleting scratch VM '%s'...\n", name)
	if !e.deleteVM(cleanup, name) {
		st = 1
	}
	return st
}

// scratchLeftovers are the scratch VMs a run could not delete (killed, its
// terminal closed), by their record: dead are those recorded by a run whose
// pid is gone (a reused pid only delays the cleanup), unknown those whose
// record holds no pid, which is no proof that no run uses them. Only names
// Scratch makes.
func (e *app) scratchLeftovers() (dead, unknown []string) {
	ents, _ := os.ReadDir(string(e.state))
	for _, f := range ents {
		name, ok := strings.CutPrefix(f.Name(), state.ScratchOf)
		if !ok || !vmname.IsScratch(name) {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(e.state.Read(f.Name())))
		switch {
		case err != nil || pid <= 0:
			unknown = append(unknown, name)
		case !alive(pid):
			dead = append(dead, name)
		}
	}
	return dead, unknown
}
