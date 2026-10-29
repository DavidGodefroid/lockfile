package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/sandbox"
)

func TestMain(m *testing.M) {
	sandbox.Main()
	os.Exit(m.Run())
}

// runArgs runs the command line args with stdin in and returns the exit
// code, stdout and stderr.
func runArgs(t *testing.T, in string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(env{stdin: strings.NewReader(in), stdout: &out, stderr: &errb}, args)
	return code, out.String(), errb.String()
}

// The host install and its update are refused: lockfile runs in Docker,
// and the launcher on the host updates itself.
func TestHostModeCommandsRefused(t *testing.T) {
	for _, args := range [][]string{{"install"}, {"install", "--yes"}, {"update"}, {"update", "--check"}} {
		code, out, errText := runArgs(t, "", args...)
		if code != product.ExitError {
			t.Errorf("%v: exit %d, want %d", args, code, product.ExitError)
		}
		if !strings.Contains(errText, "runs in Docker") || !strings.Contains(errText, "scripts/install.sh") ||
			!strings.Contains(errText, "`lockfile update` on the host") || !strings.Contains(errText, "docs/docker.md") || out != "" {
			t.Errorf("%v: stdout %q stderr %q", args, out, errText)
		}
	}
}

// The help names no host install; update is the launcher's.
func TestHelpHasNoHostInstall(t *testing.T) {
	_, out, _ := runArgs(t, "", "help")
	for _, gone := range []string{"one-time setup", "(sudo, Linux)", "install the latest signed release"} {
		if strings.Contains(out, gone) {
			t.Errorf("help still names %q:\n%s", gone, out)
		}
	}
	if !strings.Contains(out, "\n  update ") || !strings.Contains(out, "launcher") {
		t.Errorf("help lacks the launcher's update:\n%s", out)
	}
}

func TestUsageExit(t *testing.T) {
	code, out, errText := runArgs(t, "", "frobnicate")
	if code != product.ExitUsage {
		t.Fatalf("exit = %d", code)
	}
	if out != "" || !strings.Contains(errText, `unknown command "frobnicate"`) || !strings.Contains(errText, "Usage:") {
		t.Fatalf("stdout %q stderr %q", out, errText)
	}
	if code, _, _ := runArgs(t, ""); code != product.ExitUsage {
		t.Fatalf("no args: exit = %d", code)
	}
}

func TestVersion(t *testing.T) {
	code, out, _ := runArgs(t, "", "version")
	if code != 0 || !strings.HasPrefix(out, "lockfile ") {
		t.Fatalf("exit %d out %q", code, out)
	}
}
