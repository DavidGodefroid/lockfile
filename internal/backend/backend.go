// Package backend is lockfile on lockkit's console: the Backend that
// connects a profile's source, validates and shows plans for approval, runs
// the approved ones and returns only the closed result of design §7b.
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
	o      Options
	store  plugin.Store
	ws     *workspace.Manager
	runner *answers.Runner
	plans  *planTable

	mu  sync.Mutex
	cur *session // the session of the last Connect
	// given holds the answers already returned on each handle.
	given map[workspace.Handle][]answers.Answer

	stop chan struct{}
	done chan struct{}
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
	p, ok := f.Profiles[o.Profile]
	if !ok {
		return nil, fmt.Errorf("config: no profile %q", o.Profile)
	}
	home := filepath.Join(o.StateDir, product.P.Name)
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	if o.PluginDir == "" {
		o.PluginDir = home
	}
	if o.WorkDir == "" {
		o.WorkDir = f.Workspace.Dir
	}
	if o.WorkDir == "" {
		o.WorkDir = filepath.Join(home, "work")
	}
	ws, err := workspace.Open(workspace.Config{Dir: o.WorkDir, MaxBytes: f.Workspace.MaxBytes, TTL: p.HandleTTL, Now: o.Now})
	if err != nil {
		return nil, err
	}
	b := &Backend{
		o: o, store: plugin.Store{Dir: o.PluginDir}, ws: ws, plans: newPlanTable(o.Now),
		given: map[workspace.Handle][]answers.Answer{}, stop: make(chan struct{}), done: make(chan struct{}),
	}
	b.runner = &answers.Runner{WS: ws, Store: b.store, Run: o.Run, MaxPerHandle: p.MaxPluginsPerHandle}
	go b.expireLoop()
	return b, nil
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
			b.ws.Expire()
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
	return b.ws.Close()
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
	if err := b.o.ProcHidden(); err != nil {
		out = append(out, console.Finding{Refuse: true, Text: "other accounts can read the plugins' processes in /proc (a covert channel to the agent): " + procmount.Fix})
	}
	if fi, err := os.Stat(b.o.WorkDir); err == nil && fi.Mode().Perm()&0o077 != 0 {
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
