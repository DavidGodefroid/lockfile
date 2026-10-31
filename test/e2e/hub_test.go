package e2e

import (
	"crypto/ed25519"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DavidGodefroid/lockkit/console"
	"github.com/DavidGodefroid/lockkit/hubclient/hubtest"
	"github.com/DavidGodefroid/lockkit/ipc"
	"github.com/DavidGodefroid/lockkit/ticket"

	"github.com/DavidGodefroid/lockfile/internal/answers"
	"github.com/DavidGodefroid/lockfile/internal/rpc"
	"github.com/DavidGodefroid/lockfile/internal/testenv/consoletest"
)

const hubInstance = consoletest.HubInstance

// hubConsole is a console in hub mode beside a fake lockhub.
func hubConsole(t *testing.T, files map[string]string) (*consoletest.Console, *hubtest.Hub, ed25519.PublicKey) {
	t.Helper()
	c, hub, pub, _ := hubConsoleAt(t, files, nil)
	return c, hub, pub
}

// hubConsoleAt is hubConsole on clock now (nil: time.Now); it also gives
// the console's hub options, for a restart.
func hubConsoleAt(t *testing.T, files map[string]string, now func() time.Time) (*consoletest.Console, *hubtest.Hub, ed25519.PublicKey, console.HubOptions) {
	t.Helper()
	ho, hub, pub := consoletest.NewHub(t)
	c := consoletest.Start(t, consoletest.Options{
		Answers: []string{"y"}, // the policy, on the terminal; nothing else is asked here
		Plugins: []consoletest.Plugin{{Name: "test.answer1"}},
		Files:   files,
		Hub:     ho,
		Now:     now,
	})
	return c, hub, pub, ho
}

func pendingTicket(t *testing.T, r ipc.Response) string {
	t.Helper()
	var p rpc.PlanResult
	if r.Error != nil || json.Unmarshal(r.Result, &p) != nil || p.Status != "pending" || p.Lock != "lockfile" || p.Instance != hubInstance ||
		!ticket.ValidID(p.Ticket) || p.PlanID != "" {
		t.Fatalf("not pending: %+v %s", r.Error, r.Result)
	}
	return p.Ticket
}

// runDecided runs tk until its decision has arrived.
func runDecided(t *testing.T, w *wire, p rpc.RunParams) ipc.Response {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		_, r := w.call(rpc.MethodRun, p)
		var pr rpc.PlanResult
		if r.Error != nil || json.Unmarshal(r.Result, &pr) != nil || pr.Status != "pending" {
			return r
		}
		if time.Now().After(deadline) {
			t.Fatal("the decision never arrived")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// In hub mode a plan is pushed as a masked, signed ticket and answered
// pending, never asked here; once approved, its ticket runs once, on the
// bytes the plan was hashed on.
func TestHubPlanRunOnce(t *testing.T) {
	key := "invoices/jean.dupont@example.org.pdf"
	c, hub, pub := hubConsole(t, map[string]string{key: "%PDF-1.7 synthetic"})
	w := dialWire(t, c.Socket)
	who := ipc.Requester{Agent: "dev-1", Token: "tok-1"}
	_, r := w.call(rpc.MethodPlan, rpc.PlanParams{Key: key, Plugins: []string{"test.answer1"}, Intent: "check", Requester: who})
	tk := pendingTicket(t, r)
	raw := hub.Ticket(tk)
	if raw == nil || strings.Contains(string(raw), "jean.dupont@example.org") {
		t.Fatalf("hub ticket not masked: %s", raw)
	}
	got, err := ticket.VerifyTicket(pub, raw)
	if err != nil || got.Source.Lock != "lockfile" || got.Risk != ticket.RiskReadPII || got.Requester.Agent != "dev-1" {
		t.Fatalf("ticket %+v %v", got, err)
	}
	if strings.Contains(c.IO.Output(), "Approve?") {
		t.Fatalf("asked on the terminal in hub mode:\n%s", c.IO.Output())
	}
	// Before the decision: pending again, nothing runs.
	_, r = w.call(rpc.MethodRun, rpc.RunParams{Ticket: tk, Requester: who})
	pendingTicket(t, r)
	// Another agent's token cannot run it.
	hub.Decide(tk, ticket.Approved, "")
	if r := runDecided(t, w, rpc.RunParams{Ticket: tk, Requester: ipc.Requester{Agent: "dev-2", Token: "tok-2"}}); r.Error == nil || r.Error.Code != ipc.CodeWrongRequester {
		t.Fatalf("other requester: %+v %s", r.Error, r.Result)
	}
	r = runDecided(t, w, rpc.RunParams{Ticket: tk, Requester: who})
	var res answers.Result
	if r.Error != nil || json.Unmarshal(r.Result, &res) != nil || len(res.Answers) != 1 || res.Answers[0].Result == nil || *res.Answers[0].Result != 1 {
		t.Fatalf("run: %+v %s\n%s", r.Error, r.Result, c.IO.Output())
	}
	if !hub.Consumed(tk) {
		t.Error("the hub was not told the ticket was consumed")
	}
	if _, r := w.call(rpc.MethodRun, rpc.RunParams{Ticket: tk, Requester: who}); r.Error == nil {
		t.Fatalf("the ticket ran twice: %s", r.Result)
	}
}

// The content changed after the plan: the run refuses it stale before any
// plugin; a denial is a denial.
func TestHubStaleAndDenied(t *testing.T) {
	c, hub, _ := hubConsole(t, map[string]string{"invoices/a.pdf": "v1", "invoices/b.pdf": "v1"})
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(c.IO.Output())
		}
	})
	w := dialWire(t, c.Socket)
	_, r := w.call(rpc.MethodPlan, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.answer1"}})
	stale := pendingTicket(t, r)
	if err := os.WriteFile(filepath.Join(c.Root, "invoices", "a.pdf"), []byte("v2, another file"), 0o644); err != nil {
		t.Fatal(err)
	}
	hub.Decide(stale, ticket.Approved, "")
	if r := runDecided(t, w, rpc.RunParams{Ticket: stale}); r.Error == nil || r.Error.Message != string(answers.CodeStale) {
		t.Fatalf("changed content: %+v %s", r.Error, r.Result)
	}
	_, r = w.call(rpc.MethodPlan, rpc.PlanParams{Key: "invoices/b.pdf", Plugins: []string{"test.answer1"}})
	denied := pendingTicket(t, r)
	hub.Decide(denied, ticket.Denied, "not this one")
	if r := runDecided(t, w, rpc.RunParams{Ticket: denied}); r.Error == nil || r.Error.Message != string(answers.CodeApprovalDenied) {
		t.Fatalf("denied: %+v %s\n%s", r.Error, r.Result, c.IO.Output())
	}
}

// An object that cannot be fetched is not told to the agent at plan time:
// the plan is pending like any other, and the approval answers
// fetch_failed, as on the terminal.
func TestHubFetchFailureAfterApproval(t *testing.T) {
	c, hub, _ := hubConsole(t, map[string]string{"invoices/a.pdf": "v1"})
	w := dialWire(t, c.Socket)
	_, r := w.call(rpc.MethodPlan, rpc.PlanParams{Key: "invoices/missing.pdf", Plugins: []string{"test.answer1"}})
	tk := pendingTicket(t, r)
	hub.Decide(tk, ticket.Approved, "")
	if r := runDecided(t, w, rpc.RunParams{Ticket: tk}); r.Error == nil || r.Error.Message != string(answers.CodeFetchFailed) {
		t.Fatalf("missing object: %+v %s", r.Error, r.Result)
	}
}

// clock is a console clock the test moves.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }

// runUntil runs p until its answer is not pending on ticket tk (the
// decision arrived, or the ticket was asked again).
func runUntil(t *testing.T, w *wire, p rpc.RunParams, tk string) ipc.Response {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		_, r := w.call(rpc.MethodRun, p)
		var pr rpc.PlanResult
		if r.Error != nil || json.Unmarshal(r.Result, &pr) != nil || pr.Status != "pending" || pr.Ticket != tk {
			return r
		}
		if time.Now().After(deadline) {
			t.Fatal("the decision never arrived")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Decision H1: an approval the console can no longer act on (its clock
// says the approval is out of its window) is not a timeout. The run asks
// the ticket again: pending with a new ticket that renews the old one,
// re-checked by the lock (the policy, the plugins and the request are the
// plan's; the content, checked at the run only, is named unchecked). Approved,
// the new ticket runs once.
//
// lockfile's tickets are read_pii, which lockkit never expires by age
// (ticket.RiskReadPII.TTL() is 0): an approval dated past the console's
// clock is the expiry a lockfile console meets, and the one injected
// here.
func TestHubExpiredTicketRenewed(t *testing.T) {
	ck := &clock{t: time.Now().UTC().Truncate(time.Millisecond)}
	c, hub, pub, _ := hubConsoleAt(t, map[string]string{"invoices/a.pdf": "%PDF-1.7 synthetic"}, ck.now)
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(c.IO.Output())
		}
	})
	w := dialWire(t, c.Socket)
	who := ipc.Requester{Agent: "dev-1", Token: "tok-1"}
	_, r := w.call(rpc.MethodPlan, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.answer1"}, Requester: who})
	old := pendingTicket(t, r)
	// Decided at a time the console's clock is not at yet: unusable.
	hub.DecideWith(old, ticket.Approved, "", nil, func(d *ticket.Decision) { d.At = ck.now().Add(2 * time.Hour) })
	r = runUntil(t, w, rpc.RunParams{Ticket: old, Requester: who}, old)
	renewed := pendingTicket(t, r)
	if renewed == old {
		t.Fatal("the same ticket")
	}
	rt, err := ticket.VerifyTicket(pub, hub.Ticket(renewed))
	if err != nil {
		t.Fatal(err)
	}
	// Checked, nothing moved but the content, which only the run checks:
	// named unchecked, never one click.
	if r := rt.Renews; r == nil || r.Of != old || r.Reason != ticket.RenewApprovalExpired || !r.Checked || r.Unchanged() ||
		len(r.Changes) != 1 || r.Changes[0].Input != "sha256" || r.Changes[0].Change != ticket.ChangeUnchecked {
		t.Fatalf("renewal %+v", rt.Renews)
	}
	ot, err := ticket.VerifyTicket(pub, hub.Ticket(old))
	if err != nil || rt.PlanHash != ot.PlanHash || rt.PolicyFP != ot.PolicyFP || rt.Screen.Header != ot.Screen.Header {
		t.Fatalf("renewal of another plan: %+v / %+v %v", rt, ot, err)
	}
	// The old ticket again: the same renewal, not a third ticket.
	_, r = w.call(rpc.MethodRun, rpc.RunParams{Ticket: old, Requester: who})
	if again := pendingTicket(t, r); again != renewed {
		t.Fatalf("renewed twice: %s, %s", renewed, again)
	}
	if !strings.Contains(c.AuditLog(t), `"event":"renew"`) {
		t.Error("the renewal is not audited")
	}
	hub.DecideWith(renewed, ticket.Approved, "", nil, func(d *ticket.Decision) { d.At = ck.now() })
	r = runDecided(t, w, rpc.RunParams{Ticket: renewed, Requester: who})
	var res answers.Result
	if r.Error != nil || json.Unmarshal(r.Result, &res) != nil || len(res.Answers) != 1 || res.Answers[0].Result == nil || *res.Answers[0].Result != 1 {
		t.Fatalf("run: %+v %s", r.Error, r.Result)
	}
	if !hub.Consumed(renewed) {
		t.Error("the hub was not told the renewal was consumed")
	}
	if _, r := w.call(rpc.MethodRun, rpc.RunParams{Ticket: renewed, Requester: who}); r.Error == nil {
		t.Fatalf("the renewal ran twice: %s", r.Result)
	}
}

// A renewal the human asks from the hub right after a start, before any
// agent request, is checked: the console hands its hub the recompute
// before it serves (console.Options.OnHub). The approval it renews is one
// the console's clock refuses, aged out by the hub's request itself.
func TestHubRenewFromHubAfterStart(t *testing.T) {
	ck := &clock{t: time.Now().UTC().Truncate(time.Millisecond)}
	c, hub, pub, ho := hubConsoleAt(t, map[string]string{"invoices/a.pdf": "%PDF-1.7 synthetic"}, ck.now)
	w := dialWire(t, c.Socket)
	who := ipc.Requester{Agent: "dev-1", Token: "tok-1"}
	_, r := w.call(rpc.MethodPlan, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.answer1"}, Requester: who})
	old := pendingTicket(t, r)

	rc := c.Restart(t, consoletest.Options{Hub: ho, Now: ck.now})
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(rc.IO.Output())
		}
	})
	hub.DecideWith(old, ticket.Approved, "", nil, func(d *ticket.Decision) { d.At = ck.now().Add(2 * time.Hour) })
	// No agent request on this console: only the hub's renew request. It
	// is asked again until the decision has reached the console.
	rid := hub.Request(old, ticket.RequestRenew, nil, func(q *ticket.HubRequest) { q.At = ck.now() })
	var a struct{ Ticket, Error string }
	deadline := time.Now().Add(15 * time.Second)
	for {
		if ans := hub.Answer(rid); ans != nil {
			if err := json.Unmarshal(ans, &a); err == nil && a.Ticket != "" {
				break
			}
			hub.Requeue(rid)
		}
		if time.Now().After(deadline) {
			t.Fatalf("the renew request was never answered with a ticket: %s", hub.Answer(rid))
		}
		time.Sleep(50 * time.Millisecond)
	}
	rt, err := ticket.VerifyTicket(pub, hub.Ticket(a.Ticket))
	if err != nil {
		t.Fatal(err)
	}
	if r := rt.Renews; r == nil || r.Of != old || r.Reason != ticket.RenewApprovalExpired || !r.Checked ||
		len(r.Changes) != 1 || r.Changes[0].Input != "sha256" || r.Changes[0].Change != ticket.ChangeUnchecked {
		t.Fatalf("renewal from the hub after a start: %+v", rt.Renews)
	}
	if !strings.Contains(rc.AuditLog(t), `"event":"renew"`) {
		t.Error("the hub's renew request is not audited")
	}
	// The agent comes back with the old ticket: the same renewal, which
	// runs once approved.
	rw := dialWire(t, rc.Socket)
	_, r = rw.call(rpc.MethodRun, rpc.RunParams{Ticket: old, Requester: who})
	if n := pendingTicket(t, r); n != a.Ticket {
		t.Fatalf("agent run of the old ticket: %s, renewal %s", n, a.Ticket)
	}
	hub.DecideWith(a.Ticket, ticket.Approved, "", nil, func(d *ticket.Decision) { d.At = ck.now() })
	r = runDecided(t, rw, rpc.RunParams{Ticket: a.Ticket, Requester: who})
	var res answers.Result
	if r.Error != nil || json.Unmarshal(r.Result, &res) != nil || len(res.Answers) != 1 {
		t.Fatalf("run: %+v %s", r.Error, r.Result)
	}
}

// expireAndRenew has the hub approve ticket old at a time the console's
// clock is not at yet (unusable: lockfile's read_pii tickets expire this
// way only), and runs it: the agent hears pending with its renewal.
func expireAndRenew(t *testing.T, w *wire, hub *hubtest.Hub, ck *clock, old string, who ipc.Requester) string {
	t.Helper()
	hub.DecideWith(old, ticket.Approved, "", nil, func(d *ticket.Decision) { d.At = ck.now().Add(2 * time.Hour) })
	n := pendingTicket(t, runUntil(t, w, rpc.RunParams{Ticket: old, Requester: who}, old))
	if n == old {
		t.Fatal("the same ticket")
	}
	return n
}

// A plan whose object could not be fetched has no sha256 input: its
// renewal names the content unchecked all the same (never one click), and
// approved, it answers fetch_failed.
func TestHubRenewUnfetchedIsUnchecked(t *testing.T) {
	ck := &clock{t: time.Now().UTC().Truncate(time.Millisecond)}
	c, hub, pub, _ := hubConsoleAt(t, map[string]string{"invoices/a.pdf": "v1"}, ck.now)
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(c.IO.Output())
		}
	})
	w := dialWire(t, c.Socket)
	who := ipc.Requester{Agent: "dev-1", Token: "tok-1"}
	_, r := w.call(rpc.MethodPlan, rpc.PlanParams{Key: "invoices/missing.pdf", Plugins: []string{"test.answer1"}, Requester: who})
	old := pendingTicket(t, r)
	renewed := expireAndRenew(t, w, hub, ck, old, who)
	rt, err := ticket.VerifyTicket(pub, hub.Ticket(renewed))
	if err != nil {
		t.Fatal(err)
	}
	if r := rt.Renews; r == nil || r.Of != old || !r.Checked || r.Unchanged() ||
		len(r.Changes) != 1 || r.Changes[0].Input != "sha256" || r.Changes[0].Change != ticket.ChangeUnchecked {
		t.Fatalf("renewal of an unfetched plan: %+v", rt.Renews)
	}
	hub.DecideWith(renewed, ticket.Approved, "", nil, func(d *ticket.Decision) { d.At = ck.now() })
	if r := runDecided(t, w, rpc.RunParams{Ticket: renewed, Requester: who}); r.Error == nil || r.Error.Message != string(answers.CodeFetchFailed) {
		t.Fatalf("run of the renewal: %+v %s", r.Error, r.Result)
	}
}

// The content changes after a renewal was asked (its sha256 unchecked):
// approved, the renewal's run fetches again and refuses the other bytes
// stale before any plugin.
func TestHubRenewalThenContentChangedIsStale(t *testing.T) {
	ck := &clock{t: time.Now().UTC().Truncate(time.Millisecond)}
	c, hub, _, _ := hubConsoleAt(t, map[string]string{"invoices/a.pdf": "v1"}, ck.now)
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(c.IO.Output())
		}
	})
	w := dialWire(t, c.Socket)
	who := ipc.Requester{Agent: "dev-1", Token: "tok-1"}
	_, r := w.call(rpc.MethodPlan, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.answer1"}, Requester: who})
	renewed := expireAndRenew(t, w, hub, ck, pendingTicket(t, r), who)
	if err := os.WriteFile(filepath.Join(c.Root, "invoices", "a.pdf"), []byte("v2, another file"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := c.AuditLog(t)
	hub.DecideWith(renewed, ticket.Approved, "", nil, func(d *ticket.Decision) { d.At = ck.now() })
	if r := runDecided(t, w, rpc.RunParams{Ticket: renewed, Requester: who}); r.Error == nil || r.Error.Message != string(answers.CodeStale) {
		t.Fatalf("renewal run on changed content: %+v %s", r.Error, r.Result)
	}
	// The run is refused before any plugin: its head record says stale,
	// no plugin record follows.
	refused := false
	for _, l := range strings.Split(strings.TrimSpace(strings.TrimPrefix(c.AuditLog(t), before)), "\n") {
		var rec struct{ Event, Decision, Code string }
		if json.Unmarshal([]byte(l), &rec) != nil || rec.Event != "run" {
			continue
		}
		switch rec.Decision {
		case "plugin", "answered":
			t.Fatalf("a plugin ran on other bytes: %s", l)
		case "refused":
			refused = refused || rec.Code == string(answers.CodeStale)
		}
	}
	if !refused {
		t.Fatalf("no refused stale run audited:\n%s", c.AuditLog(t))
	}
}

// A policy tightened on disk (approved at a restart) between the plan and
// its expiry: the renewal is refused policy_changed (plan again). The
// agent hears the outcome only; the console prints the cause and audits
// it.
func TestHubRenewPolicyChanged(t *testing.T) {
	ck := &clock{t: time.Now().UTC().Truncate(time.Millisecond)}
	c, hub, _, ho := hubConsoleAt(t, map[string]string{"invoices/a.pdf": "v1"}, ck.now)
	w := dialWire(t, c.Socket)
	who := ipc.Requester{Agent: "dev-1", Token: "tok-1"}
	_, r := w.call(rpc.MethodPlan, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.answer1"}, Requester: who})
	old := pendingTicket(t, r)

	f, err := os.OpenFile(c.ConfigPath(), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("max_object_bytes = \"1KiB\"\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	rc := c.Restart(t, consoletest.Options{Answers: []string{"y"}, Hub: ho, Now: ck.now})
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(rc.IO.Output())
		}
	})
	rw := dialWire(t, rc.Socket)
	hub.DecideWith(old, ticket.Approved, "", nil, func(d *ticket.Decision) { d.At = ck.now().Add(2 * time.Hour) })
	r = runUntil(t, rw, rpc.RunParams{Ticket: old, Requester: who}, old)
	if r.Error == nil || r.Error.Code != ipc.CodeTimeout || strings.Contains(r.Error.Message, "policy") {
		t.Fatalf("renewal across a policy change: %+v %s", r.Error, r.Result)
	}
	if out := rc.IO.Output(); !strings.Contains(out, "could not be asked again (policy_changed)") {
		t.Fatalf("the cause is not printed:\n%s", out)
	}
	if log := rc.AuditLog(t); !strings.Contains(log, `"reason":"policy_changed"`) {
		t.Fatalf("the cause is not audited:\n%s", log)
	}
}
