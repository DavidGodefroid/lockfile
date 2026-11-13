package backend

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DavidGodefroid/lockkit/ipc"

	"github.com/DavidGodefroid/lockfile/internal/answers"
	"github.com/DavidGodefroid/lockfile/internal/plugin"
	"github.com/DavidGodefroid/lockfile/internal/rpc"
	"github.com/DavidGodefroid/lockfile/internal/testenv"
)

// twoInputs is a protocol-2 manifest with two inputs.
func twoInputs(bare string) []byte {
	return fmt.Appendf(nil, `name = %q
version = "1.0.0"
protocol = 2
question = "Does the file contain ${needle} near ${other}?"
one = "yes"
zero = "no"
accepts = ["*/*"]
inputs = [{ name = "needle", kind = "text", max_bytes = 64 }, { name = "other", kind = "text", max_bytes = 64 }]
limits = { cpu_s = 5, memory = "256MiB", timeout_s = 5 }
`, bare)
}

// inputsHarness has test.contains (input needle, text), test.mail (input
// needle, email), test.two (inputs needle and other) beside test.answer1
// and test.answer0.
func inputsHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessWith(t, func(s plugin.Store) {
		bin := testenv.BuildPlugin(t, "contains")
		testenv.InstallManifest(t, s, "test.contains", bin, func(b string) []byte { return testenv.InputsManifest(b, "text") })
		testenv.InstallManifest(t, s, "test.mail", bin, func(b string) []byte { return testenv.InputsManifest(b, "email") })
		testenv.InstallManifest(t, s, "test.two", bin, twoInputs)
	}, "test.answer1", "test.answer0")
}

func (h *harness) seen(t *testing.T) string {
	t.Helper()
	h.env.mu.Lock()
	defer h.env.mu.Unlock()
	b, err := json.Marshal(struct {
		R any
		L []string
	}{h.env.records, h.env.lines})
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	for _, s := range h.env.screens {
		out += "\n" + strings.Join(append(append([]string{s.Header, s.Note}, s.Body...), s.Warnings...), "\n")
	}
	return out
}

func TestPlanInputsAccepted(t *testing.T) {
	h := inputsHarness(t)
	r, resp := h.plan(t, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.contains"},
		Inputs: map[string]string{"test.contains.needle": "${word}"}})
	if resp.Error != nil || r.Answers != 1 || len(r.HeldInputs) != 0 {
		t.Fatalf("plan: %+v %+v", resp.Error, r)
	}
	s := h.env.screens[0]
	for _, want := range []string{"  ? Does the file contain ${word}?   1 = yes   0 = no", "values to type: ${word}", "this plan reveals at most 1 answers about the file"} {
		if !slices.Contains(s.Body, want) {
			t.Errorf("screen lacks %q:\n%s", want, strings.Join(s.Body, "\n"))
		}
	}
	if !s.NeverAuto {
		t.Error("a plan with typed values may be auto-approved")
	}
}

func TestPlanInputsRefused(t *testing.T) {
	h := inputsHarness(t)
	const literal = "alice@example.org"
	for name, c := range map[string]struct {
		plugins []string
		inputs  map[string]string
		code    string
	}{
		"unbound":           {[]string{"test.contains"}, nil, rpc.MsgPlanInputs},
		"one of two":        {[]string{"test.two"}, map[string]string{"test.two.needle": "${a}"}, rpc.MsgPlanInputs},
		"unknown key":       {[]string{"test.contains"}, map[string]string{"test.contains.needle": "${a}", "test.contains.other": "${b}"}, rpc.MsgPlanInputs},
		"plugin no inputs":  {[]string{"test.answer1"}, map[string]string{"test.answer1.needle": "${a}"}, rpc.MsgPlanInputs},
		"literal":           {[]string{"test.contains"}, map[string]string{"test.contains.needle": literal}, string(answers.CodeLiteralInput)},
		"literal elsewhere": {[]string{"test.contains"}, map[string]string{"test.contains.needle": "${a}", "x.y.z": literal}, string(answers.CodeLiteralInput)},
		"bad placeholder":   {[]string{"test.contains"}, map[string]string{"test.contains.needle": "${Email}"}, string(answers.CodeLiteralInput)},
		"placeholder text":  {[]string{"test.contains"}, map[string]string{"test.contains.needle": "${a} " + literal}, string(answers.CodeLiteralInput)},
	} {
		t.Run(name, func(t *testing.T) {
			_, resp := h.plan(t, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: c.plugins, Inputs: c.inputs})
			if resp.Error == nil || resp.Error.Message != c.code {
				t.Fatalf("got %+v, want %s", resp.Error, c.code)
			}
			if c.code == string(answers.CodeLiteralInput) {
				raw, _ := json.Marshal(resp)
				if strings.Contains(string(raw), literal) || !strings.Contains(string(raw), "E_LITERAL_INPUT") ||
					!strings.Contains(string(raw), "bind the input to a placeholder like ${email}; the human types the value") {
					t.Fatalf("refusal %s", raw)
				}
			}
		})
	}
	if len(h.env.screens) != 0 {
		t.Fatalf("a refused plan was shown: %d screens", len(h.env.screens))
	}
	if strings.Contains(h.seen(t), literal) {
		t.Fatalf("the literal reached the console or audit:\n%s", h.seen(t))
	}
}

// Each (plugin, distinct placeholder) pair counts as one answer, in the
// plan's answer and screen and in max_plugins_per_handle (4 here).
func TestPlanCostPerPlaceholder(t *testing.T) {
	if n := rpc.PlanCost([]string{"a.b", "c.d"}, map[string]string{"a.b.x": "e", "a.b.y": "f", "c.d.z": "e"}); n != 3 {
		t.Fatalf("cost %d", n)
	}
	if n := rpc.PlanCost([]string{"a.b", "c.d"}, nil); n != 2 {
		t.Fatalf("cost without inputs %d", n)
	}
	h := inputsHarness(t)
	r, resp := h.plan(t, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.two", "test.answer1"},
		Inputs: map[string]string{"test.two.needle": "${a}", "test.two.other": "${b}"}})
	if resp.Error != nil || r.Answers != 3 || !slices.Contains(h.env.screens[0].Body, "this plan reveals at most 3 answers about the file") ||
		h.env.screens[0].Header != "file plan · 3 answers" {
		t.Fatalf("plan %+v %+v %q", resp.Error, r, h.env.screens[0].Body)
	}
	// One value for both inputs: one answer.
	if r, _ := h.plan(t, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.two"},
		Inputs: map[string]string{"test.two.needle": "${a}", "test.two.other": "${a}"}}); r.Answers != 1 {
		t.Fatalf("same placeholder twice: %+v", r)
	}
	// 2 + 1 + 1 + 1 > 4.
	_, resp = h.plan(t, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.two", "test.answer1", "test.answer0", "test.contains"},
		Inputs: map[string]string{"test.two.needle": "${a}", "test.two.other": "${b}", "test.contains.needle": "${a}"}})
	if refusalCode(resp) != string(answers.CodeTooManyPlugins) {
		t.Fatalf("over budget: %+v", resp.Error)
	}
}

// Off the hub nobody types a value: a plan whose placeholder the console
// does not hold refuses its run, naming the placeholder only.
func TestRunWithoutValueRefused(t *testing.T) {
	h := inputsHarness(t)
	r, resp := h.plan(t, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.contains"}, Inputs: map[string]string{"test.contains.needle": "${word}"}})
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}
	_, resp = h.runPlan(t, r.PlanID)
	if refusalCode(resp) != string(answers.CodeInputInvalid) {
		t.Fatalf("run: %+v", resp.Error)
	}
	if !strings.Contains(h.seen(t), "${word}: no value is held for it") {
		t.Fatalf("console:\n%s", h.seen(t))
	}
	if h.src.calls != 0 {
		t.Fatal("the file was fetched for a run that cannot run")
	}
}

// A held value is reused without a prompt (the screen says so, the plan
// answer lists it) and reaches the plugin; its detail is printed escaped
// with clear results on only.
func TestRunHeldValueAndDetail(t *testing.T) {
	h := inputsHarness(t)
	h.b.vt.keep("word", []byte("synthetic"), time.Date(2026, 11, 13, 14, 2, 0, 0, time.Local))
	plan := func() rpc.PlanResult {
		r, resp := h.plan(t, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.contains"}, Inputs: map[string]string{"test.contains.needle": "${word}"}})
		if resp.Error != nil || !slices.Equal(r.HeldInputs, []string{"word"}) {
			t.Fatalf("plan %+v %+v", resp.Error, r)
		}
		return r
	}
	r := plan()
	if s := h.env.screens[0]; !slices.Contains(s.Body, "${word}: typed at 14:02, reused · replayed on 0 files this session") ||
		slices.ContainsFunc(s.Body, func(l string) bool { return strings.HasPrefix(l, "values to type") }) {
		t.Fatalf("screen %q", s.Body)
	}
	res, resp := h.runPlan(t, r.PlanID)
	if resp.Error != nil || len(res.Answers) != 1 || res.Answers[0].Result == nil || *res.Answers[0].Result != 1 {
		t.Fatalf("run %+v %+v", resp.Error, res)
	}
	if strings.Contains(h.seen(t), "found at byte") {
		t.Fatal("the detail was printed with clear results off")
	}
	h.env.showResults = true
	r = plan()
	if s := h.env.screens[1]; !slices.Contains(s.Body, "${word}: typed at 14:02, reused · replayed on 1 files this session") {
		t.Fatalf("screen %q", s.Body)
	}
	if _, resp := h.runPlan(t, r.PlanID); resp.Error != nil {
		t.Fatal(resp.Error)
	}
	seen := h.seen(t)
	if !strings.Contains(seen, "test.contains detail, shown here only; the agent got 1/0:") ||
		!strings.Contains(seen, `found at byte 9\\x1b[2J`) || strings.Contains(seen, "\x1b") {
		t.Fatalf("detail not printed escaped:\n%s", seen)
	}
	// The value appears in nothing the console printed, audited or showed.
	if strings.Contains(seen, "synthetic") {
		t.Fatalf("the value leaked:\n%s", seen)
	}
	raw, _ := json.Marshal(res)
	if strings.Contains(string(raw), "synthetic") || strings.Contains(string(raw), "found") {
		t.Fatalf("the agent's result carries the value or the detail: %s", raw)
	}
}

// A value that does not fit an input's kind refuses the run, never echoed.
func TestRunInvalidValueRefused(t *testing.T) {
	h := inputsHarness(t)
	h.b.vt.keep("addr", []byte("not-an-address"), time.Now())
	r, resp := h.plan(t, rpc.PlanParams{Key: "invoices/a.pdf", Plugins: []string{"test.mail"}, Inputs: map[string]string{"test.mail.needle": "${addr}"}})
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}
	_, resp = h.runPlan(t, r.PlanID)
	if refusalCode(resp) != string(answers.CodeInputInvalid) {
		t.Fatalf("run %+v", resp.Error)
	}
	raw, _ := json.Marshal(resp)
	seen := h.seen(t)
	if !strings.Contains(seen, "${addr}: not a valid email; plan again") || strings.Contains(seen, "not-an-address") ||
		strings.Contains(string(raw), "not-an-address") || resp.Error.Code != ipc.CodeRefused {
		t.Fatalf("console:\n%s\nresp %s", seen, raw)
	}
}

func TestVaultWipe(t *testing.T) {
	var v vault
	v.keep("a", []byte("value"), time.Now())
	h := v.m["a"].v
	v.keep("a", []byte("other"), time.Now())
	if string(v.get("a")) != "value" {
		t.Fatal("a held value was replaced")
	}
	v.wipe()
	if len(v.names()) != 0 || string(h) != "\x00\x00\x00\x00\x00" {
		t.Fatalf("not wiped: %v %q", v.names(), h)
	}
}

func TestPluginInputsJSON(t *testing.T) {
	got := pluginInputs(map[string]string{"a.b.x": "e", "a.b.y": "f", "c.d.z": "e"},
		map[string][]byte{"e": []byte(`q"uo\te`), "f": []byte("é")})
	if string(got["a.b"]) != `{"x":"q\"uo\\te","y":"é"}` || string(got["c.d"]) != `{"z":"q\"uo\\te"}` {
		t.Fatalf("%s %s", got["a.b"], got["c.d"])
	}
	for _, b := range got {
		var m map[string]string
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
	}
}
