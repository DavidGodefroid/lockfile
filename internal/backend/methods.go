package backend

import (
	"context"
	"errors"
	"slices"

	"github.com/DavidGodefroid/lockkit/audit"
	"github.com/DavidGodefroid/lockkit/config"
	"github.com/DavidGodefroid/lockkit/console"
	"github.com/DavidGodefroid/lockkit/ipc"

	"github.com/DavidGodefroid/lockfile/internal/answers"
	"github.com/DavidGodefroid/lockfile/internal/plugin"
	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/profile"
	"github.com/DavidGodefroid/lockfile/internal/rpc"
	"github.com/DavidGodefroid/lockfile/internal/source"
	"github.com/DavidGodefroid/lockfile/internal/workspace"
)

const (
	msgPlanParams = rpc.MsgPlanParams
	msgNoSuchPlan = rpc.MsgNoSuchPlan
	msgConnLost   = rpc.MsgConnLost
	msgInternal   = rpc.MsgInternal
)

func policyOf(env console.Env) *profile.Policy {
	p, _ := env.Policy().(*profile.Policy)
	return p
}

func peerUID(ctx context.Context) int {
	if c, ok := console.PeerFrom(ctx); ok {
		return c.UID
	}
	return -1
}

// plugins lists the approved, installed plugins with their questions.
func (b *Backend) plugins(ctx context.Context, req ipc.Request, env console.Env) ipc.Response {
	if r := console.Decode(req, &struct{}{}); r != nil {
		return *r
	}
	pol := policyOf(env)
	installed, err := b.store.Installed()
	if err != nil || pol == nil {
		return console.ErrResp(req.ID, ipc.CodeInternal, msgInternal)
	}
	out := []rpc.PluginInfo{}
	for _, in := range installed {
		pin, ok := pol.Pin(in.Name)
		if !ok || in.Tampered || !samePin(pin, in) {
			continue
		}
		info := rpc.PluginInfo{Plugin: in.Name, Version: in.Version, Marketplace: in.Marketplace, Official: in.Official,
			Question: in.Manifest.Question, One: in.Manifest.One, Zero: in.Manifest.Zero}
		if !in.Official {
			info.Warning = answers.WarningUnofficial
		}
		out = append(out, info)
	}
	return console.OKResp(req.ID, out)
}

func samePin(p profile.PluginPin, in plugin.Installed) bool {
	return p.Version == in.Version && p.ManifestSHA256 == in.ManifestSHA256 && p.BinarySHA256 == in.BinarySHA256 && p.Official == in.Official
}

// plan validates a request from the request and the local install only
// (no source access), shows it to the human and keeps it for one run.
func (b *Backend) plan(ctx context.Context, req ipc.Request, env console.Env) ipc.Response {
	var p rpc.PlanParams
	if r := console.Decode(req, &p); r != nil {
		return *r
	}
	if (p.Key == "") == (p.Handle == "") || len(p.Plugins) == 0 || len(p.Intent) > rpc.MaxIntent || hasDup(p.Plugins) {
		return console.ErrResp(req.ID, ipc.CodeInvalidParams, msgPlanParams)
	}
	pol := policyOf(env)
	if pol == nil {
		return console.ErrResp(req.ID, ipc.CodeInternal, msgInternal)
	}
	pr := pol.P
	pl := &plan{peerUID: peerUID(ctx), plugins: p.Plugins, policyFP: config.Fingerprint(pol)}
	if p.Key != "" {
		k, err := source.CheckKey(pr.Prefix, p.Key)
		if err != nil {
			return product.Refuse(req.ID, answers.CodeKeyOutsidePrefix)
		}
		pl.key = k
	} else {
		pl.handle = workspace.Handle(p.Handle)
		f, _, err := b.ws.OpenFile(pl.handle)
		if err != nil {
			b.forget(pl.handle)
			return product.Refuse(req.ID, answers.CodeHandleExpired)
		}
		f.Close()
	}
	if b.runner.Used(pl.handle)+len(p.Plugins) > pr.MaxPluginsPerHandle {
		return product.Refuse(req.ID, answers.CodeTooManyPlugins)
	}
	if _, _, err := b.o.Probe(); err != nil {
		return product.Refuse(req.ID, answers.CodeSandboxUnavailable)
	}
	var shown []screenPlugin
	for _, name := range p.Plugins {
		bin, in, err := b.store.Open(name)
		switch {
		case errors.Is(err, plugin.ErrNotInstalled):
			return product.Refuse(req.ID, answers.CodePluginNotInstalled)
		case err != nil:
			return product.Refuse(req.ID, answers.CodePluginTampered)
		}
		bin.Close()
		pin, ok := pol.Pin(name)
		if !ok || !samePin(pin, in) {
			// Installed but not (or not in this version) approved by the
			// human: the console's :review comes first.
			return product.Refuse(req.ID, answers.CodePluginNotInstalled)
		}
		pl.pins = append(pl.pins, pin)
		shown = append(shown, screenPlugin{Name: name, Version: in.Version, Marketplace: in.Marketplace, Official: in.Official, Manifest: in.Manifest})
	}
	id, err := newPlanID()
	if err != nil {
		return console.ErrResp(req.ID, ipc.CodeInternal, msgInternal)
	}
	pl.id = id
	record := func(base audit.Base) audit.Record {
		return Record{Base: base, PlanID: id, Key: pl.key, Handle: string(pl.handle), Plugins: pl.plugins}
	}
	a := env.Approve(ctx, req.ID, planScreen(p.Key, p.Handle, shown, p.Intent, record))
	if !a.Approved {
		return product.ApprovalRefusal(req.ID, a.Refusal)
	}
	b.plans.add(pl)
	return console.OKResp(req.ID, rpc.PlanResult{PlanID: id, Answers: len(p.Plugins)})
}

func hasDup(s []string) bool {
	c := slices.Clone(s)
	slices.Sort(c)
	return len(slices.Compact(c)) != len(s)
}

// run runs an approved plan once and answers at the next quantum.
func (b *Backend) run(ctx context.Context, req ipc.Request, env console.Env) ipc.Response {
	var p rpc.RunParams
	if r := console.Decode(req, &p); r != nil {
		return *r
	}
	pl, ok := b.plans.take(p.PlanID, peerUID(ctx))
	if !ok {
		return console.ErrResp(req.ID, ipc.CodeNoSuchPlan, msgNoSuchPlan)
	}
	pol := policyOf(env)
	if pol == nil || config.Fingerprint(pol) != pl.policyFP || !b.pinsInstalled(pl.pins) {
		return console.ErrResp(req.ID, ipc.CodeNoSuchPlan, msgNoSuchPlan)
	}
	if _, err := env.Session(ctx); err != nil {
		return console.ErrResp(req.ID, ipc.CodeConnLost, msgConnLost)
	}
	sess := b.source()
	if sess == nil {
		return console.ErrResp(req.ID, ipc.CodeConnLost, msgConnLost)
	}
	pr := pol.P
	b.runner.MaxPerHandle = pr.MaxPluginsPerHandle
	res, ref, entries := answers.ExecutePlan(ctx, answers.Plan{Profile: pr.Name, Key: pl.key, Handle: pl.handle, Plugins: pl.plugins},
		answers.Deps{
			Sources:        map[string]source.Source{pr.Name: sess.src},
			MaxObjectBytes: func(string) int64 { return pr.MaxObjectBytes },
			WS:             b.ws, Runner: b.runner, Quantum: pr.AnswerQuantum, Now: b.o.Now,
		})
	if ref != nil {
		b.auditRun(ctx, env, pl, entries, string(ref.Code))
		env.Println("plan " + pl.id + ": refused " + string(ref.Code))
		if ref.Code == answers.CodeHandleExpired && pl.handle != "" {
			b.forget(pl.handle)
		}
		return product.Refuse(req.ID, ref.Code)
	}
	b.auditRun(ctx, env, pl, entries, "")
	b.mu.Lock()
	h := workspace.Handle(res.Handle)
	b.given[h] = append(b.given[h], res.Answers...)
	b.mu.Unlock()
	env.Println("plan " + pl.id + ": answered")
	return console.OKResp(req.ID, res)
}

// pinsInstalled reports whether every pinned plugin is still installed as
// it was approved.
func (b *Backend) pinsInstalled(pins []profile.PluginPin) bool {
	installed, err := b.store.Installed()
	if err != nil {
		return false
	}
	for _, pin := range pins {
		ok := slices.ContainsFunc(installed, func(in plugin.Installed) bool {
			return in.Name == pin.Name && !in.Tampered && samePin(pin, in)
		})
		if !ok {
			return false
		}
	}
	return true
}

func (b *Backend) forget(h workspace.Handle) {
	b.mu.Lock()
	delete(b.given, h)
	b.mu.Unlock()
}

// answers returns the answers already given on a live handle.
func (b *Backend) answers(ctx context.Context, req ipc.Request, env console.Env) ipc.Response {
	var p rpc.HandleParams
	if r := console.Decode(req, &p); r != nil {
		return *r
	}
	h := workspace.Handle(p.Handle)
	f, _, err := b.ws.OpenFile(h)
	if err != nil {
		b.forget(h)
		return product.Refuse(req.ID, answers.CodeHandleExpired)
	}
	f.Close()
	b.mu.Lock()
	given, ok := b.given[h]
	out := slices.Clone(given)
	b.mu.Unlock()
	if !ok {
		return product.Refuse(req.ID, answers.CodeHandleExpired)
	}
	return console.OKResp(req.ID, answers.Result{Handle: p.Handle, Answers: out})
}

// release deletes a fetched file now.
func (b *Backend) release(ctx context.Context, req ipc.Request, env console.Env) ipc.Response {
	var p rpc.HandleParams
	if r := console.Decode(req, &p); r != nil {
		return *r
	}
	h := workspace.Handle(p.Handle)
	b.forget(h)
	if err := b.ws.Release(h); err != nil {
		return product.Refuse(req.ID, answers.CodeHandleExpired)
	}
	env.Audit(Record{Base: stamp(ctx, b.o.Profile, audit.EventRun, "released"), Handle: p.Handle})
	return console.OKResp(req.ID, rpc.Released{Released: true})
}
