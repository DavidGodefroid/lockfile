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
