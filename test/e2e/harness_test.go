// Package e2e checks, over the real socket and framing of a console run in
// the test process, that nothing outside design §7b reaches the agent.
package e2e

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"testing"
	"time"

	"github.com/DavidGodefroid/lockkit/ipc"

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
