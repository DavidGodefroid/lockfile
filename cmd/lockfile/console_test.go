package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DavidGodefroid/lockkit/client"
	"github.com/DavidGodefroid/lockkit/console"
	"github.com/DavidGodefroid/lockkit/hubclient/hubtest"
	"github.com/DavidGodefroid/lockkit/ipc"
	"github.com/DavidGodefroid/lockkit/ticket"

	"github.com/DavidGodefroid/lockfile/internal/answers"
	"github.com/DavidGodefroid/lockfile/internal/backend"
	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/rpc"
	"github.com/DavidGodefroid/lockfile/internal/testenv"
	"github.com/DavidGodefroid/lockfile/internal/testenv/consoletest"
)

// The harness of package consoletest, through the console command itself.
var sameUserIsolation = consoletest.SameUserIsolation

type consoleRun struct {
	io     *consoletest.ScriptIO
	socket string
	work   string
	done   chan int
	// hub is the fake lockhub the console is attached to.
	hub   *hubtest.Hub
	state string
}

// hubArgs are the hub flags lox up writes, for o.
func hubArgs(o console.HubOptions) []string {
	return []string{"--hub", o.URL, "--hub-key-file", o.KeyFile, "--instance", o.Instance}
}

// startConsole runs `lockfile console --profile samples` in-process on a
// local source holding invoices/a.pdf, with the given plugins installed,
// attached to a fake lockhub.
func startConsole(t *testing.T, answersScript []string, plugins ...string) *consoleRun {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "invoices"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "invoices", "a.pdf"), []byte("%PDF-1.7 synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(t.TempDir(), "config.toml")
	body := "[profiles.samples]\nsource = \"local\"\npath = \"" + root + "\"\nprefix = \"invoices/\"\nanswer_quantum = \"1s\"\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	store := testenv.TempStore(t)
	for _, p := range plugins {
		testenv.Install(t, store, p, testenv.BuildPlugin(t, p[strings.LastIndex(p, ".")+1:]), "Is it "+p+"?", 2)
	}
	work := filepath.Join(t.TempDir(), "work")
	ho, hub, _ := consoletest.NewHub(t)
	r := &consoleRun{io: consoletest.NewScriptIO(answersScript...), work: work, done: make(chan int, 1), hub: hub, state: t.TempDir()}
	ready := make(chan string, 1)
	testConsoleHooks = &consoleHooks{
		io: r.io, isolation: sameUserIsolation(t), harden: func() error { return nil },
		stateDir: r.state, configPath: cfg, ready: func(s string) { ready <- s }, home: ho.Home,
		backend: func(o *backend.Options) {
			o.PluginDir, o.WorkDir = store.Dir, work
			o.ProcHidden = func() error { return nil }
		},
	}
	t.Cleanup(func() { testConsoleHooks = nil })
	go func() {
		code, _, errText := runArgs(t, "", append([]string{"console", "--profile", "samples"}, hubArgs(ho)...)...)
		if code != 0 {
			t.Logf("console exit %d: %s", code, errText)
		}
		r.done <- code
	}()
	select {
	case r.socket = <-ready:
	case code := <-r.done:
		t.Fatalf("console exited %d before listening:\n%s", code, r.io.Output())
	case <-time.After(20 * time.Second):
		t.Fatalf("no socket:\n%s", r.io.Output())
	}
	return r
}

func (r *consoleRun) stop(t *testing.T) {
	t.Helper()
	c, err := client.Dial(product.P, t.TempDir(), "samples")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.Call(context.Background(), ipc.MethodLogout, nil, nil)
	select {
	case <-r.done:
	case <-time.After(10 * time.Second):
		t.Fatal("console did not stop")
	}
}

func TestConsoleRefusesWithoutSandbox(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfg, []byte("[profiles.samples]\nsource = \"local\"\npath = \"/tmp\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	io := consoletest.NewScriptIO("y")
	testConsoleHooks = &consoleHooks{
		io: io, isolation: sameUserIsolation(t), harden: func() error { return nil }, stateDir: t.TempDir(), configPath: cfg,
		backend: func(o *backend.Options) {
			o.WorkDir = filepath.Join(t.TempDir(), "work")
			o.Probe = func() (int, bool, error) { return 0, false, errors.New("no landlock") }
		},
	}
	t.Cleanup(func() { testConsoleHooks = nil })
	ho, _, _ := consoletest.NewHub(t)
	code, _, errText := runArgs(t, "", append([]string{"console", "--profile", "samples"}, hubArgs(ho)...)...)
	if code != product.ExitError || !strings.Contains(errText, "sandbox") {
		t.Fatalf("exit %d: %s\n%s", code, errText, io.Output())
	}
}

func TestConsoleUsage(t *testing.T) {
	if code, _, _ := runArgs(t, "", "console"); code != product.ExitUsage {
		t.Fatalf("no profile: exit %d", code)
	}
	// Launched by lox up only: without the hub flags, or with only some,
	// refused before anything else.
	for _, args := range [][]string{
		{"console", "--profile", "samples"},
		{"console", "--profile", "samples", "--hub", "http://lockhub:8443", "--instance", "files"},
		{"console", "--profile", "samples", "--hub-key-file", "/run/lockfamily/hub.pub", "--instance", "files"},
	} {
		code, _, errText := runArgs(t, "", args...)
		if code != product.ExitUsage || !strings.Contains(errText, "launched by `lox up` only") || !strings.Contains(errText, "usage: lockfile console --profile NAME --hub URL") {
			t.Errorf("%v: exit %d: %s", args, code, errText)
		}
	}
	if code, _, _ := runArgs(t, "", "console", "--profile", "samples", "--auto"); code != product.ExitUsage {
		t.Errorf("--auto: exit %d", code)
	}
}

func TestConsoleEndToEnd(t *testing.T) {
	// First start: approve the policy at the terminal; the plan is
	// approved on the hub.
	r := startConsole(t, []string{"y"}, "test.answer1")
	c, err := client.Dial(product.P, t.TempDir(), "samples")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	var plan rpc.PlanResult
	if err := c.Call(ctx, rpc.MethodPlan, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.answer1"}}, &plan); err != nil || plan.Status != "pending" {
		t.Fatalf("plan: %+v %v\n%s", plan, err, r.io.Output())
	}
	r.hub.Decide(plan.Ticket, ticket.Approved, "")
	var res answers.Result
	deadline := time.Now().Add(15 * time.Second)
	for {
		var raw json.RawMessage
		if err := c.Call(ctx, rpc.MethodRun, rpc.RunParams{Ticket: plan.Ticket}, &raw); err != nil {
			t.Fatalf("run: %v\n%s", err, r.io.Output())
		}
		var p rpc.PlanResult
		if json.Unmarshal(raw, &p) != nil || p.Status != "pending" {
			if err := json.Unmarshal(raw, &res); err != nil {
				t.Fatalf("run result %s: %v", raw, err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the decision never arrived")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(res.Answers) != 1 || res.Answers[0].Result == nil || *res.Answers[0].Result != 1 {
		au, _ := os.ReadFile(filepath.Join(r.state, "lockfile", "audit.log"))
		t.Fatalf("result = %+v\n%s\n%s", res, r.io.Output(), au)
	}
	entries, err := os.ReadDir(r.work)
	if err != nil || len(entries) != 1 {
		t.Fatalf("work dir during the session: %v %v", entries, err)
	}
	r.stop(t)
	entries, _ = os.ReadDir(r.work)
	if len(entries) != 0 {
		t.Fatalf("work dir not emptied at exit: %v", entries)
	}
	if out := r.io.Output(); strings.Contains(out, "synthetic") || strings.Contains(out, r.work) {
		t.Fatalf("console output holds file data:\n%s", out)
	}
}

// The hub flags are required, and --auto is gone; hub-key prints the
// instance key lockhub pins, made once.
func TestConsoleHubFlagsAndHubKey(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfg, []byte("[profiles.samples]\nsource = \"local\"\npath = \"/tmp\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	testConsoleHooks = &consoleHooks{io: consoletest.NewScriptIO(), isolation: sameUserIsolation(t), harden: func() error { return nil },
		stateDir: t.TempDir(), configPath: cfg, home: home}
	t.Cleanup(func() { testConsoleHooks = nil })
	for _, args := range [][]string{
		{"--hub", "http://lockhub:8443"},
		{"--hub", "http://lockhub:8443", "--hub-key-file", "k", "--instance", "files", "--auto"},
	} {
		if code, _, errText := runArgs(t, "", append([]string{"console", "--profile", "samples"}, args...)...); code == product.ExitOK {
			t.Errorf("%v accepted: %s", args, errText)
		}
	}
	code, pub, errText := runArgs(t, "", "hub-key", "--instance", "files")
	if code != product.ExitOK || len(strings.TrimSpace(pub)) != 44 {
		t.Fatalf("hub-key: %d %q %s", code, pub, errText)
	}
	if _, again, _ := runArgs(t, "", "hub-key", "--instance", "files"); again != pub {
		t.Fatal("hub-key changed the key")
	}
	if fi, err := os.Stat(filepath.Join(home, "hub", "files.key")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("instance key: %v %v", fi, err)
	}
}

// --allowed-uids is checked before anything starts, and reaches the
// isolation checks: without a separated setup it is refused there.
func TestConsoleAllowedUIDs(t *testing.T) {
	for _, bad := range []string{"0", "007", "1,,2", "x", "65534"} {
		code, _, errText := runArgs(t, "", "console", "--profile", "samples", "--allowed-uids", bad)
		if code != product.ExitUsage || !strings.Contains(errText, "--allowed-uids") {
			t.Errorf("--allowed-uids %q: exit %d: %s", bad, code, errText)
		}
	}
	if code, _, errText := runArgs(t, "", "console", "--profile", "samples", "--allowed-uids", "1000", "--allowed-uids", "1001"); code != product.ExitUsage {
		t.Errorf("--allowed-uids twice: exit %d: %s", code, errText)
	}
	cfg := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfg, []byte("[profiles.samples]\nsource = \"local\"\npath = \"/tmp\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	io := consoletest.NewScriptIO("y")
	testConsoleHooks = &consoleHooks{
		io: io, isolation: sameUserIsolation(t), harden: func() error { return nil }, stateDir: t.TempDir(), configPath: cfg,
		backend: func(o *backend.Options) { o.WorkDir = filepath.Join(t.TempDir(), "work") },
	}
	t.Cleanup(func() { testConsoleHooks = nil })
	ho, _, _ := consoletest.NewHub(t)
	code, _, errText := runArgs(t, "", append([]string{"console", "--profile", "samples", "--allowed-uids", "1000"}, hubArgs(ho)...)...)
	if code != product.ExitError || !strings.Contains(errText, "--allowed-uids needs the separated setup") {
		t.Fatalf("exit %d: %s\n%s", code, errText, io.Output())
	}
	if !strings.Contains(io.Output(), "allowed uids besides lockfile-clients (--allowed-uids): 1000") {
		t.Fatalf("the console does not print the uids:\n%s", io.Output())
	}
}
