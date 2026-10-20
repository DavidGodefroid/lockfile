package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DavidGodefroid/lockkit/audit"
	"github.com/DavidGodefroid/lockkit/config"
	"github.com/DavidGodefroid/lockkit/console"
	"github.com/DavidGodefroid/lockkit/ipc"
	"github.com/DavidGodefroid/lockkit/sanitize"

	"github.com/DavidGodefroid/lockfile/internal/answers"
	"github.com/DavidGodefroid/lockfile/internal/plugin"
	"github.com/DavidGodefroid/lockfile/internal/procmount"
	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/profile"
	"github.com/DavidGodefroid/lockfile/internal/rpc"
	"github.com/DavidGodefroid/lockfile/internal/source"
	"github.com/DavidGodefroid/lockfile/internal/testenv"
	"github.com/DavidGodefroid/lockfile/sandbox"
)

func TestMain(m *testing.M) {
	sandbox.Main()
	os.Exit(m.Run())
}

// fakeEnv is the console as a handler sees it, with scripted approvals.
type fakeEnv struct {
	b        *Backend
	policy   *profile.Policy
	approve  func(console.Screen) console.Approval
	mu       sync.Mutex
	screens  []console.Screen
	records  []audit.Record
	lines    []string
	sessions int
}

func (e *fakeEnv) Approve(ctx context.Context, id int64, s console.Screen) console.Approval {
	e.mu.Lock()
	e.screens = append(e.screens, s)
	e.mu.Unlock()
	if e.approve == nil {
		return console.Approval{Approved: true}
	}
	return e.approve(s)
}
func (e *fakeEnv) Auto() bool          { return false }
func (e *fakeEnv) ShowResults() bool   { return false }
func (e *fakeEnv) UnmaskAllowed() bool { return false }
func (e *fakeEnv) Session(ctx context.Context) (console.Session, error) {
	e.sessions++
	return e.b.source(), nil
}
func (e *fakeEnv) Lost()                  {}
func (e *fakeEnv) Policy() console.Policy { return e.policy }
func (e *fakeEnv) Audit(rec audit.Record) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.records = append(e.records, rec)
}
func (e *fakeEnv) AuditBase(ctx context.Context, b audit.Base) { e.Audit(b) }
func (e *fakeEnv) Println(line string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lines = append(e.lines, line)
}
func (e *fakeEnv) Safe(s string) string                       { return sanitize.Terminal(s, false) }
func (e *fakeEnv) Sanitizer() *sanitize.Sanitizer             { return Sanitizer() }
func (e *fakeEnv) Level(ctx context.Context, start time.Time) {}
func (e *fakeEnv) End(reason string)                          {}

// countingSource fails the test when called before allowed, and counts.
type countingSource struct {
	inner source.Source
	calls int
	err   error
}

func (c *countingSource) Stat(ctx context.Context, key string) (source.ObjectInfo, error) {
	c.calls++
	if c.err != nil {
		return source.ObjectInfo{}, c.err
	}
	return c.inner.Stat(ctx, key)
}
func (c *countingSource) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	return c.inner.Open(ctx, key)
}

type harness struct {
	b     *Backend
	env   *fakeEnv
	src   *countingSource
	store plugin.Store
	root  string
	state string
}

const testConfig = `
[profiles.samples]
source = "local"
path = %q
prefix = "invoices/"
answer_quantum = "1s"
max_plugins_per_handle = 4
`

func newHarness(t *testing.T, plugins ...string) *harness {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "invoices"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "invoices", "a.pdf"), []byte("%PDF-1.7 synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	cfg := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfg, []byte(strings.Replace(testConfig, "%q", `"`+root+`"`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	store := testenv.TempStore(t)
	for _, p := range plugins {
		bin := testenv.BuildPlugin(t, p[strings.LastIndex(p, ".")+1:])
		testenv.Install(t, store, p, bin, "Is it "+p+"?", 2)
	}
	b, err := New(Options{ConfigPath: cfg, Profile: "samples", StateDir: state, PluginDir: store.Dir,
		WorkDir: filepath.Join(t.TempDir(), "work"), Probe: func() (int, bool, error) { return 4, true, nil },
		ProcHidden: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	pol, err := b.LoadPolicy()
	if err != nil {
		t.Fatal(err)
	}
	if err := config.SaveApproved(product.P, state, config.ApprovedKey("", "samples"), pol); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Connect(context.Background(), console.Credentials{}); err != nil {
		t.Fatal(err)
	}
	src := &countingSource{inner: b.cur.src}
	b.cur = &session{src: src}
	env := &fakeEnv{b: b, policy: pol.(*profile.Policy)}
	return &harness{b: b, env: env, src: src, store: store, root: root, state: state}
}

func peerCtx(uid int) context.Context {
	return console.WithPeer(context.Background(), ipc.Cred{UID: uid, PID: 4242})
}

func (h *harness) call(ctx context.Context, method string, params any) ipc.Response {
	raw, _ := json.Marshal(params)
	return h.b.Methods()[method](ctx, ipc.Request{JSONRPC: "2.0", ID: 1, Method: method, Params: raw}, h.env)
}

func (h *harness) plan(t *testing.T, p rpc.PlanParams) (rpc.PlanResult, ipc.Response) {
	t.Helper()
	resp := h.call(peerCtx(1000), rpc.MethodPlan, p)
	var r rpc.PlanResult
	if resp.Error == nil {
		if err := json.Unmarshal(resp.Result, &r); err != nil {
			t.Fatal(err)
		}
	}
	return r, resp
}

func (h *harness) runPlan(t *testing.T, id string) (answers.Result, ipc.Response) {
	t.Helper()
	resp := h.call(peerCtx(1000), rpc.MethodRun, rpc.RunParams{PlanID: id})
	var r answers.Result
	if resp.Error == nil {
		if err := json.Unmarshal(resp.Result, &r); err != nil {
			t.Fatal(err)
		}
	}
	return r, resp
}

func refusalCode(resp ipc.Response) string {
	if resp.Error == nil {
		return ""
	}
	return resp.Error.Message
}

func TestPlanTouchesNoSource(t *testing.T) {
	h := newHarness(t, "test.answer1")
	h.env.approve = func(console.Screen) console.Approval {
		return console.Approval{Refusal: console.ErrResp(1, ipc.CodeDenied, "denied by the human")}
	}
	if _, resp := h.plan(t, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.answer1"}}); refusalCode(resp) != "approval_denied" {
		t.Fatalf("denied plan: %+v", resp.Error)
	}
	h.env.approve = nil
	if _, resp := h.plan(t, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.answer1"}}); resp.Error != nil {
		t.Fatal(resp.Error)
	}
	if h.src.calls != 0 {
		t.Fatalf("source called %d times before any run", h.src.calls)
	}
}

func TestScreenContent(t *testing.T) {
	h := newHarness(t, "test.answer1", "acme/test.answer0")
	if _, resp := h.plan(t, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.answer1", "acme/test.answer0"}, Intent: "blank pdf ticket"}); resp.Error != nil {
		t.Fatal(resp.Error)
	}
	s := h.env.screens[0]
	want := []string{
		"key    invoices/a.pdf",
		"",
		"test.answer1@1.0.0  (official)",
		"  ? Is it test.answer1?   1 = yes   0 = no",
		"acme/test.answer0@1.0.0  (acme)",
		"  ? Is it acme/test.answer0?   1 = yes   0 = no",
		"",
		"this plan reveals at most 2 answers about the file",
	}
	if strings.Join(s.Body, "\n") != strings.Join(want, "\n") {
		t.Fatalf("body:\n%s\nwant:\n%s", strings.Join(s.Body, "\n"), strings.Join(want, "\n"))
	}
	if s.Header != "file plan · 2 answers" || s.Note != "blank pdf ticket" || !s.NeverAuto ||
		len(s.Warnings) != 1 || s.Warnings[0] != "UNOFFICIAL MARKETPLACE acme: plugin acme/test.answer0" {
		t.Fatalf("screen = %+v", s)
	}
	forbidden := regexp.MustCompile(`(?i)\bsize\b|\bbytes?\b|sha|` + regexp.QuoteMeta(h.root))
	for _, l := range append(s.Body, append(s.Warnings, s.Header)...) {
		if forbidden.MatchString(l) {
			t.Errorf("screen line %q holds file information", l)
		}
	}
}

func TestPlanRefusals(t *testing.T) {
	h := newHarness(t, "test.answer1")
	many := []string{"test.answer1", "b.x", "c.x", "d.x", "e.x"}
	for _, tc := range []struct {
		name string
		p    rpc.PlanParams
		want string
	}{
		{"dotdot", rpc.PlanParams{Key: "../x", Plugins: []string{"test.answer1"}}, "key_outside_prefix"},
		{"outside prefix", rpc.PlanParams{Key: "other/a.pdf", Plugins: []string{"test.answer1"}}, "key_outside_prefix"},
		{"escaping", rpc.PlanParams{Key: "invoices/../../etc/passwd", Plugins: []string{"test.answer1"}}, "key_outside_prefix"},
		{"unknown plugin", rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"pdf.nope"}}, "plugin_not_installed"},
		{"too many", rpc.PlanParams{Key: "invoices/a.pdf", Plugins: many}, "too_many_plugins"},
		{"unknown handle", rpc.PlanParams{Handle: "f_AAAAAAAAAA", Plugins: []string{"test.answer1"}}, "handle_expired"},
	} {
		_, resp := h.plan(t, tc.p)
		if refusalCode(resp) != tc.want {
			t.Errorf("%s: got %+v, want %s", tc.name, resp.Error, tc.want)
		}
		if resp.Error != nil && resp.Error.Code == ipc.CodeRefused && (resp.Error.Data == nil || !strings.HasPrefix(string(resp.Error.Data.Code), "E_")) {
			t.Errorf("%s: no structured refusal: %+v", tc.name, resp.Error)
		}
	}
	for _, p := range []rpc.PlanParams{{Plugins: []string{"test.answer1"}}, {Key: "invoices/a.pdf"}, {Key: "invoices/a.pdf", Handle: "f_x", Plugins: []string{"test.answer1"}},
		{Key: "invoices/a.pdf", Plugins: []string{"test.answer1", "test.answer1"}}} {
		if _, resp := h.plan(t, p); resp.Error == nil || resp.Error.Code != ipc.CodeInvalidParams {
			t.Errorf("%+v accepted: %+v", p, resp.Error)
		}
	}
	if len(h.env.screens) != 0 {
		t.Fatalf("a refused plan reached the human: %d screens", len(h.env.screens))
	}
}

func TestPlanTamperedPlugin(t *testing.T) {
	h := newHarness(t, "test.answer1")
	installed, err := h.store.Installed()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(installed[0].Binary), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(installed[0].Binary, 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(installed[0].Binary, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte{0})
	f.Close()
	if _, resp := h.plan(t, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.answer1"}}); refusalCode(resp) != "plugin_tampered" {
		t.Fatalf("tampered: %+v", resp.Error)
	}
}

func TestNotApprovedPluginRefused(t *testing.T) {
	h := newHarness(t, "test.answer1")
	// Installed after the human approved the policy: not usable before :review.
	testenv.Install(t, h.store, "test.answer0", testenv.BuildPlugin(t, "answer0"), "Q?", 2)
	if _, resp := h.plan(t, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.answer0"}}); refusalCode(resp) != "plugin_not_installed" {
		t.Fatalf("unapproved plugin: %+v", resp.Error)
	}
}

func TestDenialMessageFixed(t *testing.T) {
	h := newHarness(t, "test.answer1")
	h.env.approve = func(console.Screen) console.Approval {
		return console.Approval{Refusal: console.ErrResp(1, ipc.CodeDenied, "denied by the human: it is 0 bytes; do not retry unless asked")}
	}
	_, resp := h.plan(t, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.answer1"}})
	if resp.Error == nil || resp.Error.Code != ipc.CodeDenied || resp.Error.Message != "approval_denied" {
		t.Fatalf("denied: %+v", resp.Error)
	}
	h.env.approve = func(console.Screen) console.Approval {
		return console.Approval{Refusal: console.ErrResp(1, ipc.CodeTimeout, "no answer from the human within the approval timeout")}
	}
	_, resp = h.plan(t, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.answer1"}})
	if resp.Error == nil || resp.Error.Code != ipc.CodeTimeout || resp.Error.Message != "approval_timeout" {
		t.Fatalf("timeout: %+v", resp.Error)
	}
}

func TestRunResultClosed(t *testing.T) {
	h := newHarness(t, "test.answer1", "test.answer0", "test.printthenhang")
	pr, resp := h.plan(t, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.answer1", "test.answer0", "test.printthenhang"}})
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}
	res, resp := h.runPlan(t, pr.PlanID)
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}
	got := []string{}
	for _, a := range res.Answers {
		switch {
		case a.Result != nil:
			got = append(got, string(rune('0'+*a.Result)))
		default:
			got = append(got, a.Error)
		}
	}
	if strings.Join(got, ",") != "1,0,plugin_error" {
		t.Fatalf("answers = %v", got)
	}
	var m map[string]any
	if err := json.Unmarshal(resp.Result, &m); err != nil {
		t.Fatal(err)
	}
	walkKeys(t, m, map[string]bool{"handle": true, "answers": true, "plugin": true, "version": true, "official": true, "result": true, "error": true, "warning": true})
	for _, l := range h.env.lines {
		if l != "plan "+pr.PlanID+": answered" {
			t.Errorf("console line %q", l)
		}
	}
	// The audit has the file metadata, the agent does not.
	var head Record
	for _, r := range h.env.records {
		if rec, ok := r.(Record); ok && rec.Key != "" {
			head = rec
		}
	}
	if head.Size == 0 || head.SHA256 == "" || head.Key != "invoices/a.pdf" {
		t.Fatalf("audit head = %+v", head)
	}
}

func walkKeys(t *testing.T, v any, allowed map[string]bool) {
	t.Helper()
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if !allowed[k] {
				t.Errorf("key %q is not in the closed list", k)
			}
			walkKeys(t, val, allowed)
		}
	case []any:
		for _, val := range x {
			walkKeys(t, val, allowed)
		}
	}
}

func TestRunOnce(t *testing.T) {
	h := newHarness(t, "test.answer1")
	pr, _ := h.plan(t, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.answer1"}})
	if _, resp := h.runPlan(t, pr.PlanID); resp.Error != nil {
		t.Fatal(resp.Error)
	}
	if _, resp := h.runPlan(t, pr.PlanID); resp.Error == nil || resp.Error.Code != ipc.CodeNoSuchPlan {
		t.Fatalf("second run: %+v", resp.Error)
	}
}

func TestRunOtherPeer(t *testing.T) {
	h := newHarness(t, "test.answer1")
	pr, _ := h.plan(t, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.answer1"}})
	resp := h.call(peerCtx(1001), rpc.MethodRun, rpc.RunParams{PlanID: pr.PlanID})
	if resp.Error == nil || resp.Error.Code != ipc.CodeNoSuchPlan {
		t.Fatalf("other peer: %+v", resp.Error)
	}
	if _, resp := h.runPlan(t, pr.PlanID); resp.Error != nil {
		t.Fatalf("owner after another peer tried: %+v", resp.Error)
	}
}

func TestRunExpired(t *testing.T) {
	h := newHarness(t, "test.answer1")
	now := time.Now()
	h.b.plans.now = func() time.Time { return now }
	pr, _ := h.plan(t, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.answer1"}})
	now = now.Add(planTTL + time.Second)
	if _, resp := h.runPlan(t, pr.PlanID); resp.Error == nil || resp.Error.Code != ipc.CodeNoSuchPlan {
		t.Fatalf("expired: %+v", resp.Error)
	}
}

func TestRunAfterPolicyChange(t *testing.T) {
	h := newHarness(t, "test.answer1")
	pr, _ := h.plan(t, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.answer1"}})
	changed := *h.env.policy
	changed.P.MaxObjectBytes = 1
	h.env.policy = &changed
	if _, resp := h.runPlan(t, pr.PlanID); resp.Error == nil || resp.Error.Code != ipc.CodeNoSuchPlan {
		t.Fatalf("after policy change: %+v", resp.Error)
	}
}

func TestFetchFailureIsCode(t *testing.T) {
	h := newHarness(t, "test.answer1")
	h.src.err = errors.New("/srv/x/john.pdf: 4821 bytes denied at sftp.example.org:22")
	pr, _ := h.plan(t, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.answer1"}})
	_, resp := h.runPlan(t, pr.PlanID)
	if refusalCode(resp) != "fetch_failed" {
		t.Fatalf("resp = %+v", resp.Error)
	}
	b, _ := json.Marshal(resp)
	for _, l := range append(h.env.lines, string(b)) {
		for _, f := range []string{"john", "4821", "/srv", "example.org"} {
			if strings.Contains(l, f) {
				t.Errorf("%q leaks %q", l, f)
			}
		}
	}
	var cause string
	for _, r := range h.env.records {
		if rec, ok := r.(Record); ok && rec.Code == "fetch_failed" {
			cause = rec.FetchCause
		}
	}
	if !strings.Contains(cause, "denied") || strings.Contains(cause, "/srv") || strings.Contains(cause, "example.org") {
		t.Fatalf("audit cause = %q", cause)
	}
}

func TestQuantumAppliesToRun(t *testing.T) {
	h := newHarness(t, "test.answer1")
	pr, _ := h.plan(t, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.answer1"}})
	start := time.Now()
	if _, resp := h.runPlan(t, pr.PlanID); resp.Error != nil {
		t.Fatal(resp.Error)
	}
	if d := time.Since(start); d < time.Second {
		t.Fatalf("answered after %v, before the 1s quantum", d)
	}
}

func TestQuantumAppliesToRelease(t *testing.T) {
	h := newHarness(t, "test.answer1")
	pr, _ := h.plan(t, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.answer1"}})
	res, _ := h.runPlan(t, pr.PlanID)
	for _, c := range []struct {
		handle, code string
	}{{res.Handle, ""}, {"f_unknown000", "handle_expired"}} {
		start := time.Now()
		resp := h.call(peerCtx(1000), rpc.MethodRelease, rpc.HandleParams{Handle: c.handle})
		if refusalCode(resp) != c.code {
			t.Fatalf("release %s: %+v", c.handle, resp.Error)
		}
		if d := time.Since(start); d < time.Second {
			t.Fatalf("release %s answered after %v, before the 1s quantum", c.handle, d)
		}
	}
}

func TestAnswersRecallAndRelease(t *testing.T) {
	h := newHarness(t, "test.answer1", "test.answer0")
	pr, _ := h.plan(t, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.answer1"}})
	res, _ := h.runPlan(t, pr.PlanID)
	follow, resp := h.plan(t, rpc.PlanParams{Handle: res.Handle, Plugins: []string{"test.answer0"}})
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}
	if !strings.HasPrefix(h.env.screens[1].Header, "follow-up on "+res.Handle) {
		t.Fatalf("follow-up header %q", h.env.screens[1].Header)
	}
	if _, resp := h.runPlan(t, follow.PlanID); resp.Error != nil {
		t.Fatal(resp.Error)
	}
	if h.src.calls != 2 { // one Stat and one Open for the single fetch
		t.Fatalf("source calls = %d", h.src.calls)
	}
	resp = h.call(peerCtx(1000), rpc.MethodAnswers, rpc.HandleParams{Handle: res.Handle})
	var all answers.Result
	if resp.Error != nil || json.Unmarshal(resp.Result, &all) != nil || len(all.Answers) != 2 {
		t.Fatalf("answers = %+v %s", resp.Error, resp.Result)
	}
	if resp := h.call(peerCtx(1000), rpc.MethodRelease, rpc.HandleParams{Handle: res.Handle}); resp.Error != nil {
		t.Fatal(resp.Error)
	}
	if resp := h.call(peerCtx(1000), rpc.MethodAnswers, rpc.HandleParams{Handle: res.Handle}); refusalCode(resp) != "handle_expired" {
		t.Fatalf("after release: %+v", resp.Error)
	}
}

func TestPluginsList(t *testing.T) {
	h := newHarness(t, "test.answer1", "acme/test.answer0")
	resp := h.call(peerCtx(1000), rpc.MethodPlugins, nil)
	var list []rpc.PluginInfo
	if resp.Error != nil || json.Unmarshal(resp.Result, &list) != nil || len(list) != 2 {
		t.Fatalf("plugins = %+v %s", resp.Error, resp.Result)
	}
	for _, p := range list {
		if p.Official == (p.Warning == answers.WarningUnofficial) {
			t.Errorf("%+v: warning does not match official", p)
		}
	}
}

func TestChecksRefuseWithoutSandbox(t *testing.T) {
	h := newHarness(t)
	h.b.o.Probe = func() (int, bool, error) { return 0, false, errors.New("no landlock") }
	f := h.b.Checks(context.Background(), nil)
	if len(f) != 1 || !f[0].Refuse {
		t.Fatalf("findings = %+v", f)
	}
	if st := h.b.Status(nil).(rpc.Status); st.Sandbox != "unavailable" {
		t.Fatalf("status = %+v", st)
	}
}

// /proc without hidepid=invisible lets the agent watch a running plugin:
// the console refuses to start, and a plan already in flight is refused
// with its own code (the mount may change after start).
func TestProcNotHiddenRefuses(t *testing.T) {
	h := newHarness(t, "test.answer1")
	h.b.o.ProcHidden = func() error { return procmount.ErrNotHidden }
	f := h.b.Checks(context.Background(), nil)
	if len(f) != 1 || !f[0].Refuse || !strings.Contains(f[0].Text, "hidepid=invisible") || !strings.Contains(f[0].Text, "/etc/fstab") {
		t.Fatalf("findings = %+v", f)
	}
	_, resp := h.plan(t, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.answer1"}})
	if refusalCode(resp) != "proc_not_hidden" || resp.Error.Data == nil || resp.Error.Data.Code != "E_PROC_NOT_HIDDEN" {
		t.Fatalf("plan: %+v", resp.Error)
	}
	if len(h.env.screens) != 0 {
		t.Fatalf("a refused plan reached the human")
	}
	h.b.o.ProcHidden = func() error { return nil }
	if f := h.b.Checks(context.Background(), nil); len(f) != 0 {
		t.Fatalf("hidden /proc: findings = %+v", f)
	}
}

// Production reads the real mount table: the default is not a stub.
func TestProcHiddenDefault(t *testing.T) {
	h := newHarness(t)
	b, err := New(Options{ConfigPath: h.b.o.ConfigPath, Profile: "samples", StateDir: t.TempDir(), PluginDir: h.b.o.PluginDir,
		WorkDir: filepath.Join(t.TempDir(), "work")})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if got, want := fmt.Sprint(b.o.ProcHidden()), fmt.Sprint(procmount.CheckSystem()); got != want {
		t.Fatalf("default ProcHidden = %v, CheckSystem = %v", got, want)
	}
}

// The workspace limits come from the approved policy only: an edit of the
// config file the human has not reviewed (handle_ttl 24h,
// max_workspace_bytes 1) changes nothing, and a handle keeps the TTL it was
// fetched with.
func TestWorkspaceFollowsApprovedPolicy(t *testing.T) {
	h := newHarness(t, "test.answer1")
	approvedPol := h.env.policy
	cfg := h.b.o.ConfigPath
	raw, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	edited := "max_workspace_bytes = \"1\"\n" + string(raw) + "handle_ttl = \"24h\"\n"
	if err := os.WriteFile(cfg, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var offset time.Duration
	now := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return time.Now().Add(offset)
	}
	o := h.b.o
	o.Now, o.WorkDir = now, filepath.Join(t.TempDir(), "work")
	h.b.Close()
	b, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	if _, err := b.Connect(context.Background(), console.Credentials{}); err != nil {
		t.Fatal(err)
	}
	h.b, h.env.b = b, b

	pr, _ := h.plan(t, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.answer1"}})
	res, resp := h.runPlan(t, pr.PlanID)
	if resp.Error != nil {
		t.Fatalf("fetch under the approved 1 GiB budget: %+v", resp.Error)
	}
	// A longer TTL approved after the fetch does not extend the handle.
	longer := *approvedPol
	longer.P.HandleTTL = 24 * time.Hour
	h.env.policy = &longer
	if _, resp := h.plan(t, rpc.PlanParams{Handle: res.Handle, Plugins: []string{"test.answer1"}}); resp.Error != nil {
		t.Fatalf("follow-up before expiry: %+v", resp.Error)
	}
	mu.Lock()
	offset = profile.DefaultHandleTTL + time.Minute
	mu.Unlock()
	if resp := h.call(peerCtx(1000), rpc.MethodAnswers, rpc.HandleParams{Handle: res.Handle}); refusalCode(resp) != "handle_expired" {
		t.Fatalf("handle alive past the approved TTL: %+v", resp.Error)
	}
}
