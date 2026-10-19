package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/DavidGodefroid/lockkit/sysconf"

	"github.com/DavidGodefroid/lockfile/internal/product"
)

// A typed confirmation is read from a terminal only: the right name piped
// in is refused.
func TestTypedConfirmationNeedsTerminal(t *testing.T) {
	h, _ := humanEnv(t, "official")
	h.terminal = func(io.Reader) bool { return false }
	code, _, errText := runArgs(t, "acme\n", addArgs("acme")...)
	if code != product.ExitDenied || !strings.Contains(errText, "terminal") {
		t.Fatalf("piped name: exit %d: %s", code, errText)
	}
	if _, err := os.Stat(h.configPath); !os.IsNotExist(err) {
		t.Fatal("marketplace added from a pipe")
	}
}

// The read-only human commands are gated too: from the agent's account
// the store is empty, and "plugin verify" exiting 0 on it is false
// assurance.
func TestReadCommandsNeedServiceAccount(t *testing.T) {
	h, _ := humanEnv(t, "official")
	h.sys = func() (*sysconf.Config, error) { return &sysconf.Config{ServiceUser: "root"}, nil }
	h.euid = func() int { return 4242 }
	for _, args := range [][]string{
		{"plugin", "list"},
		{"plugin", "verify"},
		{"plugin", "verify", "pdf.parses"},
		{"marketplace", "list"},
	} {
		code, out, errText := runArgs(t, "", args...)
		if code != product.ExitError || out != "" || !strings.Contains(errText, "run this in the root account") {
			t.Fatalf("%v: exit %d: %q %q", args, code, out, errText)
		}
	}
}
