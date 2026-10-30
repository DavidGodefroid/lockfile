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
	// Unfetched: the object could not be fetched at plan time. The plan
	// goes to the hub all the same and its run refuses fetch_failed: the
	// agent learns nothing about the file before an approval, as on the
	// terminal, where the fetch comes after the human.
	Unfetched bool `json:"unfetched,omitempty"`
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
	// The answer comes at the next answer quantum: its time says nothing
	// about the file's size or the fetch.
	start := b.o.Now()
	defer func() { _ = answers.Quantize(ctx, start, pr.AnswerQuantum, b.o.Now) }()
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
			// Not told to the agent now (the key's prefix was checked
			// before): the human sees it, the run refuses it.
			body.Unfetched = true
			sc.Body = append(sc.Body, "the object could not be fetched now: an approval will answer fetch_failed")
		} else {
			// Only hashed here: the run fetches again and serves that copy.
			_ = ws.Release(hd)
			body.SHA256 = info.SHA256
			sc.Body = append(sc.Body, fmt.Sprintf("sha256 %s · %d bytes", info.SHA256, info.Size))
		}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return console.ErrResp(req.ID, ipc.CodeInternal, msgInternal)
	}
	hash, inputs, err := body.hashes()
	if err != nil {
		return console.ErrResp(req.ID, ipc.CodeInternal, msgInternal)
	}
	caller := plans.Caller{UID: pl.peerUID, Token: p.Requester.Token}
	stored, err := h.Store.Create(plans.Binding{UID: caller.UID, Agent: p.Requester.Agent, TokenHash: plans.HashToken(caller.Token),
		PolicyFP: policyFPOf(pol), PlanHash: hash, Inputs: inputs}, raw, hubPlanTTL)
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
		bd, fp, hash, _, err := b.hubCheck(pol, sp)
		body = bd
		return fp, hash, err
	}
	run, d, err := h.Client.Begin(h.Store, p.Ticket, caller, recheck)
	if err != nil {
		if d != nil && errors.Is(err, hubclient.ErrDenied) {
			b.auditDecision(ctx, env, p.Ticket, nil, d, audit.EventDenied)
		}
		if errors.Is(err, ticket.ErrExpired) {
			// Decision H1: the ticket (pending past its TTL, or approved
			// but the approval aged) is asked again, re-checked through
			// the recompute, and the agent hears pending with the new one.
			n, rerr := h.Client.Renew(ctx, h.Store, p.Ticket, caller)
			b.auditRenewal(ctx, env, p.Ticket, n, rerr)
			if n == "" {
				// The agent hears the outcome only; the cause is audited
				// and printed here.
				return console.ErrResp(req.ID, ipc.CodeTimeout, "ticket expired, and could not be asked again: plan the request again")
			}
			// Pushed or not (hub down), the renewal is pending: it goes
			// out later.
			return console.PendingResp(req.ID, product.P.Name, console.Pending{Ticket: n, Instance: h.Instance})
		}
		return hubRefusal(req.ID, p.Ticket, h.Instance, err)
	}
	pl := &plan{id: run.ID, peerUID: caller.UID, key: body.Key, handle: workspace.Handle(body.Handle), plugins: body.Plugins, pins: body.Pins}
	b.auditDecision(ctx, env, p.Ticket, pl, d, audit.EventApproved)
	var resp ipc.Response
	if body.Unfetched {
		start := b.o.Now()
		b.auditRun(ctx, env, pl, nil, string(answers.CodeFetchFailed))
		_ = answers.Quantize(ctx, start, pol.P.AnswerQuantum, b.o.Now)
		resp = product.Refuse(req.ID, answers.CodeFetchFailed)
	} else {
		resp = b.execute(ctx, req, env, pol, pl, body.SHA256)
	}
	reason := ""
	if resp.Error != nil {
		reason = resp.Error.Message
	}
	if err := h.Client.Finish(ctx, h.Store, run.ID, caller, resp.Error == nil, reason); err != nil {
		env.Println("ticket " + p.Ticket + ": the hub was not told it was consumed: " + env.Safe(err.Error()))
	}
	return resp
}

// hashes are the plan hash of body and its inputs by field name
// (ticket.InputDigests over exactly what the hash covers): a renewal
// names the fields that moved.
func (body hubBody) hashes() (string, map[string]string, error) {
	hash, err := ticket.HashOf(body)
	if err != nil {
		return "", nil, err
	}
	inputs, err := ticket.InputDigests(body)
	if err != nil {
		return "", nil, err
	}
	return hash, inputs, nil
}

// hubCheck is the recheck of stored plan sp under the policy in force:
// its body, policy fingerprint, plan hash and plan-hash inputs. The run
// (Begin's recheck) and the renewal (recompute) share it, so both see the
// same hash. It has no effect: the plugins are looked up, not opened.
func (b *Backend) hubCheck(pol *profile.Policy, sp *plans.Plan) (hubBody, string, string, map[string]string, error) {
	var body hubBody
	if err := json.Unmarshal(sp.Body, &body); err != nil {
		return body, "", "", nil, err
	}
	if !b.pinsInstalled(body.Pins) {
		return body, "", "", nil, fmt.Errorf("%w: a plugin changed since the plan", plans.ErrStale)
	}
	hash, inputs, err := body.hashes()
	if err != nil {
		return body, "", "", nil, err
	}
	return body, policyFPOf(pol), hash, inputs, nil
}

// recompute is the hub client's Recompute (decision H1): what a renewal
// of a stored plan is re-checked against, the policy in force now and the
// plugins installed now, as the run's recheck sees them. The policy in
// force is the approved one on disk: the console saves a policy before it
// applies it (at start and at every change), and a renewal asked from the
// hub comes with no request, hence no console.Env.
//
// The content's sha256 is not checked now: hashing the object again means
// fetching it, which needs a live session and writes to the workspace,
// and a recompute runs nothing with effects (a renewal may come from the
// hub, with no agent request). Its input is ticket.UncheckedInput, so the
// renewal names it "unchecked" and is never shown as unchanged; the plan
// hash stays the stored one. The run fetches the object again and refuses
// other bytes as stale before any plugin. Everything else the plan hash
// covers is either the request itself (profile, key, handle, plugins) or
// bound by the policy fingerprint (the pins), which lockkit refuses to
// renew across.
func (b *Backend) recompute(sp *plans.Plan) (string, string, map[string]string, error) {
	pol, err := b.approved()
	if err != nil {
		return "", "", nil, err
	}
	body, fp, hash, inputs, err := b.hubCheck(pol, sp)
	if err != nil {
		return "", "", nil, err
	}
	// A plan whose object could not be fetched has no sha256 input: it is
	// named unchecked all the same (its run answers fetch_failed, or would
	// hash content nobody saw), never one click.
	if _, ok := inputs["sha256"]; ok || body.Unfetched {
		inputs["sha256"] = ticket.UncheckedInput
	}
	return fp, hash, inputs, nil
}

// OnHub is console.Options.OnHub: the console's hub, before any request,
// gets the recompute, so a renewal asked from the hub right after a start
// is checked too.
func (b *Backend) OnHub(h *console.Hub) { h.Client.SetRecompute(b.recompute) }

// auditRenewal records the renewal of expired ticket old by the agent's
// run: the new ticket n, or why it could not be asked again (err: the
// policy or a plugin changed, or the ticket is not renewable), which the
// console prints too.
func (b *Backend) auditRenewal(ctx context.Context, env console.Env, old, n string, err error) {
	rec := Record{Base: stamp(ctx, b.o.Profile, audit.EventRenew, "renewed")}
	rec.Approval, rec.Ticket = "hub", old
	switch {
	case n == "":
		if err == nil {
			err = errors.New("no renewal")
		}
		code := hubclient.AnswerCode(err)
		rec.Decision, rec.Reason, rec.Error = "refused", code, err.Error()
		env.Println("ticket " + old + " expired and could not be asked again (" + code + "): " + env.Safe(err.Error()))
	case err != nil:
		rec.Reason = "renewed by ticket " + n + ", not sent to the hub yet"
		rec.Error = err.Error()
		env.Println("ticket " + old + " expired: asked again as ticket " + n + ", not sent to the hub yet: " + env.Safe(err.Error()))
	default:
		rec.Reason = "renewed by ticket " + n
	}
	env.Audit(rec)
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
func hubRefusal(id int64, tk, instance string, err error) ipc.Response {
	switch {
	case errors.Is(err, hubclient.ErrPending):
		return console.PendingResp(id, product.P.Name, console.Pending{Ticket: tk, Instance: instance})
	case errors.Is(err, hubclient.ErrDenied):
		// The outcome only, as on the terminal: the reason is audited.
		return console.ErrResp(id, ipc.CodeDenied, string(answers.CodeApprovalDenied))
	case errors.Is(err, plans.ErrStale):
		return product.Refuse(id, answers.CodeStale)
	case errors.Is(err, plans.ErrWrongRequester):
		return console.ErrResp(id, ipc.CodeWrongRequester, "the ticket was asked by another agent")
	}
	return console.ErrResp(id, ipc.CodeNoSuchPlan, msgNoSuchPlan)
}
