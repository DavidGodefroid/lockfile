package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DavidGodefroid/lockkit/ipc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/DavidGodefroid/lockfile/internal/answers"
	"github.com/DavidGodefroid/lockfile/internal/clientcmd"
	"github.com/DavidGodefroid/lockfile/internal/mcpserver"
	"github.com/DavidGodefroid/lockfile/internal/rpc"
	"github.com/DavidGodefroid/lockfile/internal/testenv"
	"github.com/DavidGodefroid/lockfile/internal/testenv/consoletest"
)

// The plugins of the closed-output scenarios: answers, a hang (timeout), a
// crash, junk output, an unofficial one, and one tampered with mid-session.
var scenarioPlugins = []consoletest.Plugin{
	{Name: "test.answer1"}, {Name: "test.answer0"}, {Name: "test.printthenhang", TimeoutS: 1},
	{Name: "test.panic"}, {Name: "test.bigout"}, {Name: "acme/test.answer1"}, {Name: "test.tamper", Binary: "answer1"},
}

const leak = "it is 2048 bytes, /srv/x/john.pdf"

// scenarios runs every method and outcome against a fresh console and
// returns it with the raw response lines.
func scenarios(t *testing.T) (*consoletest.Console, *wire, []string) {
	t.Helper()
	c := consoletest.Start(t, consoletest.Options{
		// policy; plan 1 approved; denied with a reason; missing object;
		// oversized object; then no answer at all (timeout).
		Answers: []string{"y", "y", "n " + leak, "y", "y"},
		Plugins: scenarioPlugins,
		Files:   map[string]string{"invoices/a.pdf": "%PDF-1.7 synthetic", "invoices/big.pdf": strings.Repeat("x", 2048)},
		Profile: "max_object_bytes = \"1KiB\"\n",
	})
	w := dialWire(t, c.Socket)
	var agent []string // what the agent itself sent
	plan := func(p rpc.PlanParams) ipc.Response {
		agent = append(agent, p.Key, p.Handle)
		agent = append(agent, p.Plugins...)
		_, r := w.call(rpc.MethodPlan, p)
		return r
	}
	run := func(id string) ipc.Response {
		_, r := w.call(rpc.MethodRun, rpc.RunParams{PlanID: id})
		return r
	}
	planID := func(r ipc.Response) string {
		var p rpc.PlanResult
		if r.Error != nil || json.Unmarshal(r.Result, &p) != nil {
			t.Fatalf("plan: %+v\n%s", r.Error, c.IO.Output())
		}
		return p.PlanID
	}

	w.call(ipc.MethodStatus, nil)
	w.call(rpc.MethodPlugins, nil)
	// 1. Approved: 1, 0, hang, crash, junk, unofficial.
	all := []string{"test.answer1", "test.answer0", "test.printthenhang", "test.panic", "test.bigout", "acme/test.answer1"}
	id := planID(plan(rpc.PlanParams{Key: "invoices/a.pdf", Plugins: all, Intent: "check the invoice"}))
	r := run(id)
	var res answers.Result
	if r.Error != nil || json.Unmarshal(r.Result, &res) != nil || len(res.Answers) != 6 {
		t.Fatalf("run: %+v %s\n%s", r.Error, r.Result, c.IO.Output())
	}
	got := make([]string, len(res.Answers))
	for i, a := range res.Answers {
		got[i] = a.Error
		if a.Result != nil {
			got[i] = fmt.Sprint(*a.Result)
		}
	}
	if want := []string{"1", "0", "plugin_error", "plugin_error", "plugin_error", "1"}; !slices.Equal(got, want) ||
		res.Answers[5].Warning != answers.WarningUnofficial {
		t.Fatalf("answers %v, want %v", got, want)
	}
	agent = append(agent, res.Handle)
	if r := run(id); r.Error == nil || r.Error.Code != ipc.CodeNoSuchPlan {
		t.Fatalf("second run: %+v", r)
	}
	// 2. Denied with a reason that holds file metadata.
	if r := plan(rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.answer1"}}); r.Error == nil || r.Error.Code != ipc.CodeDenied {
		t.Fatalf("denied: %+v", r)
	}
	// 3. Refused before any approval.
	plan(rpc.PlanParams{Key: "../etc/passwd", Plugins: []string{"test.answer1"}})
	plan(rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.nope"}})
	var many []string
	for i := range 17 {
		many = append(many, fmt.Sprintf("test.x%d", i))
	}
	plan(rpc.PlanParams{Key: "invoices/a.pdf", Plugins: many})
	plan(rpc.PlanParams{Handle: "f_unknown", Plugins: []string{"test.answer1"}})
	// 4. Approved, then the fetch fails: missing, oversized.
	run(planID(plan(rpc.PlanParams{Key: "invoices/missing.pdf", Plugins: []string{"test.answer1"}})))
	run(planID(plan(rpc.PlanParams{Key: "invoices/big.pdf", Plugins: []string{"test.answer1"}})))
	run("pl_0000000000")
	// 5. Recall, release, recall after release.
	w.call(rpc.MethodAnswers, rpc.HandleParams{Handle: res.Handle})
	w.call(rpc.MethodRelease, rpc.HandleParams{Handle: res.Handle})
	w.call(rpc.MethodAnswers, rpc.HandleParams{Handle: res.Handle})
	// 6. A plugin tampered with after the human approved it.
	bin := filepath.Join(c.PluginDir, "plugins", "test.tamper", "1.0.0", "plugin")
	if err := os.Chmod(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if r := plan(rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.tamper"}}); r.Error == nil || r.Error.Message != "plugin_tampered" {
		t.Fatalf("tampered: %+v", r)
	}
	// 7. Protocol faults and lockkit's own methods.
	w.call(rpc.MethodPlan, map[string]any{"key": "invoices/a.pdf", "plugins": []string{"test.answer1"}, "bogus": 1})
	w.call("fs.list", nil)
	w.call(ipc.MethodChangeRequest, ipc.ChangeParams{Change: "max_object_bytes = 1GiB"})
	agent = append(agent, "max_object_bytes = 1GiB")
	// 8. Nobody answers: timeout.
	if r := plan(rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.answer1"}}); r.Error == nil || r.Error.Code != ipc.CodeTimeout {
		t.Fatalf("timeout: %+v", r)
	}
	return c, w, agent
}

// TestWireIsClosed: every response of every method, over the real socket
// and framing, holds only the closed values of docs/security-model.md and what the agent sent.
func TestWireIsClosed(t *testing.T) {
	_, w, agent := scenarios(t)
	v := testenv.ClosedVocab(agent...)
	for _, q := range scenarioPlugins {
		v.Strings[q.Name] = true
		v.Strings["Is it "+q.Name+"?"] = true
	}
	for _, line := range w.Lines {
		v.CheckJSON(t, line)
	}
	all := string(slicesJoin(w.Lines))
	for _, s := range []string{"john", "2048", "/srv", "synthetic", "/tmp", "goroutine", "no such file"} {
		if strings.Contains(all, s) {
			t.Errorf("wire holds %q", s)
		}
	}
	if !strings.Contains(all, `"approval_denied"`) || !strings.Contains(all, `"approval_timeout"`) || !strings.Contains(all, `"fetch_failed"`) {
		t.Fatalf("scenarios incomplete:\n%s", all)
	}
}

func slicesJoin(lines [][]byte) []byte {
	var out []byte
	for _, l := range lines {
		out = append(out, l...)
	}
	return out
}

var sha256Hex = regexp.MustCompile(`\b[0-9a-f]{64}\b`)

// TestConsoleWindowIsClosed: in desktop level 1 the agent can read the
// console window; after start-up it shows no size, hash, path or source
// error text.
func TestConsoleWindowIsClosed(t *testing.T) {
	c, _, _ := scenarios(t)
	var lines []string
	for _, l := range strings.Split(c.IO.OutputFrom(c.StartLines), "\n") {
		// lockkit echoes the reason the human typed with a denial: the
		// human's own words (documented residual), not lockfile's.
		if !strings.Contains(l, "denied: "+leak) {
			lines = append(lines, l)
		}
	}
	out := strings.Join(lines, "\n")
	for _, s := range []string{"john", "2048", "/srv", "synthetic", c.Work, c.Root, "no such file", "1KiB", "goroutine", "permission denied"} {
		if strings.Contains(out, s) {
			t.Errorf("console window shows %q", s)
		}
	}
	if m := regexp.MustCompile(`\d+ ?(bytes|B|KiB|MiB)\b`).FindString(out); m != "" {
		t.Errorf("console window shows a size %q", m)
	}
	if m := sha256Hex.FindString(out); m != "" {
		t.Errorf("console window shows a sha256 %s", m)
	}
	for _, want := range []string{"refused fetch_failed", ": answered"} {
		if !strings.Contains(out, want) {
			t.Errorf("console window lacks %q:\n%s", want, out)
		}
	}
}

// TestRunQuantumEndToEnd: through the socket, a run answers at the next
// multiple of answer_quantum (5 s) after the run call, whatever the
// plugins took, and a fetch failure after approval too.
func TestRunQuantumEndToEnd(t *testing.T) {
	c := consoletest.Start(t, consoletest.Options{
		Answers: []string{"y", "y", "y", "y"},
		Plugins: []consoletest.Plugin{{Name: "test.slow1", TimeoutS: 10}, {Name: "test.slow6", TimeoutS: 10}},
		Profile: "answer_quantum = \"5s\"\n",
	})
	w := dialWire(t, c.Socket)
	timed := func(key, plugin string) (time.Duration, ipc.Response) {
		_, r := w.call(rpc.MethodPlan, rpc.PlanParams{Key: key, Plugins: []string{plugin}})
		var p rpc.PlanResult
		if r.Error != nil || json.Unmarshal(r.Result, &p) != nil {
			t.Fatalf("plan: %+v", r.Error)
		}
		start := time.Now()
		_, r = w.call(rpc.MethodRun, rpc.RunParams{PlanID: p.PlanID})
		return time.Since(start), r
	}
	for _, tc := range []struct {
		name, key, plugin string
		min, max          time.Duration
		refused           bool
	}{
		{"1.2 s plugin", "invoices/a.pdf", "test.slow1", 5 * time.Second, 7 * time.Second, false},
		{"6 s plugin", "invoices/a.pdf", "test.slow6", 10 * time.Second, 12 * time.Second, false},
		{"fetch failure", "invoices/missing.pdf", "test.slow1", 5 * time.Second, 7 * time.Second, true},
	} {
		d, r := timed(tc.key, tc.plugin)
		if d < tc.min || d > tc.max {
			t.Errorf("%s: answered after %s, want [%s, %s]", tc.name, d, tc.min, tc.max)
		}
		if (r.Error != nil) != tc.refused {
			t.Errorf("%s: %+v", tc.name, r.Error)
		}
	}
}

// TestSharedSocketDir: in a separated setup the socket directory (2750)
// lists the running profiles' sockets and nothing else, and the console
// serves members of the clients group only.
func TestSharedSocketDir(t *testing.T) {
	allowed := true
	c := consoletest.Start(t, consoletest.Options{Answers: []string{"y"}, Shared: true, Clients: func() bool { return allowed }})
	fi, err := os.Stat(c.SharedDir)
	if err != nil || fi.Mode()&os.ModeSetgid == 0 || fi.Mode().Perm() != 0o750 {
		t.Fatalf("socket dir %v %v", fi.Mode(), err)
	}
	entries, err := os.ReadDir(c.SharedDir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
		if e.Type()&os.ModeSocket == 0 {
			t.Errorf("%s in the socket dir is not a socket", e.Name())
		}
	}
	if !slices.Equal(names, []string{"user-samples.sock"}) || filepath.Dir(c.Socket) != c.SharedDir {
		t.Fatalf("socket dir lists %v (socket %s)", names, c.Socket)
	}
	if sfi, _ := os.Stat(c.Socket); sfi.Mode().Perm() != 0o660 {
		t.Fatalf("socket mode %v", sfi.Mode())
	}
	w := dialWire(t, c.Socket)
	w.call(ipc.MethodStatus, nil)
	// Out of the clients group: the console closes the connection unserved.
	allowed = false
	conn, err := net.Dial("unix", c.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = ipc.WriteMsg(conn, ipc.Request{JSONRPC: "2.0", ID: 1, Method: ipc.MethodHello, Params: []byte(`{"protocol_major":1}`)})
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 64)
	if n, err := conn.Read(buf); err == nil || n > 0 {
		t.Fatalf("a non-client was answered: %q %v", buf[:n], err)
	}
}

// TestMCPAndCLIAgree: the same plan through the CLI's engine and the MCP
// server gives the same answers.
func TestMCPAndCLIAgree(t *testing.T) {
	consoletest.Start(t, consoletest.Options{Answers: []string{"y", "y", "y"},
		Plugins: []consoletest.Plugin{{Name: "test.answer1"}, {Name: "test.printthenhang", TimeoutS: 1}, {Name: "acme/test.answer0"}}})
	params := rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.answer1", "test.printthenhang", "acme/test.answer0"}}
	ctx := context.Background()
	cli, err := clientcmd.PlanAndRun(ctx, "samples", params)
	if err != nil {
		t.Fatal(err)
	}
	ct, st := mcp.NewInMemoryTransports()
	ss, err := mcpserver.New("").Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	call := func(name string, args map[string]any) map[string]any {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || res.IsError {
			t.Fatalf("%s: %v %+v", name, err, res)
		}
		b, _ := json.Marshal(res.StructuredContent)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		return m
	}
	p := call(mcpserver.ToolPlan, map[string]any{"key": params.Key, "plugins": params.Plugins})
	r := call(mcpserver.ToolRun, map[string]any{"plan_id": p["plan_id"]})
	viaMCP, _ := json.Marshal(r["answers"])
	viaCLI, _ := json.Marshal(cli.Answers)
	var a, b any
	_ = json.Unmarshal(viaMCP, &a)
	_ = json.Unmarshal(viaCLI, &b)
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		t.Fatalf("MCP %s\nCLI %s", ja, jb)
	}
}
