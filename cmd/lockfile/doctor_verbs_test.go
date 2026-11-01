package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DavidGodefroid/lockkit/agentwire"
	"github.com/DavidGodefroid/lockkit/verbs"

	"github.com/DavidGodefroid/lockfile/internal/product"
)

// agentsHome is an account where Gemini CLI and Cursor are installed
// (their directories) and not wired to lockfile.
func agentsHome(t *testing.T, h *doctorHooks) string {
	t.Helper()
	home := t.TempDir()
	for _, d := range []string{".gemini", ".cursor"} {
		if err := os.Mkdir(filepath.Join(home, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	h.agents = func() (agentwire.Env, error) {
		return agentwire.Env{Home: home, Getenv: func(string) string { return "" },
			LookPath: func(string) (string, error) { return "", errors.New("not on PATH") },
			Run:      func(string, ...string) ([]byte, error) { return nil, errors.New("no command in this test") }}, nil
	}
	return home
}

// --json prints one verbs.Doctor: ok, the repairs, every check.
func TestDoctorJSON(t *testing.T) {
	h := doctorEnv(t, "/nonexistent")
	agentsHome(t, h)
	code, out, errText := runArgs(t, "", "doctor", "--json")
	var d verbs.Doctor
	jsonLine(t, out, &d)
	if code != product.ExitOK || !d.OK || d.Lock != "lockfile" || d.Fixed == nil || len(d.Fixed) != 0 || errText != "" {
		t.Fatalf("exit %d: %+v %s", code, d, errText)
	}
	var wiring []verbs.Check
	for _, c := range d.Checks {
		if c.Check == "agents" {
			wiring = append(wiring, c)
		}
	}
	if len(wiring) != 2 || wiring[0].Status != verbs.StatusWarn || !strings.Contains(wiring[0].Detail, "not wired: ") || !strings.Contains(wiring[0].Fix, "doctor --fix") {
		t.Fatalf("agents: %+v", wiring)
	}
	// A failed check: not OK, exit 1, still one Doctor line.
	h.probe = func() (int, bool, error) { return 0, false, errors.New("no landlock") }
	code, out, _ = runArgs(t, "", "doctor", "--json")
	jsonLine(t, out, &d)
	if code != product.ExitError || d.OK {
		t.Fatalf("exit %d: %+v", code, d)
	}
}

// --fix --yes wires every agent not wired, without asking, and reports
// the repairs; a second run finds nothing to do.
func TestDoctorFixYes(t *testing.T) {
	h := doctorEnv(t, "/nonexistent")
	home := agentsHome(t, h)
	code, out, errText := runArgs(t, "", "doctor", "--fix", "--yes", "--json")
	var d verbs.Doctor
	jsonLine(t, out, &d)
	if code != product.ExitOK || !d.OK || len(d.Fixed) != 2 || strings.Contains(errText, "[y/N]") {
		t.Fatalf("exit %d: %+v %s", code, d, errText)
	}
	if _, err := os.Stat(filepath.Join(home, ".gemini", "settings.json")); err != nil {
		t.Fatalf("gemini not wired: %v", err)
	}
	_, out, _ = runArgs(t, "", "doctor", "--fix", "--yes", "--json")
	jsonLine(t, out, &d)
	if len(d.Fixed) != 0 {
		t.Fatalf("second run: %+v", d)
	}
}

// --fix on a terminal asks before each repair (on stderr) and makes only
// the ones accepted.
func TestDoctorFixAsks(t *testing.T) {
	h := doctorEnv(t, "/nonexistent")
	home := agentsHome(t, h)
	humanEnv(t, "official") // acts as a terminal
	code, out, errText := runArgs(t, "n\ny\n", "doctor", "--fix")
	if code != product.ExitOK || strings.Count(errText, "[y/N]") != 2 || strings.Contains(out, "[y/N]") {
		t.Fatalf("exit %d: %s %s", code, out, errText)
	}
	// Detect lists cursor before gemini: cursor declined, gemini wired.
	if !strings.Contains(out, "fixed wired gemini") || !strings.Contains(out, "not wired: cursor") {
		t.Fatalf("output:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(home, ".gemini", "settings.json")); err != nil {
		t.Fatalf("gemini not wired: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".cursor", "mcp.json")); !os.IsNotExist(err) {
		t.Fatalf("cursor wired although declined: %v", err)
	}
}

// --yes without --fix is a usage error; --fix without a terminal and
// without --yes is refused, with a verbs.Error under --json; nothing is
// wired.
func TestDoctorFlagRules(t *testing.T) {
	h := doctorEnv(t, "/nonexistent")
	home := agentsHome(t, h)
	hh, _ := humanEnv(t, "official")
	hh.terminal = func(io.Reader) bool { return false }
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--yes"}, "--yes goes with --fix"},
		{[]string{"--yes", "--json"}, "--yes goes with --fix"},
		{[]string{"--fix"}, "run it in a terminal, or add --yes"},
		{[]string{"--fix", "--json"}, "run it in a terminal, or add --yes"},
		{[]string{"--bogus", "--json"}, "flag"},
		{[]string{"stray"}, "no argument"},
	} {
		code, out, errText := runArgs(t, "y\ny\n", append([]string{"doctor"}, tc.args...)...)
		if code != product.ExitUsage {
			t.Errorf("%v: exit %d", tc.args, code)
		}
		if verbs.WantsJSON(tc.args) {
			var e verbs.Error
			jsonLine(t, out, &e)
			if e.Code != verbs.CodeUsage || e.Lock != "lockfile" || !strings.Contains(e.Error, tc.want) {
				t.Errorf("%v: %+v", tc.args, e)
			}
			if strings.Contains(out, `"checks"`) {
				t.Errorf("%v: a Doctor for a refused doctor: %s", tc.args, out)
			}
		} else if out != "" || !strings.Contains(errText, tc.want) || !strings.Contains(errText, "usage: lockfile doctor [--fix [--yes]] [--json]") {
			t.Errorf("%v: %q %q", tc.args, out, errText)
		}
	}
	for _, d := range []string{".gemini", ".cursor"} {
		if entries, _ := os.ReadDir(filepath.Join(home, d)); len(entries) != 0 {
			t.Fatalf("%s written by a refused doctor", d)
		}
	}
}
