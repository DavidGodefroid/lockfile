package answers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DavidGodefroid/lockfile/internal/source"
	"github.com/DavidGodefroid/lockfile/internal/workspace"
	"github.com/DavidGodefroid/lockfile/pluginspec"
)

type fakeSource struct {
	body    string
	statErr error
}

func (f fakeSource) Stat(_ context.Context, key string) (source.ObjectInfo, error) {
	if _, err := source.CheckKey("invoices/", key); err != nil {
		return source.ObjectInfo{}, err
	}
	return source.ObjectInfo{Size: int64(len(f.body))}, f.statErr
}

func (f fakeSource) Open(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(f.body)), nil
}

func deps(f *fixture, r *Runner, src source.Source) Deps {
	return Deps{
		Sources:        map[string]source.Source{"prod-docs": src},
		MaxObjectBytes: func(string) int64 { return 1 << 20 },
		WS:             f.ws,
		Runner:         r,
		Quantum:        10 * time.Millisecond,
	}
}

var allowedKeys = map[string]bool{"handle": true, "answers": true, "plugin": true, "version": true,
	"official": true, "result": true, "error": true, "warning": true, "refusal": true}

// walk checks every key and value of an agent-visible JSON document.
func walk(t *testing.T, v any) {
	t.Helper()
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if !allowedKeys[k] {
				t.Errorf("key %q is not agent-visible", k)
			}
			switch k {
			case "result":
				if n, ok := val.(float64); val != nil && (!ok || (n != 0 && n != 1)) {
					t.Errorf("result %v", val)
				}
			case "error", "refusal":
				if s, _ := val.(string); !Code(s).Valid() {
					t.Errorf("%s %q is not a code", k, val)
				}
			case "warning":
				if val != WarningUnofficial {
					t.Errorf("warning %v", val)
				}
			case "handle":
				if s, _ := val.(string); !strings.HasPrefix(s, "f_") || len(s) != 12 {
					t.Errorf("handle %v", val)
				}
			}
			walk(t, val)
		}
	case []any:
		for _, e := range x {
			walk(t, e)
		}
	}
}

func marshal(t *testing.T, res Result, ref *Refusal) []byte {
	t.Helper()
	var v any = res
	if ref != nil {
		v = ref
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestResultIsClosed(t *testing.T) {
	f := newFixture(t, "pdf.ok", "pdf.bad")
	installAs(t, f.store, "acme/pdf.x", "acme", false, []byte("bin:acme"), 5)
	r := f.runner(map[string]pluginspec.Outcome{"pdf.ok": yes(), "pdf.bad": pluginspec.Failed(pluginspec.CauseSignal), "acme": no()}, nil)
	res, ref, _ := ExecutePlan(context.Background(),
		Plan{Profile: "prod-docs", Key: "invoices/a.pdf", Plugins: []string{"pdf.ok", "pdf.bad", "acme/pdf.x", "pdf.missing"}},
		deps(f, r, fakeSource{body: "synthetic input"}))
	if ref != nil {
		t.Fatalf("refused %v", ref.Code)
	}
	if got := string(marshal(t, res, nil)); !strings.Contains(got, `"result":1}`) || !strings.Contains(got, `"result":null,"error":"plugin_error"}`) ||
		!strings.Contains(got, `"result":0,"warning":"unofficial_marketplace"}`) || !strings.Contains(got, `"error":"plugin_not_installed"`) {
		t.Fatalf("json %s", got)
	}
	for _, b := range [][]byte{marshal(t, res, nil), marshal(t, Result{}, &Refusal{Code: CodeFetchFailed})} {
		var v any
		if err := json.Unmarshal(b, &v); err != nil {
			t.Fatal(err)
		}
		walk(t, v)
	}
	if len(res.Answers) != 4 {
		t.Fatalf("answers %+v", res.Answers)
	}
}

func TestCodesClosed(t *testing.T) {
	want := []string{"plugin_error", "fetch_failed", "key_outside_prefix", "unknown_profile", "too_many_plugins",
		"plugin_not_installed", "plugin_tampered", "plugin_yanked", "sandbox_unavailable", "proc_not_hidden", "approval_denied",
		"approval_timeout", "handle_expired", "stale"}
	if len(Codes) != len(want) {
		t.Fatalf("Codes = %v", Codes)
	}
	for _, c := range want {
		if !Code(c).Valid() {
			t.Errorf("%s not valid", c)
		}
	}
	for _, c := range []string{"", "ok", "permission denied", "fetch_failed "} {
		if Code(c).Valid() {
			t.Errorf("%q valid", c)
		}
	}
}

func TestErrorsNeverForwarded(t *testing.T) {
	f := newFixture(t, "pdf.ok")
	r := f.runner(map[string]pluginspec.Outcome{"pdf.ok": yes()}, nil)
	leak := errors.New("/srv/x/john.pdf: permission denied, 4821 bytes")
	res, ref, audit := ExecutePlan(context.Background(),
		Plan{Profile: "prod-docs", Key: "invoices/john.pdf", Plugins: []string{"pdf.ok"}},
		deps(f, r, fakeSource{statErr: leak}))
	if ref == nil || ref.Code != CodeFetchFailed {
		t.Fatalf("refusal %+v result %+v", ref, res)
	}
	b := marshal(t, res, ref)
	for _, s := range []string{"/srv", "john", "4821", "permission"} {
		if bytes.Contains(b, []byte(s)) {
			t.Errorf("%q leaked in %s", s, b)
		}
	}
	if string(b) != `{"refusal":"fetch_failed"}` {
		t.Fatalf("json %s", b)
	}
	kept := false
	for _, e := range audit {
		if e.Fetch != nil && strings.Contains(e.Fetch.Cause, "/srv/x/john.pdf: permission denied, 4821 bytes") {
			kept = true
		}
	}
	if !kept {
		t.Fatalf("audit lost the cause: %+v", audit)
	}
	if len(f.calls) != 0 {
		t.Fatalf("plugins ran after a failed fetch: %v", f.calls)
	}
}

func TestRefusalCodes(t *testing.T) {
	f := newFixture(t, "pdf.ok")
	r := f.runner(map[string]pluginspec.Outcome{"pdf.ok": yes()}, nil)
	d := deps(f, r, fakeSource{body: "x"})
	ctx := context.Background()
	cases := []struct {
		p    Plan
		want Code
	}{
		{Plan{Profile: "nope", Key: "invoices/a.pdf", Plugins: []string{"pdf.ok"}}, CodeUnknownProfile},
		{Plan{Profile: "prod-docs", Key: "../etc/passwd", Plugins: []string{"pdf.ok"}}, CodeKeyOutsidePrefix},
		{Plan{Profile: "prod-docs", Key: "invoices/a.pdf", Plugins: make([]string, 17)}, CodeTooManyPlugins},
		{Plan{Handle: "f_0000000000", Plugins: []string{"pdf.ok"}}, CodeHandleExpired},
	}
	for _, c := range cases {
		_, ref, _ := ExecutePlan(ctx, c.p, d)
		if ref == nil || ref.Code != c.want {
			t.Errorf("%+v: refusal %+v, want %s", c.p, ref, c.want)
		}
	}
	big := deps(f, r, fakeSource{body: strings.Repeat("x", 100)})
	big.MaxObjectBytes = func(string) int64 { return 10 }
	if _, ref, _ := ExecutePlan(ctx, Plan{Profile: "prod-docs", Key: "invoices/a.pdf", Plugins: []string{"pdf.ok"}}, big); ref == nil || ref.Code != CodeFetchFailed {
		t.Errorf("too large: %+v", ref)
	}
}

// A plan approved on a content hash runs only on those bytes: other bytes
// are released before any plugin runs and the plan is stale; the same
// bytes are answered, and the handle's spool holds exactly them.
func TestExecutePlanContentHash(t *testing.T) {
	f := newFixture(t, "pdf.ok")
	r := f.runner(map[string]pluginspec.Outcome{"pdf.ok": yes()}, nil)
	body := "synthetic input"
	sum := sha256.Sum256([]byte(body))
	approved := hex.EncodeToString(sum[:])
	ctx := context.Background()

	before := spools(t, f)
	changed := deps(f, r, fakeSource{body: "changed input"})
	_, ref, audit := ExecutePlan(ctx, Plan{Profile: "prod-docs", Key: "invoices/a.pdf", Plugins: []string{"pdf.ok"}, SHA256: approved}, changed)
	if ref == nil || ref.Code != CodeStale || len(f.calls) != 0 {
		t.Fatalf("changed content: refusal %+v, plugin runs %q", ref, f.calls)
	}
	if len(audit) != 1 || audit[0].Fetch == nil || !strings.Contains(audit[0].Fetch.Cause, "content changed") || audit[0].Fetch.SHA256 == approved {
		t.Fatalf("audit %+v", audit)
	}
	if n := spools(t, f); n != before {
		t.Fatalf("%d spools after stale, want %d", n, before)
	}

	res, ref, _ := ExecutePlan(ctx, Plan{Profile: "prod-docs", Key: "invoices/a.pdf", Plugins: []string{"pdf.ok"}, SHA256: approved}, deps(f, r, fakeSource{body: body}))
	if ref != nil || len(f.calls) != 1 || *res.Answers[0].Result != 1 {
		t.Fatalf("same content: %+v %+v, plugin runs %q", res, ref, f.calls)
	}
	file, info, err := f.ws.OpenFile(workspace.Handle(res.Handle))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	got, _ := io.ReadAll(file)
	if string(got) != body || info.SHA256 != approved {
		t.Fatalf("spool %q sha256 %s, want the approved bytes", got, info.SHA256)
	}
}

// spools counts the files fetched into the workspace.
func spools(t *testing.T, f *fixture) int {
	t.Helper()
	des, err := os.ReadDir(f.ws.Dir())
	if err != nil {
		t.Fatal(err)
	}
	return len(des)
}

func TestFollowUpOnHandle(t *testing.T) {
	f := newFixture(t, "pdf.ok")
	r := f.runner(map[string]pluginspec.Outcome{"pdf.ok": yes()}, nil)
	d := deps(f, r, fakeSource{body: "synthetic input"})
	res, ref, _ := ExecutePlan(context.Background(), Plan{Profile: "prod-docs", Key: "invoices/a.pdf", Plugins: []string{"pdf.ok"}}, d)
	if ref != nil {
		t.Fatal(ref.Code)
	}
	res2, ref, _ := ExecutePlan(context.Background(), Plan{Handle: workspace.Handle(res.Handle), Plugins: []string{"pdf.ok"}}, d)
	if ref != nil || res2.Handle != res.Handle || *res2.Answers[0].Result != 1 {
		t.Fatalf("follow-up %+v %+v", res2, ref)
	}
}

func TestNoSizeNoType(t *testing.T) {
	f := newFixture(t, "pdf.ok")
	r := f.runner(map[string]pluginspec.Outcome{"pdf.ok": yes()}, nil)
	res, ref, audit := ExecutePlan(context.Background(), Plan{Profile: "prod-docs", Key: "invoices/a.pdf", Plugins: []string{"pdf.ok"}},
		deps(f, r, fakeSource{body: "synthetic input"}))
	if ref != nil {
		t.Fatal(ref.Code)
	}
	b := marshal(t, res, nil)
	for _, k := range []string{"size", "media_type", "sha256", "type", "bytes", "duration", "path"} {
		if bytes.Contains(b, []byte(`"`+k+`"`)) {
			t.Errorf("key %q in %s", k, b)
		}
	}
	if audit[0].Fetch == nil || audit[0].Fetch.Size != 15 || len(audit[0].Fetch.SHA256) != 64 {
		t.Fatalf("audit fetch %+v", audit[0].Fetch)
	}
}

func TestNextQuantum(t *testing.T) {
	start := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	q := 5 * time.Second
	for _, c := range []struct{ elapsed, want time.Duration }{
		{0, 5 * time.Second},
		{1200 * time.Millisecond, 5 * time.Second},
		{5 * time.Second, 5 * time.Second},
		{6 * time.Second, 10 * time.Second},
		{10*time.Second + time.Nanosecond, 15 * time.Second},
	} {
		if got := nextQuantum(start, start.Add(c.elapsed), q); !got.Equal(start.Add(c.want)) {
			t.Errorf("elapsed %v: deadline %v, want %v", c.elapsed, got.Sub(start), c.want)
		}
	}
}

func TestQuantum(t *testing.T) {
	q := 100 * time.Millisecond
	for _, c := range []struct{ work, want time.Duration }{{12 * time.Millisecond, q}, {120 * time.Millisecond, 2 * q}} {
		start := time.Now()
		time.Sleep(c.work)
		if err := Quantize(context.Background(), start, q, time.Now); err != nil {
			t.Fatal(err)
		}
		got := time.Since(start)
		if got < c.want || got > c.want+80*time.Millisecond {
			t.Errorf("work %v returned after %v, want %v", c.work, got, c.want)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	start := time.Now()
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	if err := Quantize(ctx, start, 10*time.Second, time.Now); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("cancel did not return early")
	}
}

func TestExecutePlanIsQuantized(t *testing.T) {
	f := newFixture(t, "pdf.ok")
	r := f.runner(map[string]pluginspec.Outcome{"pdf.ok": yes()}, nil)
	d := deps(f, r, fakeSource{body: "x"})
	d.Quantum = 150 * time.Millisecond
	for _, p := range []Plan{
		{Profile: "prod-docs", Key: "invoices/a.pdf", Plugins: []string{"pdf.ok"}},
		{Profile: "prod-docs", Key: "invoices/a.pdf", Plugins: []string{"pdf.ok"}},
	} {
		start := time.Now()
		ExecutePlan(context.Background(), p, d)
		if el := time.Since(start); el < d.Quantum {
			t.Errorf("returned after %v, before the quantum", el)
		}
	}
	d.Sources["prod-docs"] = fakeSource{statErr: errors.New("missing")}
	start := time.Now()
	ExecutePlan(context.Background(), Plan{Profile: "prod-docs", Key: "invoices/a.pdf", Plugins: []string{"pdf.ok"}}, d)
	if el := time.Since(start); el < d.Quantum {
		t.Errorf("fetch failure returned after %v, before the quantum", el)
	}
}
