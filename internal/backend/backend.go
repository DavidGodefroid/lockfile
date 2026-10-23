// Package backend is lockfile on lockkit's console: the Backend that
// connects a profile's source, validates and shows plans for approval, runs
// the approved ones and returns only the closed result of docs/security-model.md.
package backend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/DavidGodefroid/lockkit/config"
	"github.com/DavidGodefroid/lockkit/console"
	"github.com/DavidGodefroid/lockkit/kit"

	"github.com/DavidGodefroid/lockfile/internal/answers"
	"github.com/DavidGodefroid/lockfile/internal/plugin"
	"github.com/DavidGodefroid/lockfile/internal/procmount"
	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/profile"
	"github.com/DavidGodefroid/lockfile/internal/rpc"
	"github.com/DavidGodefroid/lockfile/internal/workspace"
	"github.com/DavidGodefroid/lockfile/pluginspec"
	"github.com/DavidGodefroid/lockfile/sandbox"
)

// Options configure a Backend.
type Options struct {
	// ConfigPath is the config file; Profile the profile served.
	ConfigPath string
	Profile    string
	// StateDir is lockkit's state base (kit.StateBase() when empty): the
	// approved policy, the audit log, and by default the plugins and the
	// workspace live under <StateDir>/lockfile.
	StateDir string
	// PluginDir and WorkDir override the defaults (tests).
	PluginDir, WorkDir string
	// Run replaces sandbox.Run, Probe sandbox.Probe (tests).
	Run   func(context.Context, sandbox.Spec) (pluginspec.Outcome, sandbox.Report, error)
	Probe func() (int, bool, error)
	// ProcHidden replaces procmount.CheckSystem: nil when /proc hides the
	// plugins' processes from other accounts. Set only by tests (CI
	// runners do not mount /proc with hidepid); no flag or config reaches
	// it.
	ProcHidden func() error
	Now        func() time.Time
	// ExpireEvery is how often expired handles are removed (30 s).
	ExpireEvery time.Duration
}

// Backend implements console.Backend for one profile.
type Backend struct {
	o     Options
	store plugin.Store
	home  string
	plans *planTable

	mu sync.Mutex
	// ws and runner follow the approved policy's workspace (workspaceFor).
	ws     *workspace.Manager
	runner *answers.Runner
	cur    *session // the session of the last Connect
	// given holds the answers already returned on each handle.
	given map[workspace.Handle][]answers.Answer

	stop chan struct{}
	done chan struct{}

	sanOnce sync.Once
	san     *profileSanitizer
}

var _ console.Backend = (*Backend)(nil)

// New prepares the backend: plugin store, workspace (its orphans swept),
// runner. Close removes the workspace.
func New(o Options) (*Backend, error) {
	if o.StateDir == "" {
		base, err := kit.StateBase()
		if err != nil {
			return nil, err
		}
		o.StateDir = base
	}
	if o.Run == nil {
		o.Run = sandbox.Run
	}
	if o.Probe == nil {
		o.Probe = sandbox.Probe
	}
	if o.ProcHidden == nil {
		o.ProcHidden = procmount.CheckSystem
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.ExpireEvery <= 0 {
		o.ExpireEvery = 30 * time.Second
	}
	f, err := profile.ReadFile(o.ConfigPath)
	if err != nil {
		return nil, err
	}
	if _, ok := f.Profiles[o.Profile]; !ok {
		return nil, fmt.Errorf("config: no profile %q", o.Profile)
	}
	home := filepath.Join(o.StateDir, product.P.Name)
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	if o.PluginDir == "" {
		o.PluginDir = home
	}
	b := &Backend{
		o: o, store: plugin.Store{Dir: o.PluginDir}, home: home, plans: newPlanTable(o.Now),
		given: map[workspace.Handle][]answers.Answer{}, stop: make(chan struct{}), done: make(chan struct{}),
	}
	// The workspace follows the approved policy, never the raw config: an
	// unreviewed work_dir, max_workspace_bytes or handle_ttl is not used.
	// Before any approval, the default directory and limits apply.
	pol, err := b.approved()
	if err != nil {
		pol = &profile.Policy{}
	}
	if _, _, err := b.workspaceFor(pol); err != nil {
		return nil, err
	}
	go b.expireLoop()
	return b, nil
}

// workDirOf is the workspace directory of a policy.
func (b *Backend) workDirOf(pol *profile.Policy) string {
	switch {
	case b.o.WorkDir != "":
		return b.o.WorkDir
	case pol.Workspace.Dir != "":
		return pol.Workspace.Dir
	}
	return filepath.Join(b.home, "work")
}

// workspaceFor returns the workspace and runner of the approved policy pol:
// a moved work_dir closes the old workspace (its files are deleted) and
// opens the new one; the budget and the TTL of new handles are pol's.
func (b *Backend) workspaceFor(pol *profile.Policy) (*workspace.Manager, *answers.Runner, error) {
	dir := b.workDirOf(pol)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ws == nil || b.ws.Dir() != dir {
		if b.ws != nil {
			_ = b.ws.Close()
			b.given = map[workspace.Handle][]answers.Answer{}
		}
		ws, err := workspace.Open(workspace.Config{Dir: dir, Now: b.o.Now})
		if err != nil {
			b.ws, b.runner = nil, nil
			return nil, nil, err
		}
		b.ws = ws
		b.runner = &answers.Runner{WS: ws, Store: b.store, Run: b.o.Run}
	}
	b.ws.SetLimits(pol.Workspace.MaxBytes, pol.P.HandleTTL)
	b.runner.MaxPerHandle = pol.P.MaxPluginsPerHandle
	return b.ws, b.runner, nil
}

// current is the open workspace and runner, nil before New succeeded or
// after a failed move.
func (b *Backend) current() (*workspace.Manager, *answers.Runner) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ws, b.runner
}

func (b *Backend) expireLoop() {
	defer close(b.done)
	t := time.NewTicker(b.o.ExpireEvery)
	defer t.Stop()
	for {
		select {
		case <-b.stop:
			return
		case <-t.C:
			if ws, _ := b.current(); ws != nil {
				ws.Expire()
			}
		}
	}
}

// Close stops the expiry loop and removes every fetched file.
func (b *Backend) Close() error {
	select {
	case <-b.stop:
		return nil
	default:
	}
	close(b.stop)
	<-b.done
	if ws, _ := b.current(); ws != nil {
		return ws.Close()
	}
	return nil
}

// Methods implements console.Backend.
func (b *Backend) Methods() map[string]console.Handler {
	return map[string]console.Handler{
		rpc.MethodPlugins: b.plugins,
		rpc.MethodPlan:    b.plan,
		rpc.MethodRun:     b.run,
		rpc.MethodAnswers: b.answers,
		rpc.MethodRelease: b.release,
	}
}

// approved reads the policy the human approved last: Connect is not given
// it (lockkit v0.1.0), and must not connect to an unconfirmed edit.
func (b *Backend) approved() (*profile.Policy, error) {
	var p profile.Policy
	ok, err := config.LoadApproved(product.P, b.o.StateDir, config.ApprovedKey("", b.o.Profile), &p)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("no approved policy yet")
	}
	return &p, nil
}

// Connect implements console.Backend.
func (b *Backend) Connect(ctx context.Context, c console.Credentials) (console.Session, error) {
	pol, err := b.approved()
	if err != nil {
		return nil, err
	}
	b.sanitizer().addProfile(pol.P)
	s, err := connect(pol.P, c)
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	b.cur = s
	b.mu.Unlock()
	return s, nil
}

// session is the source of the session lockkit holds. lockkit may wrap the
// session it got from Connect (a Vault lease), so the backend keeps its
// own reference.
func (b *Backend) source() *session {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cur
}

// Checks implements console.Backend: no sandbox, no console.
func (b *Backend) Checks(ctx context.Context, s console.Session) []console.Finding {
	var out []console.Finding
	if _, _, err := b.o.Probe(); err != nil {
		out = append(out, console.Finding{Refuse: true, Text: "the plugin sandbox is unavailable (Landlock and seccomp are required): run lockfile doctor"})
	}
	if err := b.o.ProcHidden(); errors.Is(err, procmount.ErrContainer) {
		out = append(out, console.Finding{Refuse: true, Text: "other accounts could read the plugins' processes in /proc (a covert channel to the agent): " + err.Error()})
	} else if err != nil {
		out = append(out, console.Finding{Refuse: true, Text: "other accounts can read the plugins' processes in /proc (a covert channel to the agent): " + procmount.Fix})
	}
	if ws, _ := b.current(); ws == nil {
		out = append(out, console.Finding{Refuse: true, Text: "the workspace directory (work_dir) cannot be opened"})
	} else if fi, err := os.Stat(ws.Dir()); err == nil && fi.Mode().Perm()&0o077 != 0 {
		out = append(out, console.Finding{Refuse: true, Text: "the workspace directory (work_dir) is open to other accounts: it must be 0700"})
	}
	return out
}

// LoadPolicy implements console.Backend.
func (b *Backend) LoadPolicy() (console.Policy, error) {
	p, err := profile.Load(b.o.ConfigPath, b.o.Profile, b.store)
	if err != nil {
		return nil, err
	}
	b.sanitizer().addProfile(p.P)
	return p, nil
}

// Status implements console.Backend.
func (b *Backend) Status(console.Session) any {
	st := rpc.Status{Protocol: pluginspec.ProtocolVersion, Sandbox: "ok"}
	if _, _, err := b.o.Probe(); err != nil {
		st.Sandbox = "unavailable"
	}
	if pol, err := b.approved(); err == nil {
		st.AnswerQuantumMS = pol.P.AnswerQuantum.Milliseconds()
	}
	return st
}
