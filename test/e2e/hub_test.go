package e2e

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DavidGodefroid/lockkit/console"
	"github.com/DavidGodefroid/lockkit/hubclient"
	"github.com/DavidGodefroid/lockkit/hubclient/hubtest"
	"github.com/DavidGodefroid/lockkit/ipc"
	"github.com/DavidGodefroid/lockkit/ticket"

	"github.com/DavidGodefroid/lockfile/internal/answers"
	"github.com/DavidGodefroid/lockfile/internal/rpc"
	"github.com/DavidGodefroid/lockfile/internal/testenv/consoletest"
)

const hubInstance = "files"

// hubConsole is a console in hub mode beside a fake lockhub.
func hubConsole(t *testing.T, files map[string]string) (*consoletest.Console, *hubtest.Hub, ed25519.PublicKey) {
	t.Helper()
	home := t.TempDir()
	key, err := hubclient.InstanceKey(home, hubInstance)
	if err != nil {
		t.Fatal(err)
	}
	pub := key.Public().(ed25519.PublicKey)
	hub := hubtest.New(t, hubInstance, pub)
	hubKey := filepath.Join(t.TempDir(), "hub.pub")
	if err := os.WriteFile(hubKey, []byte(base64.StdEncoding.EncodeToString(hub.Pub)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := consoletest.Start(t, consoletest.Options{
		Answers: []string{"y"}, // the policy, on the terminal; nothing else is asked here
		Plugins: []consoletest.Plugin{{Name: "test.answer1"}},
		Files:   files,
		Hub:     console.HubOptions{URL: hub.URL(), KeyFile: hubKey, Instance: hubInstance, Home: home},
	})
	return c, hub, pub
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
