package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/DavidGodefroid/lockkit/audit"
	"github.com/DavidGodefroid/lockkit/config"
	"github.com/DavidGodefroid/lockkit/console"
	"github.com/DavidGodefroid/lockkit/hubclient"
	"github.com/DavidGodefroid/lockkit/ipc"
	"github.com/DavidGodefroid/lockkit/pii"
	"github.com/DavidGodefroid/lockkit/plans"
	"github.com/DavidGodefroid/lockkit/render"
	"github.com/DavidGodefroid/lockkit/ticket"

	"github.com/DavidGodefroid/lockfile/internal/answers"
	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/profile"
	"github.com/DavidGodefroid/lockfile/internal/rpc"
	"github.com/DavidGodefroid/lockfile/internal/source"
	"github.com/DavidGodefroid/lockfile/internal/workspace"
)

// hubPlanTTL is how long a plan waits for its decision and its run.
const hubPlanTTL = 24 * time.Hour

// hubBody is a plan of hub mode as the plan store keeps it: what the
// plan hash covers. SHA256 is the content the human is asked about,
// fetched and hashed at plan time; the run fetches again and refuses
// other bytes before any plugin.
type hubBody struct {
	Profile string              `json:"profile"`
	Key     string              `json:"key,omitempty"`
	Handle  string              `json:"handle,omitempty"`
	SHA256  string              `json:"sha256,omitempty"`
	Plugins []string            `json:"plugins"`
	Pins    []profile.PluginPin `json:"pins"`
}

// policyFPOf is the policy fingerprint in the ticket's form.
func policyFPOf(pol *profile.Policy) string { return "sha256:" + config.Fingerprint(pol) }

// screenDetectors mask personal data in a key on the hub's screen.
var screenDetectors = func() []pii.Detector {
	ds, err := pii.Detectors([]string{"email", "phone", "iban", "card"})
	if err != nil {
		panic(err)
	}
	return ds
}()

// hubPlan is file.plan in hub mode: the content is fetched now and its
// sha256, the plugins and their pins make the plan hash; the plan is
// stored, its masked screen pushed to the hub, and the agent hears
// pending with the ticket to run.
func (b *Backend) hubPlan(ctx context.Context, req ipc.Request, env console.Env, h *console.Hub, pol *profile.Policy, pl *plan, p rpc.PlanParams, sc console.Screen) ipc.Response {
	pr := pol.P
	body := hubBody{Profile: pr.Name, Key: pl.key, Handle: string(pl.handle), Plugins: pl.plugins, Pins: pl.pins}
	if pl.key != "" {
		if _, err := env.Session(ctx); err != nil {
			return console.ErrResp(req.ID, ipc.CodeConnLost, msgConnLost)
		}
		sess := b.source()
		if sess == nil {
			return console.ErrResp(req.ID, ipc.CodeConnLost, msgConnLost)
		}
		ws, _, err := b.workspaceFor(pol)
		if err != nil {
			return console.ErrResp(req.ID, ipc.CodeInternal, msgInternal)
		}
		hd, info, err := source.Fetch(ctx, sess.src, pl.key, pr.MaxObjectBytes, ws)
		if err != nil {
			if errors.Is(err, source.ErrKeyOutsidePrefix) {
				return product.Refuse(req.ID, answers.CodeKeyOutsidePrefix)
			}
			return product.Refuse(req.ID, answers.CodeFetchFailed)
		}
		// Only hashed here: the run fetches again and serves that copy.
		_ = ws.Release(hd)
		body.SHA256 = info.SHA256
		sc.Body = append(sc.Body, fmt.Sprintf("sha256 %s · %d bytes", info.SHA256, info.Size))
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return console.ErrResp(req.ID, ipc.CodeInternal, msgInternal)
	}
	hash, err := ticket.HashOf(body)
	if err != nil {
		return console.ErrResp(req.ID, ipc.CodeInternal, msgInternal)
	}
	caller := plans.Caller{UID: pl.peerUID, Token: p.Requester.Token}
	stored, err := h.Store.Create(plans.Binding{UID: caller.UID, Agent: p.Requester.Agent, TokenHash: plans.HashToken(caller.Token),
		PolicyFP: policyFPOf(pol), PlanHash: hash}, raw, hubPlanTTL)
	if err != nil {
		return console.ErrResp(req.ID, ipc.CodeInternal, msgInternal)
	}
	lines := make([]render.Line, 0, len(sc.Body))
	for _, l := range sc.Body {
		lines = append(lines, render.Structure(l))
	}
	sc.Hub = &console.HubTicket{PlanID: stored.ID, Caller: caller, Risk: ticket.RiskReadPII,
		Requester: ticket.Requester{Agent: p.Requester.Agent},
		Masked: render.MaskScreen(render.ScreenSource{Header: sc.Header, Note: sc.Note, Body: lines, Warnings: sc.Warnings, Red: sc.Red},
			render.Masking{Detectors: screenDetectors})}
	a := env.Approve(ctx, req.ID, sc)
	if a.Pending != nil {
		return a.Refusal
	}
	_ = h.Store.End(stored.ID, caller, plans.Withdrawn, "not submitted")
	if a.Approved {
		// An approval on this terminal cannot happen in hub mode.
		return console.ErrResp(req.ID, ipc.CodeInternal, msgInternal)
	}
	return product.ApprovalRefusal(req.ID, a.Refusal)
}

// hubRun is file.run in hub mode: the plan of a ticket the hub decided
// runs once, if the decision verifies, matches the ticket and the plan's
// binding (policy, plugins, content) has not moved.
func (b *Backend) hubRun(ctx context.Context, req ipc.Request, env console.Env, h *console.Hub, p rpc.RunParams) ipc.Response {
	if p.Ticket == "" {
		return console.ErrResp(req.ID, ipc.CodeInvalidParams, "in hub mode, run the ticket file.plan answered")
	}
	pol := policyOf(env)
	if pol == nil {
		return console.ErrResp(req.ID, ipc.CodeInternal, msgInternal)
	}
	caller := plans.Caller{UID: peerUID(ctx), Token: p.Requester.Token}
	var body hubBody
	recheck := func(sp *plans.Plan) (string, string, error) {
		if err := json.Unmarshal(sp.Body, &body); err != nil {
			return "", "", err
		}
		if !b.pinsInstalled(body.Pins) {
			return "", "", fmt.Errorf("%w: a plugin changed since the plan", plans.ErrStale)
		}
		hash, err := ticket.HashOf(body)
		return policyFPOf(pol), hash, err
	}
	run, d, err := h.Client.Begin(h.Store, p.Ticket, caller, recheck)
	if err != nil {
		if d != nil && errors.Is(err, hubclient.ErrDenied) {
			b.auditDecision(ctx, env, p.Ticket, nil, d, audit.EventDenied)
		}
		return hubRefusal(req.ID, p.Ticket, err)
	}
	pl := &plan{id: run.ID, peerUID: caller.UID, key: body.Key, handle: workspace.Handle(body.Handle), plugins: body.Plugins, pins: body.Pins}
	b.auditDecision(ctx, env, p.Ticket, pl, d, audit.EventApproved)
	resp := b.execute(ctx, req, env, pol, pl, body.SHA256)
	reason := ""
	if resp.Error != nil {
		reason = resp.Error.Message
	}
	if err := h.Client.Finish(ctx, h.Store, run.ID, caller, resp.Error == nil, reason); err != nil {
		env.Println("ticket " + p.Ticket + ": the hub was not told it was consumed: " + env.Safe(err.Error()))
	}
	return resp
}

// auditDecision records the hub's decision on a ticket: who answered, on
// which facade, and the hub's signature.
func (b *Backend) auditDecision(ctx context.Context, env console.Env, id string, pl *plan, d *ticket.Decision, event string) {
	rec := Record{Base: stamp(ctx, b.o.Profile, event, string(d.Decision))}
	rec.Approval, rec.Reason, rec.Ticket = "hub", d.Reason, id
	rec.Approver, rec.Facade, rec.HubSig = &d.Approver, d.Facade, d.Sig
	if pl != nil {
		rec.PlanID, rec.Key, rec.Handle, rec.Plugins = pl.id, pl.key, string(pl.handle), pl.plugins
	}
	env.Audit(rec)
}

// hubRefusal answers a run whose ticket cannot run.
func hubRefusal(id int64, tk string, err error) ipc.Response {
	switch {
	case errors.Is(err, hubclient.ErrPending):
		return console.PendingResp(id, product.P.Name, console.Pending{Ticket: tk})
	case errors.Is(err, hubclient.ErrDenied):
		// The outcome only, as on the terminal: the reason is audited.
		return console.ErrResp(id, ipc.CodeDenied, string(answers.CodeApprovalDenied))
	case errors.Is(err, ticket.ErrExpired):
		return console.ErrResp(id, ipc.CodeTimeout, string(answers.CodeApprovalTimeout))
	case errors.Is(err, plans.ErrStale):
		return product.Refuse(id, answers.CodeStale)
	case errors.Is(err, plans.ErrWrongRequester):
		return console.ErrResp(id, ipc.CodeWrongRequester, "the ticket was asked by another agent")
	}
	return console.ErrResp(id, ipc.CodeNoSuchPlan, msgNoSuchPlan)
}
