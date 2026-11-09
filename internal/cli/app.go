package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/paths"
	"github.com/sylvinus/agent-vm/internal/state"
	"github.com/sylvinus/agent-vm/internal/ui"
	"github.com/sylvinus/agent-vm/internal/vm"
	"github.com/sylvinus/agent-vm/internal/vmname"
)

// app is what the commands work with.
type app struct {
	io             IO
	ui             *ui.UI
	state          state.Dir
	privateStorage []string
	names          vmname.Limit
	cageProfile    string

	migrateOnce sync.Once
	migrate     func() // moves 0.2's VMs, when set
	backOnce    sync.Once
	newBack     func() (vm.Backend, error)
	back        vm.Backend
	backErr     error
}

// backend is made on first use: most commands never need Lima. The first
// use by a command that changes VMs also moves 0.2's VMs (migrateOld).
func (e *app) backend() (vm.Backend, error) {
	e.migrateOnce.Do(func() {
		if e.migrate != nil {
			e.migrate()
		}
	})
	return e.readOnlyBackend()
}

// readOnlyBackend is backend without the move of 0.2's VMs, for commands
// that only look (list, status, doctor, info, install). A command that goes
// on to change VMs asks for backend, which moves them then.
func (e *app) readOnlyBackend() (vm.Backend, error) {
	e.backOnce.Do(func() { e.back, e.backErr = e.newBack() })
	return e.back, e.backErr
}

// migratedMarker says the VMs of 0.2 were moved into agent-vm's Lima home.
const migratedMarker = ".agent-vm-lima-migrated"

// oldLimaHome is where 0.2 kept its VMs: Lima's home, $LIMA_HOME or ~/.lima.
func oldLimaHome() string {
	if h := vm.UserLimaHome(); h != "" {
		return paths.Host(absOr(h))
	}
	return paths.Host(filepath.Join(paths.Home(), ".lima"))
}

// absOr is p made absolute, as it is when that cannot be done: a relative
// folder would defeat the checks that compare locations.
func absOr(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// migrated reports whether 0.2's VMs were moved already.
func (e *app) migrated() bool {
	_, err := os.Stat(e.state.Path(migratedMarker))
	return err == nil
}

// unmovedOld are 0.2's VMs that have not been moved yet, and the Lima home
// they are in. None for an app with no move wired (migrate unset).
func (e *app) unmovedOld() ([]vm.OldVM, string) {
	old := oldLimaHome()
	if e.migrate == nil || e.migrated() || sameDir(old, LimaHome(e.state)) {
		return nil, old
	}
	vms, _ := vm.FindOld(old, vmname.OldPrefix)
	return vms, old
}

// migrateOld moves, once, the VMs agent-vm 0.2 made in the user's Lima home
// into its own (see LimaHome). Asked on a terminal, yes by default. Without
// one, the stopped ones are moved and the running ones left: moving stops
// them, which cuts sessions nobody agreed to cut. A VM that could not be
// moved stays, and is offered again.
func (e *app) migrateOld(ctx context.Context, old, now string) {
	if e.migrated() || sameDir(old, now) {
		return
	}
	vms, err := vm.FindOld(old, vmname.OldPrefix)
	if err != nil || len(vms) == 0 {
		return
	}
	w := e.io.Stderr
	fmt.Fprintf(w, "agent-vm now keeps its VMs in %s, apart from your own Lima's. Found in %s:\n", now, old)
	running := false
	for _, v := range vms {
		state := ""
		if v.Running {
			state, running = " (running)", true
		}
		fmt.Fprintf(w, "  %s, as %s%s\n", v.Name, e.names.FromOld(v.Name), state)
	}
	canAsk := e.ui.CanAsk()
	if running && canAsk {
		fmt.Fprintln(w, "Running ones are stopped first: sessions using them are cut.")
	}
	if canAsk && !e.ui.AskYN("Move them there now?", true) {
		fmt.Fprintf(w, "Not moved: this agent-vm does not see them until they are. They stay in %s.\n", old)
		return
	}
	failed := false
	for _, v := range vms {
		if v.Running && !canAsk {
			fmt.Fprintf(w, "%s is running and stays in %s: no terminal to ask whether to cut its sessions. Stop it, or run agent-vm in a terminal.\n", v.Name, old)
			failed = true
			continue
		}
		to := e.names.FromOld(v.Name)
		fmt.Fprintf(w, "Moving %s...\n", v.Name)
		if err := vm.MoveOld(ctx, old, now, v.Name, to); err != nil {
			fmt.Fprintf(w, "Error: could not move %s: %v. It stays in %s.\n", v.Name, err, old)
			failed = true
			continue
		}
		if err := e.state.Rename(v.Name, to); err != nil {
			fmt.Fprintf(w, "Warning: %s moved, but not all that agent-vm knows of it: %v\n", to, err)
		}
	}
	if failed {
		return
	}
	if err := e.state.Write(migratedMarker, []byte(old+"\n")); err != nil {
		fmt.Fprintf(w, "Warning: could not record that they were moved: %v. They are looked for in %s again next time.\n", err, old)
	}
	fmt.Fprintln(w, "Done: they keep their shares and settings, under their new names.")
}

// sameDir reports whether a and b are one folder, links resolved where they
// exist.
func sameDir(a, b string) bool {
	if fa, err := os.Stat(a); err == nil {
		if fb, err := os.Stat(b); err == nil {
			return os.SameFile(fa, fb)
		}
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// LimaHome is where agent-vm's Lima keeps its VMs: its own, so that the
// user's limactl, of another version, never runs them, and nothing of the
// user's own Lima is touched. AGENT_VM_LIMA_HOME overrides it, made
// absolute.
func LimaHome(st state.Dir) string {
	if h := os.Getenv("AGENT_VM_LIMA_HOME"); h != "" {
		return paths.Host(absOr(h))
	}
	return st.Path("lima")
}

func newApp(io IO) (*app, error) {
	st, err := state.Default()
	if err != nil {
		return nil, err
	}
	u := ui.New()
	u.Stderr = io.Stderr
	e := &app{io: io, ui: u, state: st}
	// Names that fit this machine's socket paths (a long home, a long user
	// name): cut in their folder part, not refused.
	e.names = vmname.Limit(vm.MaxName(LimaHome(st)))
	e.migrate = func() { e.migrateOld(context.Background(), oldLimaHome(), LimaHome(st)) }
	// Cage owns a separate profile; it never imports another tool's VMs.
	if config := os.Getenv(cage.ConfigEnv); config != "" {
		e.migrate = nil
		e.cageProfile = filepath.Dir(config)
		e.privateStorage, err = cage.StoragePrivatePaths(filepath.Dir(config))
		if err != nil {
			return nil, err
		}
		e.privateStorage = append(e.privateStorage, filepath.Dir(config))
	}
	e.newBack = func() (vm.Backend, error) {
		// Every VM there is agent-vm's: list, rm and destroy-all take them
		// all. Never a Lima home the user's own VMs live in.
		if h := LimaHome(st); sameDir(h, oldLimaHome()) {
			return nil, fmt.Errorf("agent-vm's Lima home %s is also your own Lima's home: set AGENT_VM_LIMA_HOME to a folder for agent-vm alone", h)
		}
		return vm.NewLima(LimaHome(st), string(st))
	}
	return e, nil
}

func (e *app) instanceName(dir string) string {
	if e.cageProfile != "" {
		return cage.InstanceName(e.cageProfile, dir)
	}
	return e.names.Name(dir)
}
