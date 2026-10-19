package clientcmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DavidGodefroid/lockkit/client"
	"github.com/DavidGodefroid/lockkit/console"
	"github.com/DavidGodefroid/lockkit/ipc"
	"github.com/DavidGodefroid/lockkit/refusal"

	"github.com/DavidGodefroid/lockfile/internal/answers"
	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/rpc"
)

// runtimeDir points the private runtime dir at a fresh short directory.
func runtimeDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("/tmp", "lfcli")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	t.Setenv(product.P.RuntimeDirEnv(), d)
	dir, err := product.P.RuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

// fakeConsole serves hello for profile and answers every other method
// with handle; a nil handle never answers.
func fakeConsole(t *testing.T, path, profile string, handle func(ipc.Request) ipc.Response) {
	t.Helper()
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				r := bufio.NewReader(c)
				for {
					var req ipc.Request
					if ipc.ReadMsg(r, &req) != nil {
						return
					}
					var resp ipc.Response
					switch {
					case req.Method == ipc.MethodHello:
						resp = console.OKResp(req.ID, ipc.HelloResult{ProtocolMajor: ipc.ProtocolMajor, Profile: profile})
					case handle == nil:
						time.Sleep(time.Hour)
					default:
						resp = handle(req)
					}
					if ipc.WriteMsg(c, resp) != nil {
						return
					}
				}
			}()
		}
	}()
}

func statusOf(profile string, production bool) func(ipc.Request) ipc.Response {
	return func(req ipc.Request) ipc.Response {
		return console.OKResp(req.ID, console.StatusResult{Profile: profile, Production: production, Product: rpc.Status{Protocol: 1, Sandbox: "ok"}})
	}
}

func TestSourcesListsRunningConsoles(t *testing.T) {
	dir := runtimeDir(t)
	probeTimeout = 500 * time.Millisecond
	t.Cleanup(func() { probeTimeout = 2 * time.Second })
	fakeConsole(t, filepath.Join(dir, "user-b.sock"), "b", statusOf("b", true))
	fakeConsole(t, filepath.Join(dir, "user-a.sock"), "a", statusOf("a", false))
	fakeConsole(t, filepath.Join(dir, "user-busy.sock"), "busy", nil)
	// A project console is not lockfile's: left out.
	fakeConsole(t, filepath.Join(dir, "abcd1234-p.sock"), "p", statusOf("p", false))
	// A socket that names another profile than it serves: left out.
	fakeConsole(t, filepath.Join(dir, "user-liar.sock"), "other", statusOf("other", false))
	// A socket left by a dead console.
	dead, err := net.Listen("unix", filepath.Join(dir, "user-dead.sock"))
	if err != nil {
		t.Fatal(err)
	}
	dead.(*net.UnixListener).SetUnlinkOnClose(false)
	dead.Close()
	if err := os.WriteFile(filepath.Join(dir, "user-file.sock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Sources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []rpc.Source{{Profile: "a"}, {Profile: "b", Production: true}, {Profile: "busy", Busy: true}}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	}
}

func TestSourcesHashedName(t *testing.T) {
	// A runtime dir long enough that a 64-character profile is hashed.
	base, err := os.MkdirTemp("/tmp", "lfcli")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	d := filepath.Join(base, strings.Repeat("d", 30))
	t.Setenv(product.P.RuntimeDirEnv(), d)
	dir, _ := product.P.RuntimeDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("l", 64)
	p, err := client.SocketPath(product.P, "/", long)
	if err != nil || !strings.Contains(p, "~") {
		t.Fatalf("socket path %s %v: not hashed", p, err)
	}
	fakeConsole(t, p, long, statusOf(long, true))
	got, err := Sources(context.Background())
	if err != nil || len(got) != 1 || got[0] != (rpc.Source{Profile: long, Production: true}) {
		t.Fatalf("got %+v %v", got, err)
	}
}

func TestStatusOfBusyConsole(t *testing.T) {
	dir := runtimeDir(t)
	probeTimeout = 300 * time.Millisecond
	t.Cleanup(func() { probeTimeout = 2 * time.Second })
	fakeConsole(t, filepath.Join(dir, "user-busy.sock"), "busy", nil)
	for _, arg := range []string{"busy", ""} {
		start := time.Now()
		out, ok, err := Statuses(context.Background(), arg)
		if err != nil || !ok || len(out) != 1 || !out[0].Busy || out[0].Profile != "busy" || out[0].Console != nil {
			t.Fatalf("status %q: %+v %v %v", arg, out, ok, err)
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Fatalf("status %q took %v", arg, d)
		}
		if l := StatusLines(out); len(l) != 1 || !strings.Contains(l[0], "busy") {
			t.Fatalf("lines %q", l)
		}
	}
}

func TestResolveBusyHashedConsole(t *testing.T) {
	base, err := os.MkdirTemp("/tmp", "lfcli")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	d := filepath.Join(base, strings.Repeat("d", 30))
	t.Setenv(product.P.RuntimeDirEnv(), d)
	dir, _ := product.P.RuntimeDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	probeTimeout = 300 * time.Millisecond
	t.Cleanup(func() { probeTimeout = 2 * time.Second })
	long := strings.Repeat("l", 64)
	p, err := client.SocketPath(product.P, "/", long)
	if err != nil || !strings.Contains(p, "~") {
		t.Fatalf("socket path %s %v: not hashed", p, err)
	}
	// Busy: it does not even answer hello until the human decides.
	ln, err := net.Listen("unix", p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { c.Close() })
		}
	}()
	if got, err := Sources(context.Background()); err != nil || len(got) != 0 {
		t.Fatalf("sources %+v %v", got, err)
	}
	if got, err := Resolve("", long); err != nil || got != long {
		t.Fatalf("resolve %q %v", got, err)
	}
	var ce *Error
	if _, err := Resolve("", "other"); !errors.As(err, &ce) || ce.Msg != "unknown_profile" {
		t.Fatalf("resolve other: %v", err)
	}
}

func TestSourcesNoDir(t *testing.T) {
	d, err := os.MkdirTemp("/tmp", "lfcli")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	t.Setenv(product.P.RuntimeDirEnv(), d)
	got, err := Sources(context.Background())
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("got %#v %v", got, err)
	}
}

func TestUnknownConsoleErrorScrubbed(t *testing.T) {
	dir := runtimeDir(t)
	reply := func(r ipc.Response) {
		fakeConsole(t, filepath.Join(dir, "user-x.sock"), "x", func(req ipc.Request) ipc.Response {
			r.ID = req.ID
			return r
		})
	}
	leaky := "/srv/x/john.pdf: 4821 bytes"
	refused := console.ErrResp(0, ipc.CodeRefused, leaky)
	refused.Error.Data = &refusal.Info{Code: product.Code(answers.CodeFetchFailed), Retry: refusal.RetryStop, Hint: leaky}
	badData := console.ErrResp(0, ipc.CodeRefused, string(answers.CodeFetchFailed))
	badData.Error.Data = &refusal.Info{Code: product.Code(answers.CodeKeyOutsidePrefix), Hint: leaky}
	goodHintLeak := console.ErrResp(0, ipc.CodeRefused, string(answers.CodeFetchFailed))
	goodHintLeak.Error.Data = &refusal.Info{Code: product.Code(answers.CodeFetchFailed), Retry: refusal.RetryRewrite, Hint: leaky}
	cases := []struct {
		name string
		resp ipc.Response
		kind Kind
		msg  string
	}{
		{"internal", console.ErrResp(0, ipc.CodeInternal, leaky), KindOther, rpc.MsgInternal},
		{"refused with free text", refused, KindOther, rpc.MsgInternal},
		{"refused with other data", badData, KindOther, rpc.MsgInternal},
		{"refused, hint replaced", goodHintLeak, KindRefused, string(answers.CodeFetchFailed)},
		{"denied with reason", console.ErrResp(0, ipc.CodeDenied, "denied by the human: "+leaky), KindDenied, string(answers.CodeApprovalDenied)},
		{"timeout", console.ErrResp(0, ipc.CodeTimeout, leaky), KindDenied, string(answers.CodeApprovalTimeout)},
		{"no such plan", console.ErrResp(0, ipc.CodeNoSuchPlan, leaky), KindRefused, rpc.MsgNoSuchPlan},
		{"conn lost", console.ErrResp(0, ipc.CodeConnLost, leaky), KindOther, rpc.MsgConnLost},
		{"params", console.ErrResp(0, ipc.CodeInvalidParams, leaky), KindOther, rpc.MsgParams},
		{"unknown code", console.ErrResp(0, -1, leaky), KindOther, rpc.MsgInternal},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_ = os.Remove(filepath.Join(dir, "user-x.sock"))
			reply(c.resp)
			_, err := Plugins(context.Background(), "x")
			var ce *Error
			if !errors.As(err, &ce) || ce.Kind != c.kind || ce.Msg != c.msg {
				t.Fatalf("err = %#v", err)
			}
			b, _ := json.Marshal(ce.Tool())
			if strings.Contains(string(b), "john") || strings.Contains(string(b), "4821") {
				t.Fatalf("leak: %s", b)
			}
			if ce.Refusal != nil {
				rc := ce.Refusal.Code
				if ce.Refusal.Hint != rc.Hint() || ce.Refusal.Retry != rc.Retry() {
					t.Fatalf("refusal not the registered one: %+v", ce.Refusal)
				}
			}
		})
	}
}

func TestSuccessfulResultsChecked(t *testing.T) {
	dir := runtimeDir(t)
	one := 1
	goodInfo := rpc.PluginInfo{Plugin: "acme/pdf.parses", Version: "1.0.0", Marketplace: "acme", Warning: answers.WarningUnofficial,
		Question: "Does it parse?", One: "it parses", Zero: "it does not"}
	goodResult := answers.Result{Handle: "f_0123456789", Answers: []answers.Answer{{Plugin: "pdf.parses", Version: "1.0.0", Official: true, Result: &one}}}
	serve := func(v any) {
		_ = os.Remove(filepath.Join(dir, "user-x.sock"))
		fakeConsole(t, filepath.Join(dir, "user-x.sock"), "x", func(req ipc.Request) ipc.Response { return console.OKResp(req.ID, v) })
	}
	ctx := context.Background()
	plan := rpc.PlanParams{Key: "k", Plugins: []string{"pdf.parses"}}
	calls := map[string]func() error{
		"plugins": func() error { _, err := Plugins(ctx, "x"); return err },
		"plan":    func() error { _, err := Plan(ctx, "x", plan); return err },
		"run":     func() error { _, err := Run(ctx, "x", "pl_0123456789"); return err },
		"answers": func() error { _, err := Answers(ctx, "x", "f_0123456789"); return err },
		"status":  func() error { _, err := GetStatus(ctx, "x"); return err },
	}
	mod := func(f func(*rpc.PluginInfo)) []rpc.PluginInfo { p := goodInfo; f(&p); return []rpc.PluginInfo{p} }
	res := func(f func(*answers.Result)) answers.Result {
		r := goodResult
		r.Answers = []answers.Answer{goodResult.Answers[0]}
		f(&r)
		return r
	}
	status := func(profile, sandbox string) console.StatusResult {
		return console.StatusResult{Profile: profile, Product: rpc.Status{Protocol: 1, Sandbox: sandbox}}
	}
	cases := []struct {
		name, call string
		reply      any
		ok         bool
	}{
		{"plugins ok", "plugins", []rpc.PluginInfo{goodInfo}, true},
		{"plugin name", "plugins", mod(func(p *rpc.PluginInfo) { p.Plugin = "/srv/x/john.pdf" }), false},
		{"plugin version", "plugins", mod(func(p *rpc.PluginInfo) { p.Version = "4821 bytes" }), false},
		{"marketplace", "plugins", mod(func(p *rpc.PluginInfo) { p.Marketplace = "a b" }), false},
		{"question control", "plugins", mod(func(p *rpc.PluginInfo) { p.Question = "q\x1b[2J" }), false},
		{"empty one", "plugins", mod(func(p *rpc.PluginInfo) { p.One = "" }), false},
		{"warning", "plugins", mod(func(p *rpc.PluginInfo) { p.Warning = "size 4821" }), false},
		{"plan ok", "plan", rpc.PlanResult{PlanID: "pl_0123456789", Answers: 1}, true},
		{"plan id", "plan", rpc.PlanResult{PlanID: "pl_/srv/john", Answers: 1}, false},
		{"plan answers", "plan", rpc.PlanResult{PlanID: "pl_0123456789", Answers: 4821}, false},
		{"run ok", "run", goodResult, true},
		{"run handle", "run", res(func(r *answers.Result) { r.Handle = "/tmp/f_x" }), false},
		{"run plugin", "run", res(func(r *answers.Result) { r.Answers[0].Plugin = "john.pdf 4821" }), false},
		{"run version", "run", res(func(r *answers.Result) { r.Answers[0].Version = "sha256" }), false},
		{"answers ok", "answers", goodResult, true},
		{"answers other handle", "answers", res(func(r *answers.Result) { r.Handle = "f_9876543210" }), false},
		{"status ok", "status", status("x", "ok"), true},
		{"status sandbox", "status", status("x", "/srv/x 4821"), false},
		{"status profile", "status", status("other", "ok"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			serve(c.reply)
			err := calls[c.call]()
			if c.ok {
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				return
			}
			var ce *Error
			if !errors.As(err, &ce) || ce.Kind != KindOther || ce.Msg != rpc.MsgInternal {
				t.Fatalf("err = %#v", err)
			}
		})
	}
}

func TestResolveTextsAndSingleProbe(t *testing.T) {
	dir := runtimeDir(t)
	var probes atomic.Int32
	counted := func(profile string) func(ipc.Request) ipc.Response {
		return func(req ipc.Request) ipc.Response {
			probes.Add(1)
			return statusOf(profile, false)(req)
		}
	}
	fakeConsole(t, filepath.Join(dir, "user-a.sock"), "a", counted("a"))
	if p, err := Resolve("", ""); err != nil || p != "a" {
		t.Fatalf("one console: %q %v", p, err)
	}
	if n := probes.Load(); n != 1 {
		t.Fatalf("%d probes for one call, want 1", n)
	}
	fakeConsole(t, filepath.Join(dir, "user-b.sock"), "b", statusOf("b", false))
	var ce *Error
	if _, err := Resolve("", ""); !errors.As(err, &ce) || ce.Msg != severalText {
		t.Fatalf("several: %v", err)
	}
	if _, err := Resolve("a", "b"); !errors.As(err, &ce) || ce.Msg != pinnedText {
		t.Fatalf("pinned: %v", err)
	}
	if p, err := Resolve("a", "a"); err != nil || p != "a" {
		t.Fatalf("pinned same: %q %v", p, err)
	}
}

func TestResultKeepsClosedValues(t *testing.T) {
	two, one := 2, 1
	in := answers.Result{Handle: "f_x", Answers: []answers.Answer{
		{Plugin: "a", Version: "1.0.0", Official: true, Result: &one},
		{Plugin: "b", Version: "1.0.0", Official: true, Result: &two},
		{Plugin: "c", Version: "1.0.0", Official: false, Error: "segfault at /srv"},
	}}
	out := result(in)
	if *out.Answers[0].Result != 1 || out.Answers[0].Error != "" {
		t.Fatalf("a: %+v", out.Answers[0])
	}
	if out.Answers[1].Result != nil || out.Answers[1].Error != "plugin_error" {
		t.Fatalf("b: %+v", out.Answers[1])
	}
	if out.Answers[2].Error != "plugin_error" || out.Answers[2].Warning != answers.WarningUnofficial {
		t.Fatalf("c: %+v", out.Answers[2])
	}
}

func TestNoConsoleIsUnknownProfile(t *testing.T) {
	runtimeDir(t)
	_, err := Plugins(context.Background(), "nope")
	var ce *Error
	if !errors.As(err, &ce) || ce.Kind != KindNoConsole || ce.Msg != "unknown_profile" ||
		ce.Refusal == nil || ce.Refusal.Code != "E_UNKNOWN_PROFILE" || !strings.Contains(ce.Refusal.Hint, "lockfile console --profile nope") {
		t.Fatalf("err = %#v", err)
	}
	if _, err := Resolve("", "nope"); !errors.As(err, &ce) || ce.Msg != "unknown_profile" {
		t.Fatalf("resolve: %v", err)
	}
	if _, err := Resolve("", ""); !errors.As(err, &ce) || ce.Kind != KindNoConsole {
		t.Fatalf("resolve none: %v", err)
	}
	if p, err := Resolve("pinned", ""); err != nil || p != "pinned" {
		t.Fatalf("pinned: %q %v", p, err)
	}
}
