// Package e2e checks, over the real socket and framing of a console run in
// the test process, that nothing outside design §7b reaches the agent.
package e2e

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/DavidGodefroid/lockkit/ipc"
	"github.com/DavidGodefroid/lockkit/refusal"

	"github.com/DavidGodefroid/lockfile/internal/answers"
	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/rpc"
	"github.com/DavidGodefroid/lockfile/sandbox"
)

func TestMain(m *testing.M) {
	sandbox.Main()
	os.Exit(m.Run())
}

// wire is a raw client: it keeps every line the console sends.
type wire struct {
	t     *testing.T
	conn  net.Conn
	r     *bufio.Reader
	id    int64
	Lines [][]byte
}

func dialWire(t *testing.T, path string) *wire {
	t.Helper()
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	w := &wire{t: t, conn: c, r: bufio.NewReader(c)}
	if _, resp := w.call(ipc.MethodHello, ipc.HelloParams{ProtocolMajor: ipc.ProtocolMajor, Client: "e2e"}); resp.Error != nil {
		t.Fatalf("hello: %+v", resp.Error)
	}
	return w
}

// call sends one request and returns the raw response line and its
// decoded form.
func (w *wire) call(method string, params any) ([]byte, ipc.Response) {
	w.t.Helper()
	w.id++
	req := ipc.Request{JSONRPC: "2.0", ID: w.id, Method: method}
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			w.t.Fatal(err)
		}
		req.Params = b
	}
	if err := ipc.WriteMsg(w.conn, req); err != nil {
		w.t.Fatal(err)
	}
	_ = w.conn.SetReadDeadline(time.Now().Add(2 * time.Minute))
	line, err := w.r.ReadBytes('\n')
	if err != nil {
		w.t.Fatalf("%s: %v", method, err)
	}
	w.Lines = append(w.Lines, line)
	var resp ipc.Response
	if err := json.Unmarshal(line, &resp); err != nil {
		w.t.Fatal(err)
	}
	return line, resp
}

// vocab is the closed vocabulary of design §7b plus what the agent sent.
type vocab struct {
	strings  map[string]bool
	patterns []*regexp.Regexp
}

// Keys an agent-visible object may carry.
var allowedKeys = map[string]bool{
	// JSON-RPC
	"jsonrpc": true, "id": true, "result": true, "error": true, "code": true, "message": true, "data": true,
	// refusals
	"retry": true, "hint": true,
	// results
	"handle": true, "answers": true, "plugin": true, "version": true, "official": true, "warning": true,
	"plan_id": true, "released": true, "marketplace": true, "question": true, "one": true, "zero": true,
	// lockkit hello and status, lockfile's status part
	"protocol_major": true, "console_version": true, "profile": true, "production": true, "auto_approve": true,
	"allow_unmask": true, "show_results": true, "idle_timeout_in_s": true, "session_ends_in_s": true, "level": true,
	"product": true, "protocol": true, "sandbox": true, "answer_quantum_ms": true,
	// change.request, sources (busy: documented addition to §7b)
	"queued": true, "busy": true,
}

// Keys whose value may be a number, and what it may be.
var numericKeys = map[string]bool{
	"id": true, "code": true, "result": true, "answers": true, "protocol_major": true, "level": true,
	"idle_timeout_in_s": true, "session_ends_in_s": true, "protocol": true, "answer_quantum_ms": true,
}

func closedVocab(agent ...string) vocab {
	v := vocab{strings: map[string]bool{
		"2.0": true, product.Version: true, "samples": true, "ok": true, "unavailable": true,
		"official": true, "acme": true, "1.0.0": true, "yes": true, "no": true, answers.WarningUnofficial: true,
		rpc.MsgPlanParams: true, rpc.MsgNoSuchPlan: true, rpc.MsgConnLost: true, rpc.MsgInternal: true,
	}}
	for _, c := range answers.Codes {
		v.strings[string(c)] = true
		if product.Mapped(c) {
			rc := product.Code(c)
			v.strings[string(rc)], v.strings[rc.Hint()], v.strings[string(rc.Retry())] = true, true, true
		}
	}
	for _, r := range []refusal.Retry{refusal.RetryRewrite, refusal.RetryAskUser, refusal.RetryStop} {
		v.strings[string(r)] = true
	}
	for _, s := range agent {
		v.strings[s] = true
	}
	v.patterns = []*regexp.Regexp{
		regexp.MustCompile(`^pl_[0-9A-Za-z]{10}$`),
		regexp.MustCompile(`^f_[0-9A-Za-z]+$`),
		// lockkit's fixed texts naming the agent's own method.
		regexp.MustCompile(`^invalid params for [a-z.]+$`),
		regexp.MustCompile(`^unknown method "[a-z.]+"$`),
	}
	return v
}

func (v vocab) allowed(s string) bool {
	if v.strings[s] {
		return true
	}
	for _, p := range v.patterns {
		if p.MatchString(s) {
			return true
		}
	}
	return false
}

// check walks one raw response and fails on any key, string or number
// outside the vocabulary.
func (v vocab) check(t *testing.T, raw []byte) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var x any
	if err := dec.Decode(&x); err != nil {
		t.Fatalf("not JSON: %s", raw)
	}
	var walk func(key string, x any)
	walk = func(key string, x any) {
		switch y := x.(type) {
		case map[string]any:
			for k, val := range y {
				if !allowedKeys[k] {
					t.Errorf("key %q outside §7b in %s", k, raw)
				}
				walk(k, val)
			}
		case []any:
			for _, val := range y {
				walk(key, val)
			}
		case string:
			if !v.allowed(y) {
				t.Errorf("string %q (key %s) outside §7b in %s", y, key, raw)
			}
		case json.Number:
			if !numericKeys[key] {
				t.Errorf("number %s under key %q in %s", y, key, raw)
			}
			if key == "result" && y != "0" && y != "1" {
				t.Errorf("result %s in %s", y, raw)
			}
		}
	}
	walk("", x)
}
