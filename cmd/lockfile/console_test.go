package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DavidGodefroid/lockkit/client"
	"github.com/DavidGodefroid/lockkit/ipc"

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
}

// startConsole runs `lockfile console --profile samples` in-process on a
// local source holding invoices/a.pdf, with the given plugins installed.
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
	r := &consoleRun{io: consoletest.NewScriptIO(answersScript...), work: work, done: make(chan int, 1)}
	ready := make(chan string, 1)
	testConsoleHooks = &consoleHooks{
		io: r.io, isolation: sameUserIsolation(t), harden: func() error { return nil },
		stateDir: t.TempDir(), configPath: cfg, ready: func(s string) { ready <- s },
		backend: func(o *backend.Options) {
			o.PluginDir, o.WorkDir = store.Dir, work
			o.ProcHidden = func() error { return nil }
		},
	}
	t.Cleanup(func() { testConsoleHooks = nil })
	go func() {
		code, _, errText := runArgs(t, "", "console", "--profile", "samples")
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
	code, _, errText := runArgs(t, "", "console", "--profile", "samples")
	if code != product.ExitError || !strings.Contains(errText, "sandbox") {
		t.Fatalf("exit %d: %s\n%s", code, errText, io.Output())
	}
}

func TestConsoleUsage(t *testing.T) {
	if code, _, _ := runArgs(t, "", "console"); code != product.ExitUsage {
		t.Fatalf("no profile: exit %d", code)
	}
}

func TestConsoleEndToEnd(t *testing.T) {
	// First start: approve the policy; then approve the plan.
	r := startConsole(t, []string{"y", "y"}, "test.answer1")
	c, err := client.Dial(product.P, t.TempDir(), "samples")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	var plan rpc.PlanResult
	if err := c.Call(ctx, rpc.MethodPlan, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.answer1"}}, &plan); err != nil {
		t.Fatalf("plan: %v\n%s", err, r.io.Output())
	}
	var res answers.Result
	if err := c.Call(ctx, rpc.MethodRun, rpc.RunParams{PlanID: plan.PlanID}, &res); err != nil {
		t.Fatalf("run: %v\n%s", err, r.io.Output())
	}
	if len(res.Answers) != 1 || res.Answers[0].Result == nil || *res.Answers[0].Result != 1 {
		t.Fatalf("result = %+v", res)
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
