// Package answers runs plugins on a fetched file and turns their outcomes
// into what the agent may see: per plugin 1, 0, or no answer with a code
// from a closed list. Everything else goes to the audit entries.
package answers

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/DavidGodefroid/lockfile/internal/plugin"
	"github.com/DavidGodefroid/lockfile/internal/workspace"
	"github.com/DavidGodefroid/lockfile/pluginspec"
	"github.com/DavidGodefroid/lockfile/sandbox"
)

// DefaultMaxPerHandle bounds the answers disclosed about one fetched file.
const DefaultMaxPerHandle = 16

// ErrTooManyPlugins refuses a request that would take a handle above its
// answer budget. Its text is the agent code.
var ErrTooManyPlugins = errors.New("too_many_plugins")

// WarningUnofficial marks an answer from a plugin of an unofficial
// marketplace.
const WarningUnofficial = "unofficial_marketplace"

// Answer is one plugin's answer as the agent sees it.
type Answer struct {
	Plugin   string `json:"plugin"`
	Version  string `json:"version"`
	Official bool   `json:"official"`
	Result   *int   `json:"result"`
	Error    string `json:"error,omitempty"`
	Warning  string `json:"warning,omitempty"`
}

// AuditEntry is what the audit keeps about one plugin run.
type AuditEntry struct {
	Plugin       string
	Version      string
	BinarySHA256 string
	Result       *int
	Cause        pluginspec.FailureCause
	Report       sandbox.Report
	ConsoleError string
	// Fetch is set on the entry that records a plan's fetch.
	Fetch *FetchAudit
}

type Runner struct {
	WS           *workspace.Manager
	Store        plugin.Store
	Run          func(context.Context, sandbox.Spec) (pluginspec.Outcome, sandbox.Report, error)
	MaxPerHandle int

	mu   sync.Mutex
	used map[workspace.Handle]int
}

func (r *Runner) reserve(h workspace.Handle, n int) error {
	max := r.MaxPerHandle
	if max <= 0 {
		max = DefaultMaxPerHandle
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.used == nil {
		r.used = map[workspace.Handle]int{}
	}
	if r.used[h]+n > max {
		return ErrTooManyPlugins
	}
	r.used[h] += n
	return nil
}

// Answer runs plugins, in order, each on a fresh read-only open of h's
// file, and returns all answers once every plugin has finished. Every
// requested plugin counts against the handle's budget, answered or not.
// The error is ErrTooManyPlugins, a workspace error (expired or unknown
// handle) or ctx's; nothing is returned with it.
func (r *Runner) Answer(ctx context.Context, h workspace.Handle, plugins []string) ([]Answer, []AuditEntry, error) {
	probe, _, err := r.WS.OpenFile(h)
	if err != nil {
		return nil, nil, err
	}
	probe.Close()
	if err := r.reserve(h, len(plugins)); err != nil {
		return nil, nil, err
	}
	answers := make([]Answer, 0, len(plugins))
	audit := make([]AuditEntry, 0, len(plugins))
	for _, name := range plugins {
		a, e, err := r.one(ctx, h, name)
		if err != nil {
			return nil, nil, err
		}
		if !a.Official {
			a.Warning = WarningUnofficial
		}
		answers = append(answers, a)
		audit = append(audit, e)
	}
	return answers, audit, nil
}

func (r *Runner) one(ctx context.Context, h workspace.Handle, name string) (Answer, AuditEntry, error) {
	a := Answer{Plugin: name, Official: !strings.Contains(name, "/")}
	e := AuditEntry{Plugin: name}
	bin, inst, err := r.Store.Open(name)
	if err != nil {
		e.ConsoleError = err.Error()
		if errors.Is(err, plugin.ErrNotInstalled) {
			a.Error = plugin.ErrNotInstalled.Error()
		} else {
			// The installed plugin cannot be shown to be the verified one.
			a.Error = plugin.ErrTampered.Error()
		}
		return a, e, nil
	}
	defer bin.Close()
	a.Version, a.Official = inst.Version, inst.Official
	e.Version, e.BinarySHA256 = inst.Version, inst.BinarySHA256

	in, _, err := r.WS.OpenFile(h)
	if err != nil {
		return Answer{}, AuditEntry{}, err
	}
	defer in.Close()
	out, rep, err := r.Run(ctx, sandbox.Spec{Binary: bin, Input: in, Limits: inst.Manifest.Limits})
	e.Report = rep
	if ctx.Err() != nil {
		return Answer{}, AuditEntry{}, ctx.Err()
	}
	if err != nil {
		e.ConsoleError = err.Error()
		a.Error = sandbox.ErrUnsupported.Error()
		return a, e, nil
	}
	if out.Answer == nil {
		e.Cause = out.Cause
		a.Error = pluginspec.AgentErrorCode
		return a, e, nil
	}
	v := 0
	if *out.Answer {
		v = 1
	}
	a.Result, e.Result = &v, &v
	return a, e, nil
}
