//go:build unix

package separate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DavidGodefroid/lockkit/kit"
)

var p = kit.Product{Name: "lockfile", Account: "lockfile", ClientsGroup: "lockfile-clients", KeychainService: "lockfile", EnvPrefix: "LOCKFILE_"}

func TestCheck(t *testing.T) {
	for _, bad := range []Options{
		{Client: "agent", Service: "agent", Group: "g"},
		{Client: "Agent!", Service: "lockfile", Group: "g"},
		{Client: "agent", Service: "lockfile", Group: "a b"},
		{Client: "agent; rm", Service: "lockfile", Group: "g"},
		{Client: "agent", Service: "lockfile", Group: "g", SocketMode: "2777"},
	} {
		if Check(bad) == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	o := Options{Client: "agent"}
	Fill(p, &o)
	if Check(o) != nil || o.Service != "lockfile" || o.Group != "lockfile-clients" || o.SocketMode != "2710" {
		t.Errorf("defaults %+v", o)
	}
}

// The scripts name the product's account, group, system file, socket
// directory and binary, and nothing of another product.
func TestScripts(t *testing.T) {
	o := Options{Client: "agent", Service: "lockfile", Group: "lockfile-clients"}
	linux := Script(p, "linux", "/opt/lockfile", o, "")
	for _, want := range []string{"useradd --create-home --shell /bin/bash --comment \"lockfile console\" lockfile",
		"usermod -aG lockfile-clients agent", "cat > /etc/lockfile/system.toml", `service_user = "lockfile"`,
		`socket_dir   = "/run/lockfile"`, "d /run/lockfile 2710 lockfile lockfile-clients -", "/etc/tmpfiles.d/lockfile.conf",
		"install -m 0755 -o root -g root '/opt/lockfile' /usr/local/bin/lockfile"} {
		if !strings.Contains(linux, want) {
			t.Errorf("linux script lacks %q:\n%s", want, linux)
		}
	}
	darwin := Script(p, "darwin", "/opt/lockfile", o, "")
	if !strings.Contains(darwin, "dseditgroup -o edit -a agent -t user lockfile-clients") || !strings.Contains(darwin, `socket_dir   = "/usr/local/var/run/lockfile"`) ||
		!strings.Contains(darwin, "install -d -m 0710 -o lockfile") {
		t.Errorf("darwin script:\n%s", darwin)
	}
	for _, s := range []string{linux, darwin} {
		if strings.Contains(s, "locksql") || strings.Contains(s, "lockmq") {
			t.Errorf("another product named:\n%s", s)
		}
	}
	// A release already at /usr/local/bin is not copied onto itself.
	if s := Script(p, "linux", InstalledBinary(p), o, ""); strings.Contains(s, "install -m 0755") {
		t.Errorf("copy onto itself:\n%s", s)
	}
	if s := Script(p, "linux", InstalledBinary(p), o, strings.Repeat("a", 64)); !strings.Contains(s, `install -m 0755 -o root -g root "$tmp" /usr/local/bin/lockfile`) {
		t.Errorf("pinned copy:\n%s", s)
	}
}

// A listable socket directory (2750) reaches tmpfiles and the fallback.
func TestSocketMode(t *testing.T) {
	o := Options{Client: "agent", Service: "lockfile", Group: "lockfile-clients", SocketMode: "2750"}
	s := Script(p, "linux", "/opt/lockfile", o, "")
	if !strings.Contains(s, "d /run/lockfile 2750 lockfile lockfile-clients -") || !strings.Contains(s, "install -d -m 2750 -o lockfile -g lockfile-clients /run/lockfile") ||
		strings.Contains(s, "2710") {
		t.Errorf("2750 script:\n%s", s)
	}
	if d := Script(p, "darwin", "/opt/lockfile", o, ""); !strings.Contains(d, "install -d -m 0750 -o lockfile") {
		t.Errorf("darwin 0750:\n%s", d)
	}
}

// The script is valid sh.
func TestScriptParses(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	o := Options{Client: "agent", Service: "lockfile", Group: "lockfile-clients", SocketMode: "2750"}
	for _, goos := range []string{"linux", "darwin"} {
		if out, err := exec.Command(sh, "-n", "-c", Script(p, goos, "/opt/it's lockfile", o, strings.Repeat("b", 64))).CombinedOutput(); err != nil {
			t.Errorf("%s: %v\n%s", goos, err, out)
		}
	}
}

// With a pin, the root script copies the binary, checks the copy and stops
// before any change when it differs.
func TestPinnedSourceChecksTheCopy(t *testing.T) {
	if _, err := exec.LookPath("sha256sum"); err != nil {
		t.Skip("sha256sum is not installed")
	}
	bin := filepath.Join(t.TempDir(), "lock file")
	if err := os.WriteFile(bin, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	sum, err := FileSum(bin)
	if err != nil {
		t.Fatal(err)
	}
	prefix, src := PinnedSource(p, bin, sum, "sha256sum -c -")
	run := func() string {
		out, _ := exec.Command("sh", "-c", "set -eu\n"+prefix+"cat "+src+"; echo; echo DONE").CombinedOutput()
		return string(out)
	}
	if out := run(); !strings.Contains(out, "binary\nDONE") {
		t.Fatalf("unchanged binary:\n%s", out)
	}
	if err := os.WriteFile(bin, []byte("replaced"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out := run(); strings.Contains(out, "DONE") || !strings.Contains(out, "lockfile install: the binary changed") {
		t.Fatalf("replaced binary:\n%s", out)
	}
}

func TestSourceRefusal(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("as root the file is root-owned")
	}
	path := filepath.Join(t.TempDir(), "lockfile")
	if err := os.WriteFile(path, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	msg, err := SourceRefusal(p, path)
	if err != nil || !strings.Contains(msg, "could have been replaced by the agent") || !strings.Contains(msg, "--trust-binary") {
		t.Errorf("%q %v", msg, err)
	}
	if _, err := SourceRefusal(p, filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("missing binary accepted")
	}
}
