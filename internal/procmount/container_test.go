package procmount

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	consoleUID = 10001
	selfPID    = 42
)

// fakeContainer builds a /proc with this process (pid 42) in pid namespace
// ns, and one process per uids entry; the declaration is decl, mode 0644.
func fakeContainer(t *testing.T, ns, decl string, uids ...string) Container {
	t.Helper()
	dir := t.TempDir()
	proc := filepath.Join(dir, "proc")
	mustMkdir(t, filepath.Join(proc, "42", "ns"))
	mustSymlink(t, "42", filepath.Join(proc, "self"))
	mustSymlink(t, ns, filepath.Join(proc, "42", "ns", "pid"))
	mustWrite(t, filepath.Join(proc, "42", "status"), "Name:\tlockfile\nUid:\t10001\t10001\t10001\t10001\n", 0o444)
	mustWrite(t, filepath.Join(proc, "1", "status"), "Name:\tdocker-init\nUid:\t0\t0\t0\t0\n", 0o444)
	for i, u := range uids {
		mustWrite(t, filepath.Join(proc, string(rune('a'+i)), "status"), "", 0o444) // not a pid: ignored
		mustWrite(t, filepath.Join(proc, "1"+strings.Repeat("0", i+1), "status"), "Uid:\t"+u+"\n", 0o444)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "container.toml")
	mustWrite(t, file, decl, 0o644)
	return Container{File: file, Proc: proc, PID: selfPID, UID: consoleUID,
		Owner: func(fs.FileInfo) (int, bool) { return 0, true }}
}

const (
	privateNS = "pid:[4026532481]"
	hostNS    = "pid:[4026531836]"
	goodDecl  = "pid_namespace = \"private\"\n"
)

func TestContainerAccepts(t *testing.T) {
	c := fakeContainer(t, privateNS, goodDecl, "10001\t10001\t10001\t10001", "0\t0\t0\t0")
	if err := c.Check(); err != nil {
		t.Fatalf("Check = %v, want nil", err)
	}
}

func TestContainerFileRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(t *testing.T, c *Container)
		want string
	}{
		{"missing", func(t *testing.T, c *Container) { os.Remove(c.File) }, "no such file"},
		{"not root", func(t *testing.T, c *Container) {
			c.Owner = func(fi fs.FileInfo) (int, bool) {
				if fi.IsDir() {
					return 0, true
				}
				return consoleUID, true
			}
		}, "not owned by root"},
		{"owner unknown", func(t *testing.T, c *Container) {
			c.Owner = func(fi fs.FileInfo) (int, bool) { return 0, fi.IsDir() }
		}, "not owned by root"},
		{"group writable", func(t *testing.T, c *Container) { os.Chmod(c.File, 0o664) }, "writable"},
		{"others writable", func(t *testing.T, c *Container) { os.Chmod(c.File, 0o646) }, "writable"},
		{"symlink", func(t *testing.T, c *Container) {
			link := c.File + ".link"
			mustSymlink(t, c.File, link)
			c.File = link
		}, "not a regular file"},
		{"directory writable", func(t *testing.T, c *Container) { os.Chmod(filepath.Dir(c.File), 0o777) }, "its directory"},
		{"directory not root", func(t *testing.T, c *Container) {
			dir := filepath.Dir(c.File)
			c.Owner = func(fi fs.FileInfo) (int, bool) {
				if fi.IsDir() && fi.Name() == filepath.Base(dir) {
					return consoleUID, true
				}
				return 0, true
			}
		}, "its directory"},
		{"wrong value", func(t *testing.T, c *Container) {
			mustWrite(t, c.File, "pid_namespace = \"host\"\n", 0o644)
		}, `must be "private"`},
		{"empty", func(t *testing.T, c *Container) { mustWrite(t, c.File, "", 0o644) }, `must be "private"`},
		{"unknown key", func(t *testing.T, c *Container) {
			mustWrite(t, c.File, goodDecl+"hidepid = false\n", 0o644)
		}, "unknown key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fakeContainer(t, privateNS, goodDecl)
			tc.edit(t, &c)
			err := c.Check()
			if !errors.Is(err, ErrContainer) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Check = %v, want ErrContainer with %q", err, tc.want)
			}
		})
	}
}

func TestContainerNamespaceRefused(t *testing.T) {
	c := fakeContainer(t, hostNS, goodDecl)
	if err := c.Check(); !errors.Is(err, ErrContainer) || !strings.Contains(err.Error(), "host's pid namespace") {
		t.Fatalf("initial namespace: Check = %v", err)
	}
	c = fakeContainer(t, "net:[4026532481]", goodDecl)
	if err := c.Check(); !errors.Is(err, ErrContainer) {
		t.Fatalf("garbled link: Check = %v", err)
	}
	// A /proc of another namespace: its self is not this process.
	c = fakeContainer(t, privateNS, goodDecl)
	c.PID = 7
	if err := c.Check(); !errors.Is(err, ErrContainer) || !strings.Contains(err.Error(), "another pid namespace") {
		t.Fatalf("foreign /proc: Check = %v", err)
	}
}

func TestContainerOtherAccountRefused(t *testing.T) {
	for _, uids := range []string{
		"10002\t10002\t10002\t10002", // the agent
		"1000\t1000\t1000\t1000",     // an ALLOWED_UIDS agent
		"10001\t10002\t10001\t10001", // effective uid only
		"10001\t10001\t10002\t10001", // saved uid only
		"10001\t10001\t10001\t10002", // filesystem uid only
	} {
		c := fakeContainer(t, privateNS, goodDecl, "0\t0\t0\t0", uids)
		if err := c.Check(); !errors.Is(err, ErrContainer) || !strings.Contains(err.Error(), "runs as uid") {
			t.Errorf("uids %q: Check = %v, want a refusal", uids, err)
		}
	}
	c := fakeContainer(t, privateNS, goodDecl, "garbage")
	if err := c.Check(); !errors.Is(err, ErrContainer) {
		t.Errorf("unreadable Uid line: Check = %v, want a refusal", err)
	}
}

func TestCheckWith(t *testing.T) {
	const (
		open   = "22 1 0:21 / /proc rw - proc proc rw\n"
		hidden = "22 1 0:21 / /proc rw - proc proc rw,hidepid=invisible\n"
	)
	table := func(s string) func() ([]byte, error) { return func() ([]byte, error) { return []byte(s), nil } }
	good := fakeContainer(t, privateNS, goodDecl)
	bad := fakeContainer(t, hostNS, goodDecl)
	none := good
	none.File = filepath.Join(t.TempDir(), "absent.toml")

	if err := CheckWith(table(hidden), bad); err != nil {
		t.Errorf("hidepid: CheckWith = %v, want nil (the container is not needed)", err)
	}
	if err := CheckWith(table(open), none); !errors.Is(err, ErrNotHidden) || errors.Is(err, ErrContainer) {
		t.Errorf("no declaration: CheckWith = %v, want ErrNotHidden alone", err)
	}
	if err := CheckWith(table(open), good); err != nil {
		t.Errorf("declared container: CheckWith = %v, want nil", err)
	}
	if err := CheckWith(table(open), bad); !errors.Is(err, ErrNotHidden) || !errors.Is(err, ErrContainer) {
		t.Errorf("failing container: CheckWith = %v, want ErrNotHidden and ErrContainer", err)
	}
	if err := CheckWith(table(""), good); !errors.Is(err, ErrNoProc) {
		t.Errorf("no /proc: CheckWith = %v, want ErrNoProc", err)
	}
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, s string, mode os.FileMode) {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	os.Remove(path)
	if err := os.WriteFile(path, []byte(s), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}
