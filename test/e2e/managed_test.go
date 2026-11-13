//go:build lockfamily_managed_test

package e2e

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DavidGodefroid/lockkit/config"
	"github.com/DavidGodefroid/lockkit/console"
	"github.com/DavidGodefroid/lockkit/managed"

	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/testenv/consoletest"
)

// boundObjects places a managed file bounding max_object_bytes to 10 MiB.
func boundObjects(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), managed.FileName)
	body := "version = 1\norganisation = \"Acme\"\n[lockfile.bounds]\nmax_object_bytes = \"10MiB\"\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	managed.SetTestPath(path)
	t.Cleanup(func() { managed.SetTestPath("") })
	m, err := managed.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return m.Fingerprint
}

const boundKey = "blocked by company policy: lockfile.bounds.max_object_bytes = 10485760 (profile samples: limits.max_object_bytes = 52428800)"

// A profile beyond the managed bounds is refused at start, citing the
// key, before any prompt; the audit records refused-managed.
func TestManagedRefusedAtStart(t *testing.T) {
	boundObjects(t)
	c := consoletest.Start(t, consoletest.Options{Answers: []string{"y"}, Profile: "max_object_bytes = \"50MiB\"\n", MayRefuse: true})
	if !errors.Is(c.Err, console.ErrManaged) {
		t.Fatalf("start: %v\n%s", c.Err, c.IO.Output())
	}
	out := c.IO.Output()
	if !strings.Contains(out, boundKey) || !strings.Contains(out, "Managed by Acme (sha256:") {
		t.Errorf("the console does not cite the key:\n%s", out)
	}
	if strings.Contains(out, "PROMPT") {
		t.Errorf("the human was asked:\n%s", out)
	}
	if audit := c.AuditLog(t); !strings.Contains(audit, `"refused-managed"`) {
		t.Errorf("no refused-managed record:\n%s", audit)
	}
}

// A profile within the bounds starts and its approval carries the managed
// fingerprint; raised beyond them while it runs, the change is refused at
// the next poll, never pending.
func TestManagedWithinThenLoosened(t *testing.T) {
	fp := boundObjects(t)
	c := consoletest.Start(t, consoletest.Options{Answers: []string{"y"}, Profile: "max_object_bytes = \"5MiB\"\n"})
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(c.IO.Output())
		}
	})
	path, err := config.ApprovedPath(product.P, c.StateDir(), config.ApprovedKey("", c.Profile))
	if err != nil {
		t.Fatal(err)
	}
	approved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err := json.Unmarshal(approved, &obj); err != nil || obj[config.ManagedField] != fp {
		t.Fatalf("approved policy without the managed fingerprint: %s", approved)
	}

	cfg, err := os.ReadFile(c.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	loose := strings.Replace(string(cfg), `max_object_bytes = "5MiB"`, `max_object_bytes = "50MiB"`, 1)
	if err := os.WriteFile(c.ConfigPath(), []byte(loose), 0o600); err != nil {
		t.Fatal(err)
	}
	from := c.IO.Lines()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(c.IO.OutputFrom(from), boundKey) {
		if time.Now().After(deadline) {
			t.Fatalf("the loosening was not refused:\n%s", c.IO.OutputFrom(from))
		}
		time.Sleep(100 * time.Millisecond)
	}
	out := c.IO.OutputFrom(from)
	if !strings.Contains(out, "policy change refused by the company's managed configuration (not approvable)") || strings.Contains(out, "PROMPT") {
		t.Errorf("the loosening is not refused without a prompt:\n%s", out)
	}
	if audit := c.AuditLog(t); !strings.Contains(audit, `"refused-managed"`) {
		t.Errorf("no refused-managed record:\n%s", audit)
	}
}
