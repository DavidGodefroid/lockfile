package e2e

import (
	"crypto/ed25519"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DavidGodefroid/lockkit/hubclient/hubtest"
	"github.com/DavidGodefroid/lockkit/ipc"
	"github.com/DavidGodefroid/lockkit/ticket"

	"github.com/DavidGodefroid/lockfile/internal/answers"
	"github.com/DavidGodefroid/lockfile/internal/rpc"
	"github.com/DavidGodefroid/lockfile/internal/testenv/consoletest"
)

// secret is the value the human types: it must reach the plugin and
// nothing else.
const secret = "alice@example.org"

var inputFiles = map[string]string{
	"invoices/a.pdf": "%PDF-1.7 page 4512: contact " + secret + " for the refund",
	"invoices/b.pdf": "%PDF-1.7 nothing to see",
}

// inputsConsole is a hub console with test.contains: input needle (an
// email), detail on.
func inputsConsole(t *testing.T, o consoletest.Options) (*consoletest.Console, *hubtest.Hub, ed25519.PublicKey) {
	t.Helper()
	ho, hub, pub := consoletest.NewHub(t)
	o.Answers = []string{"y"}
	o.Plugins = []consoletest.Plugin{{Name: "test.answer1"}, {Name: "test.contains", Binary: "contains", InputKind: "email"}}
	o.Files, o.Hub = inputFiles, ho
	c := consoletest.Start(t, o)
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(c.IO.Output())
		}
	})
	return c, hub, pub
}

// approveTyped approves ticket tk as `lox run` does with the human's typed
// values: sealed to the ticket's one-use key, over the ticket as signed.
func approveTyped(t *testing.T, hub *hubtest.Hub, pub ed25519.PublicKey, tk string, values map[string][]byte) *ticket.Ticket {
	t.Helper()
	got, err := ticket.VerifyTicket(pub, hub.Ticket(tk))
	if err != nil {
		t.Fatal(err)
	}
	hub.DecideWith(tk, ticket.Approved, "", nil, func(d *ticket.Decision) {
		box, err := ticket.SealInputs(got, values)
		if err != nil {
			t.Fatal(err)
		}
		d.Secret = box
	})
	return got
}

func oneAnswer(t *testing.T, r ipc.Response) int {
	t.Helper()
	var res answers.Result
	if r.Error != nil || json.Unmarshal(r.Result, &res) != nil || len(res.Answers) != 1 || res.Answers[0].Result == nil {
		t.Fatalf("run: %+v %s", r.Error, r.Result)
	}
	return *res.Answers[0].Result
}

// The agent plans test.contains on two files with ${email}; the human
// types alice@example.org once, on the first ticket's approval, sealed in
// the decision. The answers are 1 and 0; the second plan asks no new
// value and says it reuses it. alice@example.org is in no client
// response, plan answer, audit record, hub-side JSON, console window or
// file of the console's state.
func TestTypedValueNeverLeaves(t *testing.T) {
	c, hub, pub := inputsConsole(t, consoletest.Options{})
	w := dialWire(t, c.Socket)
	who := ipc.Requester{Agent: "dev-1", Token: "tok-1"}
	bind := map[string]string{"test.contains.needle": "${email}"}
	var wire [][]byte
	call := func(method string, p any) ipc.Response {
		raw, r := w.call(method, p)
		wire = append(wire, raw)
		return r
	}

	r := call(rpc.MethodPlan, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.contains"}, Inputs: bind, Requester: who})
	tk1 := pendingTicket(t, r)
	got := approveTyped(t, hub, pub, tk1, map[string][]byte{"email": []byte(secret)})
	if got.Subject == nil || !slices.Equal(got.Subject.Inputs, []string{"email"}) || got.Subject.SecretTo == "" || !got.Flags.NeverAuto || got.Kind != ticket.KindRead {
		t.Fatalf("ticket asks %+v %+v %s", got.Subject, got.Flags, got.Kind)
	}
	var plan1 map[string]any
	_ = json.Unmarshal(r.Result, &plan1)
	if _, ok := plan1["held_inputs"]; ok {
		t.Fatalf("held before anything was typed: %s", r.Result)
	}
	r = runUntil(t, w, rpc.RunParams{Ticket: tk1, Requester: who}, tk1)
	wire = append(wire, r.Result)
	if v := oneAnswer(t, r); v != 1 {
		t.Fatalf("a.pdf: %d", v)
	}

	// The second file: the value is held, no new prompt.
	r = call(rpc.MethodPlan, rpc.PlanParams{Key: "invoices/b.pdf", Plugins: []string{"test.contains"}, Inputs: bind, Requester: who})
	tk2 := pendingTicket(t, r)
	var plan2 rpc.PlanResult
	if json.Unmarshal(r.Result, &plan2) != nil || !slices.Equal(plan2.HeldInputs, []string{"email"}) {
		t.Fatalf("held_inputs: %s", r.Result)
	}
	t2, err := ticket.VerifyTicket(pub, hub.Ticket(tk2))
	if err != nil {
		t.Fatal(err)
	}
	if (t2.Subject != nil && len(t2.Subject.Inputs) > 0) || !t2.Flags.NeverAuto {
		t.Fatalf("second ticket %+v %+v", t2.Subject, t2.Flags)
	}
	if !strings.Contains(string(hub.Ticket(tk2)), "reused") || !strings.Contains(string(hub.Ticket(tk2)), "replayed on 1 files this session") {
		t.Fatalf("second screen does not say reused: %s", hub.Ticket(tk2))
	}
	hub.Decide(tk2, ticket.Approved, "")
	r = runUntil(t, w, rpc.RunParams{Ticket: tk2, Requester: who}, tk2)
	wire = append(wire, r.Result)
	if v := oneAnswer(t, r); v != 0 {
		t.Fatalf("b.pdf: %d", v)
	}
	call(rpc.MethodPlugins, nil)

	// The exhaustive walk.
	check := func(what string, b []byte) {
		if strings.Contains(string(b), secret) {
			t.Errorf("%s holds the typed value:\n%s", what, b)
		}
	}
	for i, b := range wire {
		check("client response "+string(rune('0'+i)), b)
	}
	check("audit log", []byte(c.AuditLog(t)))
	check("console window", []byte(c.IO.Output()))
	for _, id := range hub.Tickets() {
		check("ticket "+id, hub.Ticket(id))
	}
	for _, d := range hub.Decisions() {
		check("decision", d)
	}
	for _, dir := range []string{c.StateDir(), c.Work} {
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() {
				// A spool of a fetched file holds what the file holds.
				if b, err := os.ReadFile(p); err == nil && string(b) != inputFiles["invoices/a.pdf"] {
					check(p, b)
				}
			}
			return nil
		})
	}
	// The audit names the placeholder, never its value.
	if !strings.Contains(c.AuditLog(t), `"inputs":["email"]`) {
		t.Errorf("the audit does not name the placeholder:\n%s", c.AuditLog(t))
	}
	// Clear results are off: the detail was not printed.
	if strings.Contains(c.IO.Output(), "found at byte") || strings.Contains(c.IO.Output(), "shown here only") {
		t.Errorf("detail printed with clear results off:\n%s", c.IO.Output())
	}
}

// A literal binding is refused end to end, and nothing reaches the hub.
func TestLiteralInputRefused(t *testing.T) {
	c, hub, _ := inputsConsole(t, consoletest.Options{})
	w := dialWire(t, c.Socket)
	raw, r := w.call(rpc.MethodPlan, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.contains"},
		Inputs: map[string]string{"test.contains.needle": secret}})
	if r.Error == nil || r.Error.Message != string(answers.CodeLiteralInput) || !strings.Contains(string(raw), "E_LITERAL_INPUT") {
		t.Fatalf("literal: %s", raw)
	}
	if hub.Pushed() != 0 || strings.Contains(string(raw), secret) || strings.Contains(c.AuditLog(t), secret) || strings.Contains(c.IO.Output(), secret) {
		t.Fatalf("pushed %d; %s", hub.Pushed(), raw)
	}
}

// A typed value that is not of its input's kind refuses the run, the
// human is told by name, the value never echoed.
func TestTypedValueInvalid(t *testing.T) {
	c, hub, pub := inputsConsole(t, consoletest.Options{})
	w := dialWire(t, c.Socket)
	const bad = "not-an-address"
	_, r := w.call(rpc.MethodPlan, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.contains"},
		Inputs: map[string]string{"test.contains.needle": "${email}"}})
	tk := pendingTicket(t, r)
	approveTyped(t, hub, pub, tk, map[string][]byte{"email": []byte(bad)})
	r = runUntil(t, w, rpc.RunParams{Ticket: tk}, tk)
	if r.Error == nil || r.Error.Message != string(answers.CodeInputInvalid) {
		t.Fatalf("run: %+v %s", r.Error, r.Result)
	}
	if !strings.Contains(c.IO.Output(), "${email}: not a valid email; plan again") {
		t.Fatalf("console:\n%s", c.IO.Output())
	}
	raw, _ := json.Marshal(r)
	if strings.Contains(string(raw), bad) || strings.Contains(c.IO.Output(), bad) || strings.Contains(c.AuditLog(t), bad) {
		t.Fatal("the bad value was echoed")
	}
	// Not held: a new plan asks for it again.
	_, r = w.call(rpc.MethodPlan, rpc.PlanParams{Key: "invoices/b.pdf", Plugins: []string{"test.contains"},
		Inputs: map[string]string{"test.contains.needle": "${email}"}})
	again, err := ticket.VerifyTicket(pub, hub.Ticket(pendingTicket(t, r)))
	if err != nil || again.Subject == nil || !slices.Equal(again.Subject.Inputs, []string{"email"}) {
		t.Fatalf("the refused value is held: %+v %v", again, err)
	}
}

// With clear results on, the plugin's detail is printed on the console
// window, escaped, under "shown here only"; the agent still gets 1. Where
// lockkit refuses clear results (desktop levels 1 and 2), the console does
// not start with them and nothing is printed.
func TestDetailShownWithClearResults(t *testing.T) {
	c, hub, pub := inputsConsole(t, consoletest.Options{ShowResults: true, MayRefuse: true})
	if c.Err != nil {
		if !strings.Contains(c.Err.Error()+c.IO.Output(), "level") {
			t.Fatalf("refused for another reason: %v\n%s", c.Err, c.IO.Output())
		}
		t.Skipf("clear results refused at this desktop level: %v", c.Err)
	}
	w := dialWire(t, c.Socket)
	_, r := w.call(rpc.MethodPlan, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.contains"},
		Inputs: map[string]string{"test.contains.needle": "${email}"}})
	tk := pendingTicket(t, r)
	approveTyped(t, hub, pub, tk, map[string][]byte{"email": []byte(secret)})
	r = runUntil(t, w, rpc.RunParams{Ticket: tk}, tk)
	if v := oneAnswer(t, r); v != 1 {
		t.Fatalf("answer %d", v)
	}
	out := c.IO.Output()
	if !strings.Contains(out, "test.contains detail, shown here only; the agent got 1/0:") || !strings.Contains(out, `found at byte 28\x1b[2J`) ||
		strings.Contains(out, "\x1b[2J") {
		t.Fatalf("detail not shown escaped:\n%s", out)
	}
	if strings.Contains(string(r.Result), "found") || strings.Contains(c.AuditLog(t), "found at") {
		t.Fatal("the detail reached the agent or the audit")
	}
}
