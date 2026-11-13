//go:build lockfamily_managed_test

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DavidGodefroid/lockkit/managed"
	"github.com/DavidGodefroid/lockkit/sysconf"

	"github.com/DavidGodefroid/lockfile/internal/product"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite cmd/lockfile/testdata/managed/*.golden")

const testManaged = `version = 1
organisation = "Acme"

[lockfile.bounds]
max_object_bytes = "10MiB"
source = ["s3", "sftp"]
marketplaces = ["official"]

[lockfile.defaults]
handle_ttl = "10m"

[lockfile.profiles.erp]
source = "sftp"
host = "erp.corp.example"
user = "files"
known_hosts = "/etc/ssh/known_hosts"
auth = "key"
key = "/keys/erp"
credentials = "none"
max_object_bytes = "5MiB"
`

// placeManaged writes body as the machine's managed file for the test and
// returns its path and fingerprint ("" body: no managed file).
func placeManaged(t *testing.T, body string) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), managed.FileName)
	if body != "" {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	managed.SetTestPath(path)
	t.Cleanup(func() { managed.SetTestPath("") })
	sum := sha256.Sum256([]byte(body))
	return path, hex.EncodeToString(sum[:])
}

// golden compares got with testdata/managed/<name>.golden, where {{PATH}},
// {{SHA}} and {{SHA12}} stand for the managed file's path and fingerprint.
func golden(t *testing.T, name, got, path, sha string) {
	t.Helper()
	got = strings.ReplaceAll(got, path, "{{PATH}}")
	got = strings.ReplaceAll(got, sha, "{{SHA}}")
	got = strings.ReplaceAll(got, sha[:12], "{{SHA12}}")
	file := filepath.Join("testdata", "managed", name+".golden")
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("%s:\ngot:\n%s\nwant:\n%s", name, got, want)
	}
}

func TestManagedShow(t *testing.T) {
	humanEnv(t, "official")
	t.Run("no file", func(t *testing.T) {
		placeManaged(t, "")
		code, out, errText := runArgs(t, "", "managed", "show")
		if code != product.ExitOK || out != "no managed configuration\n" || errText != "" {
			t.Fatalf("exit %d %q %q", code, out, errText)
		}
	})
	t.Run("file", func(t *testing.T) {
		path, sha := placeManaged(t, testManaged)
		code, out, errText := runArgs(t, "", "managed", "show")
		if code != product.ExitOK || errText != "" {
			t.Fatalf("exit %d %q", code, errText)
		}
		golden(t, "show", out, path, sha)
	})
	t.Run("refused file", func(t *testing.T) {
		path, sha := placeManaged(t, "version = 2\n")
		code, out, errText := runArgs(t, "", "managed", "show")
		if code != product.ExitManaged || out != "" {
			t.Fatalf("exit %d %q", code, out)
		}
		golden(t, "show-refused", errText, path, sha)
	})
	t.Run("refused lockfile section", func(t *testing.T) {
		path, sha := placeManaged(t, "version = 1\n[lockfile.bounds]\nsoruce = [\"s3\"]\n")
		code, out, errText := runArgs(t, "", "managed", "show")
		if code != product.ExitManaged || out != "" {
			t.Fatalf("exit %d %q", code, out)
		}
		golden(t, "show-refused-section", errText, path, sha)
	})
	if code, _, _ := runArgs(t, "", "managed"); code != product.ExitUsage {
		t.Fatalf("no subcommand: exit %d", code)
	}
}

func TestManagedCheck(t *testing.T) {
	h, _ := humanEnv(t, "official")
	write := func(cfg string) {
		t.Helper()
		if err := os.WriteFile(h.configPath, []byte(cfg), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const within = "[profiles.dev]\nsource = \"s3\"\nbucket = \"invoices\"\nregion = \"eu-west-1\"\nmax_object_bytes = \"10MiB\"\n"
	const beyond = "[profiles.big]\nsource = \"s3\"\nbucket = \"invoices\"\nregion = \"eu-west-1\"\nmax_object_bytes = \"50MiB\"\n" +
		"[profiles.disk]\nsource = \"local\"\npath = \"/srv/files\"\n"

	t.Run("no file", func(t *testing.T) {
		placeManaged(t, "")
		write(beyond)
		code, out, _ := runArgs(t, "", "managed", "check")
		if code != product.ExitOK || out != "no managed configuration\n" {
			t.Fatalf("exit %d %q", code, out)
		}
	})
	t.Run("ok", func(t *testing.T) {
		path, sha := placeManaged(t, testManaged)
		write(within)
		code, out, errText := runArgs(t, "", "managed", "check")
		if code != product.ExitOK || errText != "" {
			t.Fatalf("exit %d %q %q", code, out, errText)
		}
		golden(t, "check-ok", out, path, sha)
		code, out, _ = runArgs(t, "", "managed", "check", "--profile", "erp")
		if code != product.ExitOK || !strings.HasPrefix(out, "1 profile(s) within") {
			t.Fatalf("--profile erp: exit %d %q", code, out)
		}
	})
	t.Run("violation", func(t *testing.T) {
		path, sha := placeManaged(t, testManaged)
		write(beyond)
		code, out, errText := runArgs(t, "", "managed", "check")
		if code != product.ExitManaged || errText != "" {
			t.Fatalf("exit %d %q %q", code, out, errText)
		}
		golden(t, "check-violation", out, path, sha)
		code, out, _ = runArgs(t, "", "managed", "check", "--profile", "big")
		if code != product.ExitManaged || strings.Count(out, "\n") != 1 {
			t.Fatalf("--profile big: exit %d %q", code, out)
		}
		if code, _, _ := runArgs(t, "", "managed", "check", "--profile", "nope"); code != product.ExitError {
			t.Fatalf("unknown profile: exit %d", code)
		}
	})
	t.Run("redefined managed profile", func(t *testing.T) {
		path, sha := placeManaged(t, testManaged)
		write(within + "[profiles.erp]\nsource = \"local\"\npath = \"/srv\"\n")
		code, out, _ := runArgs(t, "", "managed", "check")
		if code != product.ExitManaged {
			t.Fatalf("exit %d %q", code, out)
		}
		golden(t, "check-redefined", out, path, sha)
	})
	t.Run("refused file", func(t *testing.T) {
		path, sha := placeManaged(t, "version = 2\n")
		write(within)
		code, out, _ := runArgs(t, "", "managed", "check")
		if code != product.ExitManaged {
			t.Fatalf("exit %d %q", code, out)
		}
		golden(t, "check-refused", out, path, sha)
	})
}

// The doctor names the managed file in force, none, or why it is refused.
func TestDoctorManagedLine(t *testing.T) {
	h := doctorEnv(t, "/nonexistent")
	h.sys = func() (*sysconf.Config, error) { return nil, nil }
	humanEnv(t, "official")
	managedLine := func() string {
		t.Helper()
		_, out, _ := runArgs(t, "", "doctor")
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, " managed ") {
				return l
			}
		}
		t.Fatalf("no managed line:\n%s", out)
		return ""
	}
	placeManaged(t, "")
	if got, want := managedLine(), "ok    managed      no managed configuration"; got != want {
		t.Errorf("no file: %q, want %q", got, want)
	}
	_, sha := placeManaged(t, testManaged)
	if got, want := managedLine(), "ok    managed      Managed by Acme (sha256:"+sha[:12]+")"; got != want {
		t.Errorf("file: %q, want %q", got, want)
	}
	path, _ := placeManaged(t, "version = 2\n")
	if got, want := managedLine(), red("FAIL")+"  managed      managed: "+path+": version 2 is not supported (this build reads version 1)"; got != want {
		t.Errorf("refused: %q, want %q", got, want)
	}
}
