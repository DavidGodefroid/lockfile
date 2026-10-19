package sandbox

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

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
	if bytes.Contains([]byte(r.Signal), []byte("S")) {
		t.Fatalf("stderr content leaked into report %+v", r)
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
