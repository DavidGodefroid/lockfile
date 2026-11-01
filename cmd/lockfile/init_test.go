package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DavidGodefroid/lockfile/internal/product"
)

// init <agent> writes the agent's files into the project of the current
// directory, never asks (empty stdin), and is idempotent.
func TestInitAgent(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	code, out, errText := runArgs(t, "", "init", "claude")
	if code != product.ExitOK {
		t.Fatalf("exit %d: %s %s", code, out, errText)
	}
	mcp, err := os.ReadFile(filepath.Join(root, ".mcp.json"))
	if err != nil || !strings.Contains(string(mcp), `"lockfile"`) {
		t.Fatalf(".mcp.json: %s %v", mcp, err)
	}
	if code, out, errText := runArgs(t, "", "init", "claude"); code != product.ExitOK || strings.Contains(out, "created") {
		t.Fatalf("second run: exit %d: %s %s", code, out, errText)
	}
	if again, _ := os.ReadFile(filepath.Join(root, ".mcp.json")); string(again) != string(mcp) {
		t.Fatalf("rewritten:\n%s", again)
	}
}

// The agent is named: no default, one only, a known one.
func TestInitUsage(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, args := range [][]string{{"init"}, {"init", "claude", "codex"}, {"init", "--user"}, {"init", "emacs"}} {
		if code, _, errText := runArgs(t, "", args...); code != product.ExitUsage {
			t.Errorf("%v: exit %d: %s", args, code, errText)
		}
	}
	if entries, _ := os.ReadDir("."); len(entries) != 0 {
		t.Fatalf("written: %v", entries)
	}
}
