package main

import (
	"errors"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/DavidGodefroid/lockkit/agentwire"
	"github.com/DavidGodefroid/lockkit/sysconf"

	"github.com/DavidGodefroid/lockfile/internal/plugin"
	"github.com/DavidGodefroid/lockfile/internal/product"
)

// doctorEnv is a separated setup seen from an agent account: the console
// account is root (a real account that is not this one), the clients
// group this account's primary group.
func doctorEnv(t *testing.T, home string) *doctorHooks {
	t.Helper()
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	g, err := user.LookupGroupId(u.Gid)
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(t.TempDir(), "run")
	if err := os.Mkdir(sock, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sock, 0o750|os.ModeSetgid); err != nil {
		t.Fatal(err)
	}
	h := &doctorHooks{
		probe: func() (int, bool, error) { return 4, true, nil },
		sys: func() (*sysconf.Config, error) {
			return &sysconf.Config{ServiceUser: "root", ClientGroup: g.Name, SocketDir: sock}, nil
		},
		uid:      os.Getuid,
		home:     func(string) (string, error) { return home, nil },
		lookPath: func(string) (string, error) { return "/usr/bin/cosign", nil },
		agents:   func() (agentwire.Env, error) { return agentwire.Env{}, errors.New("no agents in this test") },
		mounts:   func() ([]byte, error) { return []byte(hiddenProc), nil },
		owner: func(fs.FileInfo) (int, int, bool) {
			gid, _ := strconv.Atoi(g.Gid)
			return 0, gid, true
		},
	}
	testDoctorHooks = h
	t.Cleanup(func() { testDoctorHooks = nil })
	return h
}

func TestDoctorAgentCanReadHomeFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root opens everything")
	}
	open := t.TempDir()
	if err := os.Chmod(open, 0o755); err != nil {
		t.Fatal(err)
	}
	doctorEnv(t, open)
	code, out, _ := runArgs(t, "", "doctor")
	if code != product.ExitError || !strings.Contains(out, "this account can open "+open) {
		t.Fatalf("open home: exit %d:\n%s", code, out)
	}
	closed := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(closed, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(closed, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(closed, 0o700) })
	testDoctorHooks.home = func(string) (string, error) { return closed, nil }
	code, out, _ = runArgs(t, "", "doctor")
	if code != 0 || !strings.Contains(out, "closed to this account") || !strings.Contains(out, "2750") {
		t.Fatalf("closed home: exit %d:\n%s", code, out)
	}
}

func TestDoctorSocketDirMode(t *testing.T) {
	h := doctorEnv(t, "/nonexistent")
	sys, _ := h.sys()
	if err := os.Chmod(sys.SocketDir, 0o710|os.ModeSetgid); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := runArgs(t, "", "doctor"); code != product.ExitError || !strings.Contains(out, "want 2750") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}

func TestDoctorRefusesWithoutSandboxOrSetup(t *testing.T) {
	h := doctorEnv(t, "/nonexistent")
	h.probe = func() (int, bool, error) { return 0, false, errors.New("no landlock") }
	h.sys = func() (*sysconf.Config, error) { return nil, nil }
	humanEnv(t, "official")
	code, out, _ := runArgs(t, "", "doctor", "--json")
	if code != product.ExitError || !strings.Contains(out, `"check":"sandbox"`) || !strings.Contains(out, "sudo lockfile install") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}

func TestDoctorOutputHasNoSecretOrPath(t *testing.T) {
	h := doctorEnv(t, "/nonexistent")
	h.sys = func() (*sysconf.Config, error) { return nil, nil }
	hh, _ := humanEnv(t, "official")
	cfg := "work_dir = \"" + filepath.Join(hh.stateDir, "secret-work") + "\"\n[profiles.docs]\nsource = \"s3\"\nbucket = \"private-bucket\"\nregion = \"eu-west-1\"\n" +
		"prefix = \"invoices/2026/\"\ncredentials = \"vault\"\nvault_path = \"secret/data/lockfile/docs\"\n" +
		"[[marketplaces]]\nname = \"acme\"\nindex_url = \"https://plugins.example.org/index.json\"\nidentity_regexp = \"^https://example\\\\.org/\"\noidc_issuer = \"https://issuer.example.org\"\n"
	if err := os.WriteFile(hh.configPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(hh.stateDir, "secret-work"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, out, errText := runArgs(t, "", "doctor")
	all := out + errText
	for _, leak := range []string{hh.stateDir, "secret-work", "private-bucket", "invoices/", "secret/data", hh.configPath} {
		if strings.Contains(all, leak) {
			t.Errorf("doctor prints %q:\n%s", leak, all)
		}
	}
	if !strings.Contains(all, "work directory has mode 0755") || !strings.Contains(all, "UNOFFICIAL acme") {
		t.Fatalf("missing findings:\n%s", all)
	}
}

const (
	hiddenProc = "22 1 0:21 / /proc rw,nosuid,nodev,noexec,relatime shared:12 - proc proc rw,hidepid=invisible,gid=27\n"
	openProc   = "22 1 0:21 / /proc rw,nosuid,nodev,noexec,relatime shared:12 - proc proc rw\n"
)

func TestDoctorProcHidepid(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("/proc is Linux only")
	}
	h := doctorEnv(t, "/nonexistent")
	h.mounts = func() ([]byte, error) { return []byte(openProc), nil }
	code, out, _ := runArgs(t, "", "doctor")
	if code != product.ExitError || !strings.Contains(out, "mount -o remount,hidepid=invisible,gid=<admin group> /proc") ||
		!strings.Contains(out, "proc /proc proc defaults,hidepid=invisible,gid=<admin group> 0 0") {
		t.Fatalf("open /proc: exit %d:\n%s", code, out)
	}
	h.mounts = func() ([]byte, error) { return nil, errors.New("unreadable") }
	if code, out, _ := runArgs(t, "", "doctor"); code != product.ExitError || !strings.Contains(out, "cannot read the mount table") {
		t.Fatalf("unreadable: exit %d:\n%s", code, out)
	}
	h.mounts = func() ([]byte, error) { return []byte(hiddenProc), nil }
	if _, out, _ := runArgs(t, "", "doctor"); !strings.Contains(out, "hidepid=invisible: other accounts") {
		t.Fatalf("hidden /proc:\n%s", out)
	}
}

func TestDoctorSocketDirOwner(t *testing.T) {
	h := doctorEnv(t, "/nonexistent")
	h.owner = func(fs.FileInfo) (int, int, bool) { return 4242, 4242, true }
	if code, out, _ := runArgs(t, "", "doctor"); code != product.ExitError || !strings.Contains(out, "is owned by 4242:4242, want root:") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}

// openUp gives "other" traversal on dir and the test's own parents up to
// the system temp directory, so the walk reaches the system's directories.
func openUp(t *testing.T, dir string) {
	t.Helper()
	tmp := filepath.Clean(os.TempDir())
	for p := dir; p != tmp && p != "/" && p != "."; p = filepath.Dir(p) {
		if err := os.Chmod(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func details(fs []finding) string {
	var b strings.Builder
	for _, f := range fs {
		b.WriteString(f.Status + " " + f.Detail + "\n")
	}
	return b.String()
}

func TestWorkDirFindings(t *testing.T) {
	closed := t.TempDir() // 0700
	// Absent: said, not silent; the parents are still checked.
	out := details(workDirFindings(filepath.Join(closed, "work"), ""))
	if !strings.Contains(out, "ok not created yet") || strings.Contains(out, "warn") {
		t.Errorf("absent:\n%s", out)
	}
	// Under a sticky world-writable directory.
	tmp := filepath.Join(closed, "tmp")
	if err := os.Mkdir(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(tmp, 0o777|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	out = details(workDirFindings(filepath.Join(tmp, "work"), ""))
	if !strings.Contains(out, "world-writable directory") || strings.Contains(out, "traverse") {
		t.Errorf("under tmp:\n%s", out)
	}
	// Parents open to other accounts.
	open := filepath.Join(t.TempDir(), "a")
	if err := os.MkdirAll(filepath.Join(open, "work"), 0o700); err != nil {
		t.Fatal(err)
	}
	openUp(t, open)
	out = details(workDirFindings(filepath.Join(open, "work"), ""))
	if !strings.Contains(out, "ok 0700") || !strings.Contains(out, "traverse the work directory's parents") {
		t.Errorf("open parents:\n%s", out)
	}
	// On a network filesystem.
	work := filepath.Join(closed, "nfs", "work")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(filepath.Join(closed, "nfs"))
	if err != nil {
		t.Fatal(err)
	}
	table := "1 0 0:1 / / rw - ext4 /dev/sda1 rw\n" +
		"2 1 0:2 / " + real + " rw - nfs4 server:/export rw\n"
	out = details(workDirFindings(work, table))
	if !strings.Contains(out, "shared filesystem (nfs4)") {
		t.Errorf("nfs:\n%s", out)
	}
	if out = details(workDirFindings(work, "1 0 0:1 / / rw - ext4 /dev/sda1 rw\n")); strings.Contains(out, "shared") {
		t.Errorf("local disk:\n%s", out)
	}
	for _, p := range []string{closed, open, real} {
		if strings.Contains(out, p) {
			t.Errorf("finding names a path: %s", out)
		}
	}
}

func TestReachableByOthers(t *testing.T) {
	gid := os.Getgid()
	owner := func(fi fs.FileInfo) (int, int, bool) { return fileOwner(fi) }
	closed := filepath.Join(t.TempDir(), "src")
	if err := os.Mkdir(closed, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := reachableByOthers(closed, gid, owner); err != nil || got {
		t.Errorf("under a 0700 parent: %v %v", got, err)
	}
	open := filepath.Join(t.TempDir(), "src")
	if err := os.Mkdir(open, 0o755); err != nil {
		t.Fatal(err)
	}
	openUp(t, open)
	if got, err := reachableByOthers(open, -1, owner); err != nil || !got {
		t.Errorf("0755 chain: %v %v", got, err)
	}
	if err := os.Chmod(open, 0o750); err != nil {
		t.Fatal(err)
	}
	if got, _ := reachableByOthers(open, -1, owner); got {
		t.Errorf("0750, no clients group: reachable")
	}
	if got, _ := reachableByOthers(open, gid, owner); !got {
		t.Errorf("0750 owned by the clients group: not reachable")
	}
	if _, err := reachableByOthers(filepath.Join(open, "missing"), gid, owner); err == nil {
		t.Errorf("missing directory: no error")
	}
}

// doctor reports an installed version plugins.lock records as yanked.
func TestDoctorReportsYanked(t *testing.T) {
	h := doctorEnv(t, "/nonexistent")
	h.sys = func() (*sysconf.Config, error) { return nil, nil }
	hh, _ := humanEnv(t, "official")
	if err := os.WriteFile(hh.configPath, []byte("[profiles.docs]\nsource = \"local\"\npath = \"/srv/x\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errText := runArgs(t, "", "plugin", "install", "pdf.parses@1.0.0"); code != 0 {
		t.Fatal(errText)
	}
	if _, out, _ := runArgs(t, "", "doctor"); !strings.Contains(out, "1 installed, none recorded as yanked") {
		t.Fatalf("before yank:\n%s", out)
	}
	s := plugin.Store{Dir: filepath.Join(hh.stateDir, product.P.Name)}
	if _, err := s.MarkYanked(plugin.OfficialMarketplace, func(string, string) (bool, bool) { return true, true }); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runArgs(t, "", "doctor")
	if code != product.ExitError || !strings.Contains(out, "pdf.parses@1.0.0 was withdrawn by its marketplace") {
		t.Fatalf("yanked: exit %d:\n%s", code, out)
	}
}

func TestDoctorLocalSource(t *testing.T) {
	h := doctorEnv(t, "/nonexistent")
	h.sys = func() (*sysconf.Config, error) { return nil, nil }
	hh, _ := humanEnv(t, "official")
	src := filepath.Join(t.TempDir(), "samples-src")
	if err := os.Mkdir(src, 0o755); err != nil {
		t.Fatal(err)
	}
	openUp(t, src)
	cfg := "[profiles.docs]\nsource = \"local\"\npath = \"" + src + "\"\n"
	if err := os.WriteFile(hh.configPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runArgs(t, "", "doctor")
	if code != product.ExitError || !strings.Contains(out, "profile docs: its directory can be reached by other accounts") || strings.Contains(out, src) {
		t.Fatalf("open source: exit %d:\n%s", code, out)
	}
	if err := os.Chmod(src, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, out, _ := runArgs(t, "", "doctor"); !strings.Contains(out, "profile docs: closed to other accounts") {
		t.Fatalf("closed source:\n%s", out)
	}
}
