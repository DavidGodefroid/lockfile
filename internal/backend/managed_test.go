//go:build lockfamily_managed_test

package backend

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DavidGodefroid/lockkit/console"
	"github.com/DavidGodefroid/lockkit/managed"

	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/profile"
)

// writeManaged places a managed file for the test (no ownership check).
func writeManaged(t *testing.T, body string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), managed.FileName)
	if err := os.WriteFile(path, []byte("version = 1\norganisation = \"Acme\"\n"+body), 0o644); err != nil {
		t.Fatal(err)
	}
	managed.SetTestPath(path)
	t.Cleanup(func() { managed.SetTestPath("") })
}

// The backend is a console.ManagedBackend: a policy beyond
// [lockfile.bounds] is refused citing the key, one within it is not.
func TestManagedBoundsBackend(t *testing.T) {
	writeManaged(t, "[lockfile.bounds]\nmax_object_bytes = \"10MiB\"\n")
	h := newHarness(t)
	m, err := managed.Load(t.Context())
	if err != nil || m == nil {
		t.Fatalf("managed file: %v %v", m, err)
	}
	pol, err := h.b.LoadPolicy()
	if err != nil {
		t.Fatal(err)
	}
	// The harness profile keeps the 50 MiB default.
	err = console.CheckManaged(product.P, h.b, pol, m)
	if !errors.Is(err, console.ErrManaged) {
		t.Fatalf("50 MiB under a 10 MiB bound: %v", err)
	}
	lines := console.ManagedLines(err)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "blocked by company policy: lockfile.bounds.max_object_bytes = 10485760 (profile samples:") {
		t.Fatalf("lines %q", lines)
	}

	writeManaged(t, "[lockfile.bounds]\nmax_object_bytes = \"100MiB\"\n")
	if m, err = managed.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := console.CheckManaged(product.P, h.b, pol, m); err != nil {
		t.Fatalf("within the bound: %v", err)
	}
	if _, err := h.b.ManagedBounds(&fakePolicy{}, m); err == nil {
		t.Fatal("a foreign policy was bounded")
	}
}

// fakePolicy is a policy of another product.
type fakePolicy struct{ profile.Policy }
