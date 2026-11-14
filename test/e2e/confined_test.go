package e2e

import (
	"bufio"
	"net"
	"strings"
	"testing"

	"github.com/DavidGodefroid/lockkit/console"
	"github.com/DavidGodefroid/lockkit/ipc"

	"github.com/DavidGodefroid/lockfile/internal/testenv/consoletest"
)

// helloAs dials the console and sends a hello carrying conf.
func helloAs(t *testing.T, socket string, conf *ipc.Confinement) (*wire, ipc.Response) {
	t.Helper()
	c, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	w := &wire{t: t, conn: c, r: bufio.NewReader(c)}
	_, resp := w.call(ipc.MethodHello, ipc.HelloParams{ProtocolMajor: ipc.ProtocolMajor, Client: "e2e", Confinement: conf})
	return w, resp
}

var outside = &ipc.Confinement{Reasons: []string{"in_container"}}

// A profile without unconfined = true refuses an agent that does not run
// confined, and one that sends no report (old_client), with
// CodeUnconfined; nothing is served on that connection (lockkit#133).
func TestUnconfinedAgentRefused(t *testing.T) {
	c := consoletest.Start(t, consoletest.Options{Answers: []string{"y"}, Profile: "unconfined = false\n"})
	for _, conf := range []*ipc.Confinement{outside, nil} {
		w, resp := helloAs(t, c.Socket, conf)
		if resp.Error == nil || resp.Error.Code != ipc.CodeUnconfined {
			t.Fatalf("hello %+v: %+v, want CodeUnconfined", conf, resp.Error)
		}
		if _, r := w.call(ipc.MethodStatus, nil); r.Error == nil {
			t.Fatalf("status served after a refused hello: %s", r.Result)
		}
	}
	if log := c.AuditLog(t); !strings.Contains(log, console.DecisionRefusedUnconfined) {
		t.Errorf("no %s record:\n%s", console.DecisionRefusedUnconfined, log)
	}
	// A confined agent is served, unless this test process holds a group
	// owning a runtime socket (docker, on a CI runner): the console then
	// refuses it whatever it reports.
	_, resp := helloAs(t, c.Socket, &ipc.Confinement{Confined: true})
	if resp.Error != nil && (resp.Error.Code != ipc.CodeUnconfined || !strings.Contains(resp.Error.Message, "runtime_group")) {
		t.Fatalf("confined hello: %+v", resp.Error)
	}
}

// unconfined = true on a non-production profile serves the agent, under
// the red banner.
func TestUnconfinedProfileServes(t *testing.T) {
	c := consoletest.Start(t, consoletest.Options{Answers: []string{"y"}, Profile: "unconfined = true\n"})
	if !strings.Contains(c.IO.Output(), console.UnconfinedBanner) {
		t.Errorf("no banner at start:\n%s", c.IO.Output())
	}
	w, resp := helloAs(t, c.Socket, outside)
	if resp.Error != nil {
		t.Fatalf("hello: %+v", resp.Error)
	}
	if _, r := w.call(ipc.MethodStatus, nil); r.Error != nil {
		t.Fatalf("status: %+v", r.Error)
	}
}

// A production profile with unconfined = true does not start.
func TestUnconfinedProductionRefused(t *testing.T) {
	c := consoletest.Start(t, consoletest.Options{Answers: []string{"y"}, Profile: "production = true\nunconfined = true\n", MayRefuse: true})
	if c.Err == nil || !strings.Contains(c.Err.Error(), "unconfined is never allowed on a production profile") {
		t.Fatalf("start: %v\n%s", c.Err, c.IO.Output())
	}
	if strings.Contains(c.IO.Output(), "PROMPT") {
		t.Errorf("asked before refusing:\n%s", c.IO.Output())
	}
}
