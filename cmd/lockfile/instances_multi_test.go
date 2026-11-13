package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DavidGodefroid/lockkit/client"
	"github.com/DavidGodefroid/lockkit/console"
	"github.com/DavidGodefroid/lockkit/ipc"
	"github.com/DavidGodefroid/lockkit/sysconf"

	"github.com/DavidGodefroid/lockfile/internal/clientcmd"
	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/rpc"
)

// consoleAsMe makes the test's user the console account of the separated
// setup (instanceSetup), so that the client's peer check accepts the
// consoles statusConsole serves.
func consoleAsMe(t *testing.T) {
	t.Helper()
	me, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(sysconf.Path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "service_user = %q\n", me.Username); err != nil {
		t.Fatal(err)
	}
}

// statusConsole serves profile's console of instance, in dir/instance: it
// answers hello and status, and counts the connections it accepts.
func statusConsole(t *testing.T, dir, instance, profile string) *dials {
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
	return serveDials(t, ln, func(c net.Conn) {
		go func() {
			defer c.Close()
			r := bufio.NewReader(c)
			for {
				var req ipc.Request
				if ipc.ReadMsg(r, &req) != nil {
					return
				}
				resp := console.OKResp(req.ID, console.StatusResult{Profile: profile, Product: rpc.Status{Protocol: 1, Sandbox: "ok"}})
				if req.Method == ipc.MethodHello {
					resp = console.OKResp(req.ID, ipc.HelloResult{ProtocolMajor: ipc.ProtocolMajor, Profile: profile})
				}
				if ipc.WriteMsg(c, resp) != nil {
					return
				}
			}
		}()
	})
}

// Instance resolution with 0, 1 and 2 locks listed by lea: none is the
// socket directory itself, one is that lock, two (none chosen) is both,
// and the flag or LOCKFILE_INSTANCE still picks one of them.
func TestResolveInstancesByCount(t *testing.T) {
	instanceSetup(t)
	for _, c := range []struct {
		list, flag, env string
		inst            string
		all             []string
		err             string
	}{
		{list: "", inst: ""},
		{list: "ftp", inst: "ftp"},
		{list: "ftp s3", all: []string{"ftp", "s3"}},
		{list: "ftp s3", flag: "s3", inst: "s3"},
		{list: "ftp s3", env: "ftp", inst: "ftp"},
		{list: "ftp s3", env: "nope", err: "no lockfile lock nope: ftp s3"},
		{list: "ftp s3", flag: "../x", err: "--instance: not a lock instance name"},
		{list: "ftp ../x", err: "LEA_LOCKFILE_INSTANCES: not a lock instance name"},
	} {
		t.Setenv("LEA_LOCKFILE_INSTANCES", c.list)
		t.Setenv("LOCKFILE_INSTANCE", c.env)
		inst, all, err := clientcmd.ResolveInstances(c.flag)
		if c.err != "" {
			if err == nil || err.Error() != c.err {
				t.Errorf("%+v: error %v, want %q", c, err, c.err)
			}
			continue
		}
		if err != nil || inst != c.inst || !slices.Equal(all, c.all) {
			t.Errorf("%+v: got %q %v %v", c, inst, all, err)
		}
	}
}

// Two lockfile locks, none chosen: every agent command reaches the lock
// that serves its profile, sources lists both, and a profile neither
// serves is unknown_profile (DavidGodefroid/lockfile#60).
func TestInstanceSeveralRoutedByProfile(t *testing.T) {
	dir := instanceSetup(t)
	t.Setenv("LEA_LOCKFILE_INSTANCES", "ftp s3")
	consoleAsMe(t)
	ftp := statusConsole(t, dir, "ftp", "docs")
	s3 := statusConsole(t, dir, "s3", "backups")

	code, out, errText := runArgs(t, "", "sources", "--json")
	var src []rpc.Source
	if code != product.ExitOK || json.Unmarshal([]byte(out), &src) != nil ||
		!slices.Equal(src, []rpc.Source{{Profile: "backups"}, {Profile: "docs"}}) {
		t.Fatalf("sources: exit %d, stdout %q stderr %q", code, out, errText)
	}
	ftp.count(t)
	s3.count(t)
	for _, c := range []struct {
		profile   string
		hit, miss *dials
	}{{"docs", ftp, s3}, {"backups", s3, ftp}} {
		code, out, errText := runArgs(t, "", "status", "--profile", c.profile)
		if code != product.ExitOK || !strings.Contains(out, c.profile+": console up") {
			t.Errorf("status %s: exit %d, stdout %q stderr %q", c.profile, code, out, errText)
		}
		if c.hit.count(t) == 0 || c.miss.count(t) != 0 {
			t.Errorf("status %s did not reach its lock only", c.profile)
		}
	}
	// No profile: both consoles report.
	code, out, errText = runArgs(t, "", "status")
	if code != product.ExitOK || !strings.Contains(out, "docs: console up") || !strings.Contains(out, "backups: console up") {
		t.Errorf("status: exit %d, stdout %q stderr %q", code, out, errText)
	}
	code, out, errText = runArgs(t, "", "status", "--profile", "nope")
	if code != product.ExitError || !strings.Contains(out+errText, "unknown_profile") {
		t.Errorf("status nope: exit %d, stdout %q stderr %q", code, out, errText)
	}
	// Several profiles and none given: the command asks for one.
	code, out, errText = runArgs(t, "", "plugins")
	if code != product.ExitError || !strings.Contains(out+errText, "several lockfile consoles are running: pass the profile") {
		t.Errorf("plugins: exit %d, stdout %q stderr %q", code, out, errText)
	}
}

// mcpSession runs `lockfile mcp args…` on pipes and initializes it; call
// sends a tools/call and returns its result's text and whether it is an
// error.
type mcpSession struct {
	t    *testing.T
	w    *io.PipeWriter
	r    *bufio.Reader
	next int
	done chan int
}

func startMCP(t *testing.T, args ...string) *mcpSession {
	t.Helper()
	in, w := io.Pipe()
	outR, outW := io.Pipe()
	s := &mcpSession{t: t, w: w, r: bufio.NewReader(outR), next: 1, done: make(chan int, 1)}
	go func() {
		code := run(env{stdin: in, stdout: outW, stderr: io.Discard}, append([]string{"mcp"}, args...))
		outW.Close()
		s.done <- code
	}()
	t.Cleanup(func() {
		w.Close()
		select {
		case <-s.done:
		case <-time.After(10 * time.Second):
			t.Error("the MCP server did not end on EOF")
		}
	})
	s.send("initialize", `{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}`)
	if _, err := io.WriteString(w, `{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n"); err != nil {
		t.Fatal(err)
	}
	return s
}

// send writes a request and returns its response's result.
func (s *mcpSession) send(method, params string) json.RawMessage {
	s.t.Helper()
	id := s.next
	s.next++
	msg := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":%q,"params":%s}`, id, method, params)
	if _, err := io.WriteString(s.w, msg+"\n"); err != nil {
		s.t.Fatalf("%s: %v", method, err)
	}
	type reply struct {
		resp struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
		}
		err error
	}
	ch := make(chan reply, 1)
	go func() {
		for {
			line, err := s.r.ReadBytes('\n')
			if err != nil {
				ch <- reply{err: err}
				return
			}
			var r reply
			if json.Unmarshal(line, &r.resp) == nil && r.resp.ID == id {
				ch <- r
				return
			}
		}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			s.t.Fatalf("%s: the server ended: %v", method, r.err)
		}
		return r.resp.Result
	case <-time.After(15 * time.Second):
		s.t.Fatalf("%s: no answer", method)
	}
	return nil
}

func (s *mcpSession) call(tool string, args map[string]any) (string, bool) {
	s.t.Helper()
	a, _ := json.Marshal(args)
	var res struct {
		Content []struct{ Text string } `json:"content"`
		IsError bool                    `json:"isError"`
	}
	raw := s.send("tools/call", fmt.Sprintf(`{"name":%q,"arguments":%s}`, tool, a))
	if err := json.Unmarshal(raw, &res); err != nil {
		s.t.Fatalf("%s: %s", tool, raw)
	}
	var text []string
	for _, c := range res.Content {
		text = append(text, c.Text)
	}
	return strings.Join(text, "\n"), res.IsError
}

// Two lockfile locks and the one server `lockfile init claude` writes
// (`lockfile mcp`, no --instance): the agent lists both profiles and each
// call reaches the lock of its profile (DavidGodefroid/lockfile#60).
func TestMCPSeveralLocks(t *testing.T) {
	dir := instanceSetup(t)
	t.Setenv("LEA_LOCKFILE_INSTANCES", "ftp s3")
	consoleAsMe(t)
	ftp := statusConsole(t, dir, "ftp", "docs")
	s3 := statusConsole(t, dir, "s3", "backups")
	s := startMCP(t)

	text, isErr := s.call("lockfile_sources", map[string]any{})
	if isErr || text != "backups\ndocs" {
		t.Fatalf("lockfile_sources: %q (error %v)", text, isErr)
	}
	ftp.count(t)
	s3.count(t)
	for _, c := range []struct {
		profile   string
		hit, miss *dials
	}{{"docs", ftp, s3}, {"backups", s3, ftp}} {
		text, isErr := s.call("lockfile_status", map[string]any{"profile": c.profile})
		if isErr || !strings.HasPrefix(text, c.profile+": console up") {
			t.Errorf("lockfile_status %s: %q (error %v)", c.profile, text, isErr)
		}
		if c.hit.count(t) == 0 || c.miss.count(t) != 0 {
			t.Errorf("lockfile_status %s did not reach its lock only", c.profile)
		}
	}
}

// An instance that cannot be resolved does not stop the MCP server: it
// starts, and status (every tool) reports the problem, never a closed
// connection (DavidGodefroid/lockfile#60).
func TestMCPInstanceErrorStarts(t *testing.T) {
	dir := instanceSetup(t)
	t.Setenv("LEA_LOCKFILE_INSTANCES", "ftp s3")
	for _, c := range []struct {
		env  string
		args []string
		want string
	}{
		{env: "nope", want: "lockfile: no lockfile lock nope: ftp s3"},
		{args: []string{"--instance", "../x"}, want: "lockfile: --instance: not a lock instance name"},
	} {
		t.Setenv("LOCKFILE_INSTANCE", c.env)
		s := startMCP(t, c.args...)
		for _, tool := range []string{"lockfile_status", "lockfile_sources", "lockfile_plugins"} {
			text, isErr := s.call(tool, map[string]any{})
			if !isErr || !strings.Contains(text, c.want) || strings.Contains(text, "../x") {
				t.Errorf("%v %s: %q (error %v)", c, tool, text, isErr)
			}
		}
	}
	if e, _ := os.ReadDir(dir); len(e) != 0 {
		t.Fatalf("socket dir holds %v", e)
	}
}
