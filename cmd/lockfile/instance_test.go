package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DavidGodefroid/lockkit/client"
	"github.com/DavidGodefroid/lockkit/sysconf"

	"github.com/DavidGodefroid/lockfile/internal/clientcmd"
	"github.com/DavidGodefroid/lockfile/internal/product"
)

// instanceSetup is a separated setup (sysconf) whose socket directory is a
// fresh directory, with no instance variable set. It returns the directory.
func instanceSetup(t *testing.T) string {
	t.Helper()
	t.Setenv("LEA_LOCKFILE_INSTANCES", "")
	t.Setenv("LOCKFILE_INSTANCE", "")
	dir, err := os.MkdirTemp("/tmp", "lfinst")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	conf := filepath.Join(t.TempDir(), "lockfile.toml")
	if err := os.WriteFile(conf, []byte(fmt.Sprintf("socket_dir = %q\n", dir)), 0o644); err != nil {
		t.Fatal(err)
	}
	path, skip := sysconf.Path, sysconf.SkipOwnerCheck
	sysconf.Path, sysconf.SkipOwnerCheck = conf, true
	t.Cleanup(func() {
		sysconf.Path, sysconf.SkipOwnerCheck = path, skip
		clientcmd.SetInstance("")
	})
	return dir
}

// listenInstance listens on profile's console socket of instance, which
// must be in dir/instance, and signals each connection it accepts (and
// closes at once).
func listenInstance(t *testing.T, dir, instance, profile string) <-chan struct{} {
	t.Helper()
	path, err := client.SocketPathInstance(product.P, "/", profile, instance)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != filepath.Join(dir, instance) {
		t.Fatalf("socket %s is not in %s", path, filepath.Join(dir, instance))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	got := make(chan struct{}, 16)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			// Signal first: the client returns once the connection closes.
			select {
			case got <- struct{}{}:
			default:
			}
			c.Close()
		}
	}()
	return got
}

// drained reports whether ch received anything, and empties it.
func drained(ch <-chan struct{}) bool {
	seen := false
	for {
		select {
		case <-ch:
			seen = true
		default:
			return seen
		}
	}
}

// --instance orders reaches the socket in <socket dir>/orders: status of a
// profile, sources (which lists that directory) and a profile's command.
func TestInstanceDialsItsDirectory(t *testing.T) {
	dir := instanceSetup(t)
	got := listenInstance(t, dir, "orders", "samples")
	for _, args := range [][]string{
		{"status", "--profile", "samples", "--instance", "orders"},
		{"sources", "--instance", "orders"},
		{"plugins", "--profile", "samples", "--instance", "orders"},
	} {
		runArgs(t, "", args...)
		if !drained(got) {
			t.Errorf("%v: nothing dialled %s", args, filepath.Join(dir, "orders"))
		}
	}
	// Without --instance, the socket directory itself, as before.
	runArgs(t, "", "status", "--profile", "samples")
	if drained(got) {
		t.Fatal("status without --instance dialled the orders lock")
	}
	// LOCKFILE_INSTANCE and the one lock lea lists pick it too.
	t.Setenv("LOCKFILE_INSTANCE", "orders")
	runArgs(t, "", "status", "--profile", "samples")
	if !drained(got) {
		t.Fatal("LOCKFILE_INSTANCE=orders did not dial the orders lock")
	}
	t.Setenv("LOCKFILE_INSTANCE", "")
	t.Setenv("LEA_LOCKFILE_INSTANCES", "orders")
	runArgs(t, "", "status", "--profile", "samples")
	if !drained(got) {
		t.Fatal("the one listed lock was not dialled")
	}
}

// agentCommands are every agent command with valid arguments otherwise.
var agentCommands = [][]string{
	{"status"},
	{"sources"},
	{"plugins"},
	{"plan", "--key", "invoices/a.pdf", "--plugin", "test.answer0"},
	{"run", "p1"},
	{"answers", "h1"},
	{"release", "h1"},
	{"mcp"},
}

// Several locks listed by lea and none chosen: lockkit's error, a usage
// error.
func TestInstanceSeveralNoneChosen(t *testing.T) {
	dir := instanceSetup(t)
	t.Setenv("LEA_LOCKFILE_INSTANCES", "inventory orders")
	for _, args := range agentCommands {
		code, out, errText := runArgs(t, "", args...)
		if code != product.ExitUsage || out != "" ||
			!strings.Contains(errText, "several lockfile locks: inventory orders; pass --instance NAME (or set LOCKFILE_INSTANCE)") ||
			!strings.Contains(errText, "[--instance NAME]") {
			t.Errorf("%v: exit %d, stdout %q stderr %q", args, code, out, errText)
		}
	}
	if e, _ := os.ReadDir(dir); len(e) != 0 {
		t.Fatalf("socket dir holds %v", e)
	}
}

// A name that is not an instance name is refused before any path is
// built, and never quoted back.
func TestInstanceBadNameRefused(t *testing.T) {
	dir := instanceSetup(t)
	for _, args := range agentCommands {
		args = append(append([]string{}, args...), "--instance", "../x")
		code, out, errText := runArgs(t, "", args...)
		if code != product.ExitUsage || out != "" || !strings.Contains(errText, "--instance: not a lock instance name") ||
			strings.Contains(errText, "../x") {
			t.Errorf("%v: exit %d, stdout %q stderr %q", args, code, out, errText)
		}
	}
	if e, _ := os.ReadDir(dir); len(e) != 0 {
		t.Fatalf("socket dir holds %v", e)
	}
}

// A leftover --instance or LOCKFILE_INSTANCE whose lock is not mounted here
// is named, never swallowed into "console error", and the name is not
// quoted back.
func TestInstanceNotMounted(t *testing.T) {
	dir := instanceSetup(t)
	const want = "no lock directory for the instance set by --instance or LOCKFILE_INSTANCE: no lock of that name is mounted here"
	for _, args := range [][]string{
		{"status", "--profile", "samples", "--instance", "orders"},
		{"status", "--instance", "orders"},
		{"sources", "--instance", "orders"},
		{"plugins", "--profile", "samples", "--instance", "orders"},
		{"plan", "--key", "invoices/a.pdf", "--plugin", "test.answer0", "--instance", "orders"},
		{"run", "p1", "--instance", "orders"},
		{"answers", "h1", "--instance", "orders"},
		{"release", "h1", "--instance", "orders"},
	} {
		code, out, errText := runArgs(t, "", args...)
		both := out + errText
		if code != product.ExitError || !strings.Contains(both, want) ||
			strings.Contains(both, "console error") || strings.Contains(both, "orders") {
			t.Errorf("%v: exit %d, stdout %q stderr %q", args, code, out, errText)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "orders")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a path of the stale instance was created: %v", err)
	}
	// LOCKFILE_INSTANCE, without the flag, names itself too.
	t.Setenv("LOCKFILE_INSTANCE", "orders")
	code, out, errText := runArgs(t, "", "status", "--profile", "samples")
	both := out + errText
	if code != product.ExitError || !strings.Contains(both, want) ||
		strings.Contains(both, "console error") || strings.Contains(both, "orders") {
		t.Errorf("LOCKFILE_INSTANCE: exit %d, stdout %q stderr %q", code, out, errText)
	}
	if _, err := os.Stat(filepath.Join(dir, "orders")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a path of the stale instance was created: %v", err)
	}
}

// lockfile mcp --instance orders: the server's tools dial the orders lock.
func TestMCPInstance(t *testing.T) {
	dir := instanceSetup(t)
	got := listenInstance(t, dir, "orders", "samples")
	in, w := io.Pipe()
	var out, errb bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- run(env{stdin: in, stdout: &out, stderr: &errb}, []string{"mcp", "--instance", "orders"})
	}()
	go func() {
		for _, m := range []string{
			`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
			`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
			`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"lockfile_status","arguments":{"profile":"samples"}}}`,
		} {
			if _, err := io.WriteString(w, m+"\n"); err != nil {
				return
			}
		}
	}()
	select {
	case <-got:
	case code := <-done:
		w.Close()
		t.Fatalf("mcp ended (exit %d) before dialling: %s", code, errb.String())
	case <-time.After(10 * time.Second):
		t.Error("the MCP status tool did not dial the orders lock")
	}
	w.Close()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the MCP server did not end on EOF")
	}
}
