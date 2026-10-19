package answers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/DavidGodefroid/lockfile/internal/plugin"
	"github.com/DavidGodefroid/lockfile/internal/workspace"
	"github.com/DavidGodefroid/lockfile/pluginspec"
	"github.com/DavidGodefroid/lockfile/sandbox"
)

func TestMain(m *testing.M) {
	sandbox.Main()
	os.Exit(m.Run())
}

func manifestFor(name string, timeoutS int) []byte {
	return []byte(`name = "` + name + `"
version = "1.0.0"
protocol = 1
question = "Q?"
one = "yes"
zero = "no"
accepts = ["*/*"]
limits = { cpu_s = 5, memory = "256MiB", timeout_s = ` + string(rune('0'+timeoutS)) + ` }
`)
}

func hexsum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func installAs(t *testing.T, s plugin.Store, qualified, marketplace string, official bool, bin []byte, timeoutS int) {
	t.Helper()
	bare := qualified[strings.LastIndex(qualified, "/")+1:]
	man := manifestFor(bare, timeoutS)
	e := plugin.LockEntry{Name: qualified, Version: "1.0.0", Marketplace: marketplace, Official: official,
		BinarySHA256: hexsum(bin), ManifestSHA256: hexsum(man)}
	if err := s.Install(e, bin, man); err != nil {
		t.Fatal(err)
	}
}

type fixture struct {
	ws    *workspace.Manager
	store plugin.Store
	h     workspace.Handle
	calls []string
}

func newFixture(t *testing.T, plugins ...string) *fixture {
	t.Helper()
	ws, err := workspace.Open(workspace.Config{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.Close() })
	h, _, err := ws.Create(strings.NewReader("synthetic input"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{ws: ws, store: plugin.Store{Dir: t.TempDir()}, h: h}
	for _, p := range plugins {
		installAs(t, f.store, p, "official", true, []byte("bin:"+p), 5)
	}
	return f
}

// fakeRun answers by the binary's content: "bin:<name>" → outcome[name].
func (f *fixture) runner(outcomes map[string]pluginspec.Outcome, errs map[string]error) *Runner {
	return &Runner{WS: f.ws, Store: f.store, Run: func(_ context.Context, s sandbox.Spec) (pluginspec.Outcome, sandbox.Report, error) {
		b := make([]byte, 256)
		n, _ := s.Binary.Read(b)
		name := strings.TrimPrefix(string(b[:n]), "bin:")
		f.calls = append(f.calls, name)
		in := make([]byte, 64)
		k, _ := s.Input.Read(in)
		if string(in[:k]) != "synthetic input" {
			return pluginspec.Outcome{}, sandbox.Report{}, errors.New("input not at its start")
		}
		if err := errs[name]; err != nil {
			return pluginspec.Outcome{}, sandbox.Report{}, err
		}
		return outcomes[name], sandbox.Report{StderrBytes: 7}, nil
	}}
}

func yes() pluginspec.Outcome { v := true; return pluginspec.Outcome{Answer: &v} }
func no() pluginspec.Outcome  { v := false; return pluginspec.Outcome{Answer: &v} }

func result(a Answer) string {
	if a.Result == nil {
		return "null"
	}
	return string(rune('0' + *a.Result))
}

func TestOrderAndValues(t *testing.T) {
	f := newFixture(t, "file.non-empty", "format.is-pdf", "pdf.parses")
	r := f.runner(map[string]pluginspec.Outcome{"file.non-empty": yes(), "format.is-pdf": no(), "pdf.parses": yes()}, nil)
	got, audit, err := r.Answer(context.Background(), f.h, []string{"pdf.parses", "file.non-empty", "format.is-pdf"})
	if err != nil {
		t.Fatal(err)
	}
	var s []string
	for _, a := range got {
		s = append(s, a.Plugin+"="+result(a))
		if a.Version != "1.0.0" || !a.Official || a.Error != "" || a.Warning != "" {
			t.Errorf("answer %+v", a)
		}
	}
	if strings.Join(s, " ") != "pdf.parses=1 file.non-empty=1 format.is-pdf=0" {
		t.Fatalf("answers %v", s)
	}
	if len(audit) != 3 || audit[0].Plugin != "pdf.parses" || audit[0].BinarySHA256 != hexsum([]byte("bin:pdf.parses")) || audit[0].Report.StderrBytes != 7 {
		t.Fatalf("audit %+v", audit)
	}
}

func TestPluginFailuresCollapse(t *testing.T) {
	causes := []pluginspec.FailureCause{pluginspec.CauseExitCode, pluginspec.CauseSignal, pluginspec.CauseTimeout,
		pluginspec.CauseInvalidOutput, pluginspec.CauseOutputTooLong, pluginspec.CauseSandbox}
	var names []string
	outcomes := map[string]pluginspec.Outcome{}
	for i, c := range causes {
		n := "pdf.fail" + string(rune('a'+i))
		names = append(names, n)
		outcomes[n] = pluginspec.Failed(c)
	}
	f := newFixture(t, names...)
	got, audit, err := f.runner(outcomes, nil).Answer(context.Background(), f.h, names)
	if err != nil {
		t.Fatal(err)
	}
	for i, a := range got {
		if a.Result != nil || a.Error != "plugin_error" {
			t.Errorf("%s: %+v", a.Plugin, a)
		}
		if audit[i].Cause != causes[i] || audit[i].Result != nil {
			t.Errorf("audit %d: %+v", i, audit[i])
		}
	}
}

func TestConsoleErrorsKeepCodes(t *testing.T) {
	f := newFixture(t, "pdf.ok", "pdf.tampered", "pdf.nosandbox")
	inst, _ := f.store.Installed()
	for _, i := range inst {
		if i.Name == "pdf.tampered" {
			os.Chmod(filepath.Dir(i.Binary), 0o700)
			os.Chmod(i.Binary, 0o700)
			os.WriteFile(i.Binary, []byte("bin:evil"), 0o700)
		}
	}
	r := f.runner(map[string]pluginspec.Outcome{"pdf.ok": yes()},
		map[string]error{"pdf.nosandbox": errors.New("sandbox_unavailable: landlock abi 0 at /proc/x")})
	got, audit, err := r.Answer(context.Background(), f.h, []string{"pdf.ok", "pdf.missing", "pdf.tampered", "pdf.nosandbox"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"", "plugin_not_installed", "plugin_tampered", "sandbox_unavailable"}
	for i, a := range got {
		if a.Error != want[i] {
			t.Errorf("%s: error %q want %q", a.Plugin, a.Error, want[i])
		}
	}
	if !strings.Contains(audit[3].ConsoleError, "/proc/x") {
		t.Errorf("audit lost the console error: %+v", audit[3])
	}
	if strings.Join(f.calls, ",") != "pdf.ok,pdf.nosandbox" {
		t.Errorf("ran %v", f.calls)
	}
}

func TestMaxPerHandleAcrossCalls(t *testing.T) {
	var names []string
	for i := 0; i < 17; i++ {
		names = append(names, "pdf.p"+string(rune('a'+i)))
	}
	f := newFixture(t, names...)
	outcomes := map[string]pluginspec.Outcome{}
	for _, n := range names {
		outcomes[n] = yes()
	}
	r := f.runner(outcomes, nil)
	if _, _, err := r.Answer(context.Background(), f.h, names[:10]); err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	if _, _, err := r.Answer(context.Background(), f.h, names[10:17]); !errors.Is(err, ErrTooManyPlugins) {
		t.Fatalf("err %v", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("ran %v", f.calls)
	}
	if _, _, err := r.Answer(context.Background(), f.h, names[10:16]); err != nil {
		t.Fatalf("up to 16 in total: %v", err)
	}
}

func TestExpiredHandle(t *testing.T) {
	f := newFixture(t, "pdf.ok")
	f.ws.Release(f.h)
	if _, _, err := f.runner(nil, nil).Answer(context.Background(), f.h, []string{"pdf.ok"}); err == nil {
		t.Fatal("released handle answered")
	}
}

func TestNoTimingInAnswer(t *testing.T) {
	f := newFixture(t, "pdf.ok", "pdf.bad")
	got, _, err := f.runner(map[string]pluginspec.Outcome{"pdf.ok": yes(), "pdf.bad": pluginspec.Failed(pluginspec.CauseTimeout)}, nil).
		Answer(context.Background(), f.h, []string{"pdf.ok", "pdf.bad"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(got)
	var raw []map[string]any
	json.Unmarshal(b, &raw)
	for _, m := range raw {
		var keys []string
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		allowed := map[string]bool{"plugin": true, "version": true, "official": true, "result": true, "error": true, "warning": true}
		for _, k := range keys {
			if !allowed[k] {
				t.Errorf("key %q in %s", k, b)
			}
		}
		if _, ok := m["result"]; !ok {
			t.Errorf("result missing (must be null, not absent): %s", b)
		}
	}
	if typ := reflect.TypeOf(Answer{}); typ.NumField() != 6 {
		t.Fatalf("Answer has %d fields", typ.NumField())
	}
	if !strings.Contains(string(b), `"result":null,"error":"plugin_error"`) {
		t.Fatalf("json %s", b)
	}
}

func TestUnofficialWarning(t *testing.T) {
	f := newFixture(t, "pdf.ok")
	installAs(t, f.store, "acme/pdf.ok", "acme", false, []byte("bin:acme"), 5)
	got, _, err := f.runner(map[string]pluginspec.Outcome{"pdf.ok": yes(), "acme": no()}, nil).
		Answer(context.Background(), f.h, []string{"pdf.ok", "acme/pdf.ok"})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Warning != "" || got[1].Warning != "unofficial_marketplace" || got[1].Official {
		t.Fatalf("answers %+v", got)
	}
}

// buildPlugin builds one of the sandbox test plugins.
func buildPlugin(t *testing.T, name string) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("go", "build", "-trimpath", "-o", out, "../../sandbox/testdata/plugins/"+name)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", name, err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestEndToEndAnswer1(t *testing.T) {
	f := newFixture(t)
	installAs(t, f.store, "test.answer1", "official", true, buildPlugin(t, "answer1"), 5)
	installAs(t, f.store, "test.printthenhang", "official", true, buildPlugin(t, "printthenhang"), 1)
	r := &Runner{WS: f.ws, Store: f.store, Run: sandbox.Run}
	start := time.Now()
	got, audit, err := r.Answer(context.Background(), f.h, []string{"test.answer1", "test.printthenhang"})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Result == nil || *got[0].Result != 1 || got[0].Error != "" {
		t.Fatalf("answer1: %+v (audit %+v)", got[0], audit[0])
	}
	if got[1].Result != nil || got[1].Error != "plugin_error" || audit[1].Cause != pluginspec.CauseTimeout {
		t.Fatalf("printthenhang: %+v (audit %+v)", got[1], audit[1])
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
}
