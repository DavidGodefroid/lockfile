package main

import (
	"strings"
	"testing"

	"github.com/DavidGodefroid/lockkit/sysconf"

	"github.com/DavidGodefroid/lockfile/internal/product"
)

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
