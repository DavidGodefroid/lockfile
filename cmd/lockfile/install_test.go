package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/separate"
)

func installEnv(t *testing.T, goos string) (*installHooks, *string) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "lockfile")
	if err := os.WriteFile(bin, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	var ran string
	h := &installHooks{executable: bin, goos: goos, tty: true, root: func(s string) error { ran = s; return nil }}
	testInstallHooks = h
	t.Cleanup(func() { testInstallHooks = nil })
	return h, &ran
}

func TestInstallScriptGolden(t *testing.T) {
	_, ran := installEnv(t, "linux")
	code, out, errText := runArgs(t, "", "install", "--client", "agent", "--trust-binary", "--yes")
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, out, errText)
	}
	for _, want := range []string{
		`useradd --create-home --shell /bin/bash --comment "lockfile console" lockfile`,
		"chmod 0700 \"$(getent passwd lockfile | cut -d: -f6)\"",
		"usermod -aG lockfile-clients agent",
		`service_user = "lockfile"`, `client_group = "lockfile-clients"`, `socket_dir   = "/run/lockfile"`,
		"printf 'd /run/lockfile 2750 lockfile lockfile-clients -\\n' > /etc/tmpfiles.d/lockfile.conf",
		`install -m 0755 -o root -g root "$tmp" /usr/local/bin/lockfile`,
		"sha256sum -c -",
	} {
		if !strings.Contains(*ran, want) {
			t.Errorf("script lacks %q:\n%s", want, *ran)
		}
	}
	if !strings.Contains(out, "lockfile console --profile NAME") || strings.Contains(*ran, "2710") {
		t.Fatalf("output:\n%s\nscript:\n%s", out, *ran)
	}
}

func TestInstallRefusesUserOwnedBinary(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("as root the binary is root-owned")
	}
	_, ran := installEnv(t, "linux")
	code, _, errText := runArgs(t, "", "install", "--client", "agent", "--yes")
	if code != product.ExitError || !strings.Contains(errText, "--trust-binary") || *ran != "" {
		t.Fatalf("exit %d: %s", code, errText)
	}
}

// Without --trust-binary, a root-owned binary is pinned as well: the
// script copies it to a root-owned file and checks its sha256 first, so a
// swap between the check and sudo installs nothing.
func TestInstallPinsRootOwnedBinary(t *testing.T) {
	h, ran := installEnv(t, "linux")
	var root string
	for _, p := range []string{"/usr/bin/env", "/bin/sh", "/usr/bin/true"} {
		if fi, err := os.Stat(p); err == nil && fi.Mode().Perm()&0o022 == 0 {
			if sum, msg, err := separate.SourceSum(product.P, p); err == nil && msg == "" && sum != "" {
				root = p
				break
			}
		}
	}
	if root == "" {
		t.Skip("no root-owned, non-writable binary on this system")
	}
	h.executable = root
	code, out, errText := runArgs(t, "", "install", "--client", "agent", "--yes")
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, out, errText)
	}
	sum, err := separate.FileSum(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"echo '" + sum + "  '\"$tmp\" | sha256sum -c -", `install -m 0755 -o root -g root "$tmp" /usr/local/bin/lockfile`} {
		if !strings.Contains(*ran, want) {
			t.Errorf("script lacks %q:\n%s", want, *ran)
		}
	}
}

func TestInstallDarwinRefused(t *testing.T) {
	_, ran := installEnv(t, "darwin")
	code, _, errText := runArgs(t, "", "install", "--client", "agent", "--trust-binary", "--yes")
	if code != product.ExitError || !strings.Contains(errText, "Linux-only in v0.2") || *ran != "" {
		t.Fatalf("exit %d: %s", code, errText)
	}
}

func TestInstallPrintAndConfirm(t *testing.T) {
	h, ran := installEnv(t, "linux")
	if code, out, _ := runArgs(t, "", "install", "--client", "agent", "--trust-binary", "--print"); code != 0 || *ran != "" || !strings.Contains(out, "These commands run as root") {
		t.Fatalf("print: exit %d ran %q", code, *ran)
	}
	if code, out, _ := runArgs(t, "n\n", "install", "--client", "agent", "--trust-binary"); code != 0 || *ran != "" || !strings.Contains(out, "nothing done") {
		t.Fatalf("no: exit %d ran %q", code, *ran)
	}
	h.tty = false
	if code, _, errText := runArgs(t, "y\n", "install", "--client", "agent", "--trust-binary"); code != product.ExitUsage || *ran != "" {
		t.Fatalf("no tty: exit %d: %s", code, errText)
	}
	if code, _, _ := runArgs(t, "", "install", "--client", "lockfile", "--trust-binary", "--yes"); code != product.ExitUsage {
		t.Fatalf("client = service: exit %d", code)
	}
}
