package sandbox

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/elastic/go-seccomp-bpf/arch"

	"github.com/DavidGodefroid/lockfile/pluginspec"
)

func TestMain(m *testing.M) {
	Main()
	os.Exit(m.Run())
}

var (
	buildMu  sync.Mutex
	buildDir string
	built    = map[string]string{}
)

// plugin builds sandbox/testdata/plugins/<name> once and returns its path.
func plugin(t *testing.T, name string, ldflags ...string) string {
	t.Helper()
	buildMu.Lock()
	defer buildMu.Unlock()
	key := name + strings.Join(ldflags, " ")
	if p, ok := built[key]; ok {
		return p
	}
	if buildDir == "" {
		d, err := os.MkdirTemp("", "lockfile-sandbox-plugins-")
		if err != nil {
			t.Fatal(err)
		}
		buildDir = d
	}
	out := filepath.Join(buildDir, name+"-"+string(rune('a'+len(built))))
	args := []string{"build", "-trimpath", "-buildvcs=false", "-o", out}
	if len(ldflags) > 0 {
		args = append(args, "-ldflags", strings.Join(ldflags, " "))
	}
	args = append(args, "./testdata/plugins/"+name)
	cmd := exec.Command("go", args...)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", name, err, b)
	}
	built[key] = out
	return out
}

func input(t *testing.T, content string) (*os.File, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "input.bin")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f, p
}

var defaultLimits = pluginspec.Limits{CPUSeconds: 10, Memory: 512 << 20, Timeout: 10 * time.Second}

func run(t *testing.T, bin string, limits pluginspec.Limits) (pluginspec.Outcome, Report) {
	t.Helper()
	in, _ := input(t, "%PDF-1.7 synthetic")
	return runOn(t, bin, in, limits)
}

func runOn(t *testing.T, bin string, in *os.File, limits pluginspec.Limits) (pluginspec.Outcome, Report) {
	t.Helper()
	b, err := os.Open(bin)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	o, r, err := Run(context.Background(), Spec{Binary: b, Input: in, Limits: limits})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if (o.Answer == nil) == (o.Cause == pluginspec.CauseNone) {
		t.Fatalf("inconsistent outcome %+v", o)
	}
	return o, r
}

func wantAnswer(t *testing.T, o pluginspec.Outcome, r Report, want bool) {
	t.Helper()
	if o.Answer == nil || *o.Answer != want {
		t.Fatalf("want answer %v, got %+v (report %+v)", want, o, r)
	}
}

func wantFailure(t *testing.T, o pluginspec.Outcome, r Report, causes ...pluginspec.FailureCause) {
	t.Helper()
	if o.Answer != nil {
		t.Fatalf("want failure, got answer %v (report %+v)", *o.Answer, r)
	}
	for _, c := range causes {
		if o.Cause == c {
			return
		}
	}
	t.Fatalf("want cause in %v, got %q (report %+v)", causes, o.Cause, r)
}

func TestAnswer1(t *testing.T) {
	o, r := run(t, plugin(t, "answer1"), defaultLimits)
	wantAnswer(t, o, r, true)
}

func TestAnswer0(t *testing.T) {
	o, r := run(t, plugin(t, "answer0"), defaultLimits)
	wantAnswer(t, o, r, false)
}

func TestReadsOnlyFD3(t *testing.T) {
	in, path := input(t, "synthetic input")
	o, r := runOn(t, plugin(t, "readpasswd", "-X main.inputPath="+path), in, defaultLimits)
	wantAnswer(t, o, r, false)
}

func TestNoNetwork(t *testing.T) {
	o, r := run(t, plugin(t, "dial"), defaultLimits)
	wantAnswer(t, o, r, false)
}

func TestTimeout(t *testing.T) {
	l := defaultLimits
	l.Timeout = time.Second
	start := time.Now()
	o, r := run(t, plugin(t, "sleep"), l)
	wantFailure(t, o, r, pluginspec.CauseTimeout)
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("timeout took %v", d)
	}
	if r.PID <= 0 {
		t.Fatalf("no pid in report %+v", r)
	}
	if err := syscall.Kill(-r.PID, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("process group %d still exists: %v", r.PID, err)
	}
}

func TestMemoryLimit(t *testing.T) {
	l := defaultLimits
	l.Memory = 256 << 20
	o, r := run(t, plugin(t, "alloc"), l)
	wantFailure(t, o, r, pluginspec.CauseExitCode, pluginspec.CauseSignal)
}

// RLIMIT_DATA counts private writable memory only: shared anonymous,
// grows-down and read-only mappings must be refused or bounded too.
func TestMemoryLimitBypass(t *testing.T) {
	l := defaultLimits
	l.Memory = 256 << 20
	o, r := run(t, plugin(t, "mmapbypass"), l)
	wantAnswer(t, o, r, false)
}

// A plugin cannot exhaust the tasks of the console's account: thread
// creation stops at the cap and the runtime dies.
func TestThreadCap(t *testing.T) {
	o, r := run(t, plugin(t, "threads"), defaultLimits)
	wantFailure(t, o, r, pluginspec.CauseExitCode, pluginspec.CauseSignal)
}

func TestBigStdout(t *testing.T) {
	o, r := run(t, plugin(t, "bigout"), defaultLimits)
	wantFailure(t, o, r, pluginspec.CauseOutputTooLong)
}

func TestNoExec(t *testing.T) {
	o, r := run(t, plugin(t, "execsh"), defaultLimits)
	wantAnswer(t, o, r, false)
}

func TestPanic(t *testing.T) {
	o, r := run(t, plugin(t, "panic"), defaultLimits)
	wantFailure(t, o, r, pluginspec.CauseExitCode)
}

func TestPrintThenHang(t *testing.T) {
	l := defaultLimits
	l.Timeout = time.Second
	o, r := run(t, plugin(t, "printthenhang"), l)
	wantFailure(t, o, r, pluginspec.CauseTimeout)
}

func TestPrintThenPanic(t *testing.T) {
	o, r := run(t, plugin(t, "printthenpanic"), defaultLimits)
	wantFailure(t, o, r, pluginspec.CauseExitCode)
}

func TestStderrDiscarded(t *testing.T) {
	o, r := run(t, plugin(t, "stderr"), defaultLimits)
	wantAnswer(t, o, r, true)
	if r.StderrBytes != 10240 {
		t.Fatalf("StderrBytes = %d", r.StderrBytes)
	}
	if r.ExitCode != 0 || r.Signal != "" || o.Cause != pluginspec.CauseNone {
		t.Fatalf("unexpected outcome %+v report %+v", o, r)
	}
	// The plugin writes only 'S' to stderr: no returned value, whatever its
	// field, may hold that byte (or bytes) as text.
	for _, v := range []any{o, r} {
		for _, s := range textOf(reflect.ValueOf(v)) {
			if strings.Contains(s, "S") {
				t.Fatalf("stderr content leaked: %q in %+v", s, v)
			}
		}
	}
}

// textOf returns every string and byte slice reachable from v.
func textOf(v reflect.Value) []string {
	switch v.Kind() {
	case reflect.String:
		return []string{v.String()}
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return nil
		}
		return textOf(v.Elem())
	case reflect.Struct:
		var out []string
		for i := 0; i < v.NumField(); i++ {
			out = append(out, textOf(v.Field(i))...)
		}
		return out
	case reflect.Slice, reflect.Array:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			b := make([]byte, v.Len())
			reflect.Copy(reflect.ValueOf(b), v)
			return []string{string(b)}
		}
		var out []string
		for i := 0; i < v.Len(); i++ {
			out = append(out, textOf(v.Index(i))...)
		}
		return out
	case reflect.Map:
		var out []string
		for _, k := range v.MapKeys() {
			out = append(out, textOf(k)...)
			out = append(out, textOf(v.MapIndex(k))...)
		}
		return out
	}
	return nil
}

func TestTextOfSeesEveryField(t *testing.T) {
	type inner struct{ B []byte }
	v := struct {
		A string
		P *inner
		M map[string]string
	}{"a", &inner{[]byte("b")}, map[string]string{"c": "d"}}
	got := strings.Join(textOf(reflect.ValueOf(v)), ",")
	for _, want := range []string{"a", "b", "c", "d"} {
		if !strings.Contains(got, want) {
			t.Fatalf("textOf = %q, missing %q", got, want)
		}
	}
}

func TestEmptyEnv(t *testing.T) {
	o, r := run(t, plugin(t, "env"), defaultLimits)
	wantAnswer(t, o, r, true)
}

// Landlock does not cover file metadata: a plugin that learns its input's
// path must still not make it, or its directory, readable to others.
func TestNoMetadataChanges(t *testing.T) {
	in, path := input(t, "synthetic input")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	o, r := runOn(t, plugin(t, "chmod", "-X main.inputPath="+path), in, defaultLimits)
	wantAnswer(t, o, r, false)
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if after.Mode() != before.Mode() || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("metadata changed: %v %d %v -> %v %d %v", before.Mode(), before.Size(), before.ModTime(), after.Mode(), after.Size(), after.ModTime())
	}
}

// Inode flags and fs xattrs on the input (ioctl, file_setattr) and the
// memory layout of other processes (move_pages, migrate_pages,
// get_robust_list) are refused.
func TestNoInodeFlagsOrMemoryProbes(t *testing.T) {
	o, r := run(t, plugin(t, "inodeflags"), defaultLimits)
	wantAnswer(t, o, r, false)
}

// Every name the filter denies must exist on amd64: an unknown name (a
// typo, a syscall newer than the library's table) would otherwise not be
// denied anywhere.
func TestDenyListKnownOnAMD64(t *testing.T) {
	info, err := arch.GetInfo("amd64")
	if err != nil {
		t.Fatal(err)
	}
	names := append([]string{}, denied...)
	for _, d := range argDenied {
		names = append(names, d.name)
	}
	if _, err := conditional(info, 1); err != nil {
		t.Error(err)
	}
	for _, n := range names {
		if _, ok := info.SyscallNames[n]; !ok {
			t.Errorf("denied system call %q is unknown on amd64", n)
		}
	}
	for n, nr := range deniedNumbers {
		if got, ok := info.SyscallNumbers[int(nr)]; ok && got != n {
			t.Errorf("raw number %d of %s is %s in the table", nr, n, got)
		}
		if _, ok := info.SyscallNames[n]; ok {
			t.Errorf("%s is in the table now: deny it by name", n)
		}
	}
	if _, err := available(info, append([]string{"no_such_syscall"}, denied...)); err == nil {
		t.Errorf("available accepts an unknown name")
	}
}

func TestNoSignalsToOthers(t *testing.T) {
	o, r := run(t, plugin(t, "killother"), defaultLimits)
	wantAnswer(t, o, r, false)
}

// System V IPC objects and POSIX message queues outlive the plugin and are
// shared with every account in the IPC namespace: a covert channel to the
// agent account that Landlock does not cover.
func TestNoSysVIPC(t *testing.T) {
	o, r := run(t, plugin(t, "sysvipc"), defaultLimits)
	wantAnswer(t, o, r, false)
}

func TestNoFork(t *testing.T) {
	o, r := run(t, plugin(t, "fork"), defaultLimits)
	wantAnswer(t, o, r, false)
}

func TestForeignArchKilled(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("the i386 system call ABI exists on amd64 only")
	}
	o, r := run(t, plugin(t, "foreignarch"), defaultLimits)
	wantFailure(t, o, r, pluginspec.CauseSandbox)
}

func TestNoLeakedDescriptors(t *testing.T) {
	// A descriptor the console leaves inheritable by mistake (dup clears
	// close-on-exec) must still not reach the plugin.
	fd, err := syscall.Dup(2)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(fd)
	o, r := run(t, plugin(t, "fdleak"), defaultLimits)
	wantAnswer(t, o, r, false)
}

func TestProbe(t *testing.T) {
	abi, sec, err := Probe()
	if err != nil || abi < 1 || !sec {
		t.Fatalf("Probe = %d %v %v", abi, sec, err)
	}
}
