package main

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/DavidGodefroid/lockkit/agentwire"
	"github.com/DavidGodefroid/lockkit/sysconf"

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
