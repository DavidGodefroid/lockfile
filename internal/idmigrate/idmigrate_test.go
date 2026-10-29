//go:build unix

package idmigrate

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// Migrate gives the old ids their new ones, keeps the others, skips a
// missing directory, and changes nothing the second time.
func TestMigrate(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"run/s", "home/a/b"} {
		if err := os.MkdirAll(filepath.Join(root, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("nowhere", filepath.Join(root, "home", "link")); err != nil {
		t.Fatal(err)
	}
	// The fake filesystem's owners, by path; the real files keep the test's.
	owner := map[string][2]uint32{
		filepath.Join(root, "run"):            {10001, 10000},
		filepath.Join(root, "run", "s"):       {10001, 10000},
		filepath.Join(root, "home"):           {10001, 10001},
		filepath.Join(root, "home", "a"):      {10001, 10001},
		filepath.Join(root, "home", "a", "b"): {4242, 10002},
		filepath.Join(root, "home", "link"):   {10031, 10031},
	}
	stat := func(p string) (uint32, uint32) { o := owner[p]; return o[0], o[1] }
	old := Lchown
	t.Cleanup(func() { Lchown = old; statOwner = defaultStatOwner })
	statOwner = func(p string, _ *syscall.Stat_t) (uint32, uint32) { return stat(p) }
	var changed []string
	Lchown = func(p string, uid, gid int) error {
		owner[p] = [2]uint32{uint32(uid), uint32(gid)}
		changed = append(changed, p)
		return nil
	}
	dirs := []string{filepath.Join(root, "run"), filepath.Join(root, "home"), filepath.Join(root, "missing")}
	n, err := Migrate(dirs)
	if err != nil || n != 5 {
		t.Fatalf("Migrate = %d, %v; want 5 files: %q", n, err, changed)
	}
	want := map[string][2]uint32{
		filepath.Join(root, "run"):            {10031, 10030},
		filepath.Join(root, "run", "s"):       {10031, 10030},
		filepath.Join(root, "home"):           {10031, 10031},
		filepath.Join(root, "home", "a"):      {10031, 10031},
		filepath.Join(root, "home", "a", "b"): {4242, 10032},
		filepath.Join(root, "home", "link"):   {10031, 10031},
	}
	for p, w := range want {
		if owner[p] != w {
			t.Errorf("%s: owner %v, want %v", p, owner[p], w)
		}
	}
	if n, err := Migrate(dirs); err != nil || n != 0 {
		t.Fatalf("second Migrate = %d, %v; want 0", n, err)
	}
}
