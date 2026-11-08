package e2e

import (
	"strings"
	"testing"

	"github.com/DavidGodefroid/lockfile/internal/answers"
	"github.com/DavidGodefroid/lockfile/internal/rpc"
	"github.com/DavidGodefroid/lockfile/internal/testenv/consoletest"
)

// The profile's max_pending reaches the console's hub client: a plan over
// the limit is refused and never reaches the hub; the status screen names
// the limits in force (lockkit#150).
func TestHubTicketLimits(t *testing.T) {
	ho, hub, _ := consoletest.NewHub(t)
	c := consoletest.Start(t, consoletest.Options{
		Answers: []string{"y"},
		Plugins: []consoletest.Plugin{{Name: "test.answer1"}},
		Files:   map[string]string{"invoices/a.pdf": "v1", "invoices/b.pdf": "v1"},
		Profile: "tickets_per_window = 5\nticket_window = \"10m\"\nmax_pending = 1\n",
		Hub:     ho,
	})
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(c.IO.Output())
		}
	})
	if out := c.IO.Output(); !strings.Contains(out, "tickets: 5 per 10m0s · 1 pending") {
		t.Errorf("the status screen does not name the ticket limits:\n%s", out)
	}
	w := dialWire(t, c.Socket)
	_, r := w.call(rpc.MethodPlan, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.answer1"}})
	pendingTicket(t, r)
	before := len(hub.Tickets())
	_, r = w.call(rpc.MethodPlan, rpc.PlanParams{Key: "invoices/b.pdf", Plugins: []string{"test.answer1"}})
	// Not submitted: the plan is refused (the agent's detailed rate message
	// comes with lockkit's Asker), the human sees why.
	if r.Error == nil || r.Error.Message != string(answers.CodeApprovalDenied) {
		t.Fatalf("over max_pending: %+v %s", r.Error, r.Result)
	}
	if out := c.IO.Output(); !strings.Contains(out, "not submitted") || !strings.Contains(out, "max_pending") {
		t.Errorf("the console does not say why:\n%s", out)
	}
	if n := len(hub.Tickets()); n != before {
		t.Fatalf("a ticket over the limit reached the hub: %d tickets, want %d", n, before)
	}
}
