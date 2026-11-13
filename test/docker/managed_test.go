//go:build docker

package docker

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// managedVolume is a volume holding managed.toml as a company installs it
// (root 0:0, 0644, its directory 0755), with mode the file's mode; it is
// mounted on /etc/lockfamily.
func managedVolume(t *testing.T, body, mode string) []string {
	t.Helper()
	v := fmt.Sprintf("lockfile-e2e-managed-%d", time.Now().UnixNano())
	mustDk(t, "volume", "create", v)
	t.Cleanup(func() { dk(t, "volume", "rm", "-f", v) })
	cmd := exec.Command(dockerCLI, "run", "--rm", "-i", "--user", "0:0", "--network", "none", "-v", v+":/etc/lockfamily",
		"--entrypoint", "sh", image, "-euc",
		"cat > /etc/lockfamily/managed.toml && chown 0:0 /etc/lockfamily /etc/lockfamily/managed.toml && chmod 0755 /etc/lockfamily && chmod "+mode+" /etc/lockfamily/managed.toml")
	cmd.Stdin = strings.NewReader(body)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("managed volume: %v\n%s", err, out)
	}
	return []string{"-v", v + ":/etc/lockfamily:ro"}
}

const managedBody = "version = 1\norganisation = \"Acme\"\n[lockfile.bounds]\nmax_object_bytes = \"%s\"\n"

// The release image (no test tag) reads the managed file at its fixed
// path: the console names it at start, `managed check` refuses a profile
// beyond its bounds (exit 3), and a file others may write refuses the
// console's start.
func TestManagedConfiguration(t *testing.T) {
	s := newSetup(t)
	within := managedVolume(t, fmt.Sprintf(managedBody, "100MiB"), "0644")
	s.console(t, image, append([]string{"--security-opt", "seccomp=" + s.seccomp}, within...)...)
	s.waitText(t, "Managed by Acme (sha256:")
	s.waitServed(t)

	run := func(managed []string, args ...string) (string, error) {
		a := append([]string{"run", "--rm", "--user", "lockfile", "--network", "none", "-v", s.home + ":/home/lockfile"}, managed...)
		out, errs, err := dk(t, append(append(a, image), args...)...)
		return out + errs, err
	}
	if out, err := run(within, "managed", "check"); err != nil || !strings.Contains(out, "1 profile(s) within the company policy") {
		t.Fatalf("managed check within the bounds: %v\n%s", err, out)
	}
	beyond := managedVolume(t, fmt.Sprintf(managedBody, "10MiB"), "0644")
	out, err := run(beyond, "managed", "check")
	var ee *exec.ExitError
	if !asExit(err, &ee) || ee.ExitCode() != 3 ||
		!strings.Contains(out, "blocked by company policy: lockfile.bounds.max_object_bytes = 10485760 (profile samples: limits.max_object_bytes = 52428800)") {
		t.Fatalf("managed check beyond the bounds: %v\n%s", err, out)
	}

	writable := managedVolume(t, fmt.Sprintf(managedBody, "100MiB"), "0666")
	out, err = run(writable, "console", "--profile", "samples", "--hub", s.hub.URL(), "--hub-key-file", "/run/lockfile-hub/hub.pub", "--instance", hubInstance)
	if !asExit(err, &ee) || ee.ExitCode() != 3 || !strings.Contains(out, "must be writable by root only") {
		t.Fatalf("console with a managed file others may write: %v\n%s", err, out)
	}
}

func asExit(err error, ee **exec.ExitError) bool {
	e, ok := err.(*exec.ExitError)
	*ee = e
	return ok
}
