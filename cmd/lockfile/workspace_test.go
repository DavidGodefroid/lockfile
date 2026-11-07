package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DavidGodefroid/lockkit/sysconf"

	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/profile"
)

// workspaceHooks: the workspace mode the launcher sets (LOCKFILE_WORKSPACE),
// with a lockfile account check that always refuses, so that a command
// that still asked for that account fails.
func workspaceHooks(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	testHumanHooks = &humanHooks{
		sys: func() (*sysconf.Config, error) { return nil, errors.New("lockfile account asked for") },
	}
	t.Cleanup(func() { testHumanHooks = nil })
	t.Setenv(workspaceEnv, ws)
	return ws
}

// profile add, in the workspace mode, writes <workspace>/.lockfile/config.toml,
// where lox run reads the lock's profile (lockhub's profileDir), with
// owner-only modes lox run keeps, and never the lockfile account's file.
func TestProfileAddWorkspace(t *testing.T) {
	ws := workspaceHooks(t)
	code, out, errText := runArgs(t, "", "profile", "add", "--profile", "samples", "--source", "local", "--path", "/srv/lockfile/samples")
	if code != product.ExitOK {
		t.Fatalf("exit %d: %s %s", code, out, errText)
	}
	dir := filepath.Join(ws, ".lockfile")
	file := filepath.Join(dir, "config.toml")
	if !strings.Contains(out, "added profile samples to "+file) || !strings.Contains(out, "lox add lockfile INSTANCE --profile samples") {
		t.Fatalf("output: %s", out)
	}
	for p, want := range map[string]os.FileMode{dir: 0o700, file: 0o600} {
		fi, err := os.Lstat(p)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != want {
			t.Errorf("%s: mode %04o, want %04o", p, fi.Mode().Perm(), want)
		}
	}
	f, err := profile.ReadFile(file)
	if err != nil || f.Profiles["samples"].LocalPath != "/srv/lockfile/samples" {
		t.Fatalf("%+v %v", f, err)
	}
	// A second profile goes to the same file.
	if code, _, errText := runArgs(t, "", "profile", "add", "--profile", "docs", "--source", "local", "--path", "/srv/docs"); code != product.ExitOK {
		t.Fatalf("second: exit %d: %s", code, errText)
	}
	if f, err := profile.ReadFile(file); err != nil || len(f.Profiles) != 2 {
		t.Fatalf("%+v %v", f, err)
	}
}

// The marketplace commands edit the same file in the workspace mode.
func TestMarketplaceWorkspace(t *testing.T) {
	ws := workspaceHooks(t)
	code, out, errText := runArgs(t, "acme\n", "marketplace", "add", "acme", "--index", "https://example.org/index.json",
		"--identity-regexp", "^https://github\\.com/acme/plugins/\\.github/workflows/release\\.yml@refs/tags/v[0-9.]+$")
	if code != product.ExitOK {
		t.Fatalf("add: exit %d: %s %s", code, out, errText)
	}
	file := filepath.Join(ws, ".lockfile", "config.toml")
	if f, err := profile.ReadFile(file); err != nil || len(f.Marketplaces) != 1 {
		t.Fatalf("%+v %v", f, err)
	}
	if code, out, errText := runArgs(t, "", "marketplace", "list"); code != product.ExitOK || !strings.Contains(out, "acme") {
		t.Fatalf("list: exit %d: %s %s", code, out, errText)
	}
	if code, _, errText := runArgs(t, "", "marketplace", "remove", "acme"); code != product.ExitOK {
		t.Fatalf("remove: exit %d: %s", code, errText)
	}
	if f, err := profile.ReadFile(file); err != nil || len(f.Marketplaces) != 0 {
		t.Fatalf("%+v %v", f, err)
	}
}

// A profile directory lox run would refuse is refused before anything is
// written: a link, one others may write, one that is not yours.
func TestProfileAddWorkspaceRefuses(t *testing.T) {
	add := func(t *testing.T) (int, string) {
		code, _, errText := runArgs(t, "", "profile", "add", "--profile", "samples", "--source", "local", "--path", "/srv/x")
		return code, errText
	}
	t.Run("link", func(t *testing.T) {
		ws := workspaceHooks(t)
		target := t.TempDir()
		if err := os.Symlink(target, filepath.Join(ws, ".lockfile")); err != nil {
			t.Fatal(err)
		}
		if code, errText := add(t); code != product.ExitError || !strings.Contains(errText, "not a directory") {
			t.Fatalf("exit %d: %s", code, errText)
		}
		if _, err := os.Stat(filepath.Join(target, "config.toml")); !os.IsNotExist(err) {
			t.Fatal("wrote through the link")
		}
	})
	t.Run("config link", func(t *testing.T) {
		ws := workspaceHooks(t)
		if err := os.Mkdir(filepath.Join(ws, ".lockfile"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(t.TempDir(), "x.toml"), filepath.Join(ws, ".lockfile", "config.toml")); err != nil {
			t.Fatal(err)
		}
		if code, errText := add(t); code != product.ExitError || !strings.Contains(errText, "not a regular file") {
			t.Fatalf("exit %d: %s", code, errText)
		}
	})
	t.Run("group writable", func(t *testing.T) {
		ws := workspaceHooks(t)
		dir := filepath.Join(ws, ".lockfile")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o770); err != nil {
			t.Fatal(err)
		}
		if code, errText := add(t); code != product.ExitError || !strings.Contains(errText, "writable by others") {
			t.Fatalf("exit %d: %s", code, errText)
		}
		if _, err := os.Stat(filepath.Join(dir, "config.toml")); !os.IsNotExist(err) {
			t.Fatal("wrote in a shared directory")
		}
	})
	t.Run("not yours", func(t *testing.T) {
		workspaceHooks(t)
		testHumanHooks.euid = func() int { return os.Geteuid() + 1 }
		ws := os.Getenv(workspaceEnv)
		if err := os.Mkdir(filepath.Join(ws, ".lockfile"), 0o700); err != nil {
			t.Fatal(err)
		}
		if code, errText := add(t); code != product.ExitError || !strings.Contains(errText, "not by you") {
			t.Fatalf("exit %d: %s", code, errText)
		}
	})
	t.Run("relative", func(t *testing.T) {
		workspaceHooks(t)
		t.Setenv(workspaceEnv, "relative/ws")
		if code, errText := add(t); code != product.ExitError || !strings.Contains(errText, "absolute") {
			t.Fatalf("exit %d: %s", code, errText)
		}
	})
}

// Without the workspace mode, profile add still asks for the lockfile
// account (the check workspaceHooks makes fail).
func TestProfileAddAccountWithoutWorkspace(t *testing.T) {
	workspaceHooks(t)
	t.Setenv(workspaceEnv, "")
	if code, _, errText := runArgs(t, "", "profile", "add", "--profile", "samples", "--source", "local", "--path", "/srv/x"); code != product.ExitError || !strings.Contains(errText, "lockfile account asked for") {
		t.Fatalf("exit %d: %s", code, errText)
	}
}
