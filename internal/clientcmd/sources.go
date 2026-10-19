package clientcmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/DavidGodefroid/lockkit/client"
	"github.com/DavidGodefroid/lockkit/config"
	"github.com/DavidGodefroid/lockkit/ipc"
	"github.com/DavidGodefroid/lockkit/mcpkit"
	"github.com/DavidGodefroid/lockkit/sysconf"

	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/rpc"
)

// Status is a console's state as the agent sees it: console fields of
// lockkit's status and lockfile's part, nothing else.
type Status struct {
	Profile        string     `json:"profile"`
	Production     bool       `json:"production"`
	AutoApprove    bool       `json:"auto_approve"`
	Level          int        `json:"level"`
	IdleTimeoutInS int        `json:"idle_timeout_in_s"`
	SessionEndsInS int        `json:"session_ends_in_s"`
	Product        rpc.Status `json:"product"`
}

// GetStatus asks profile's console for its status.
func GetStatus(ctx context.Context, profile string) (Status, error) {
	var out Status
	if err := call(ctx, profile, ipc.MethodStatus, nil, &out); err != nil {
		return Status{}, err
	}
	return out, nil
}

// probeTimeout bounds the status call of each socket in Sources. A console
// serves one request at a time: one waiting for the human answers later,
// and is listed as busy.
var probeTimeout = 2 * time.Second

// socketDir is where the consoles' sockets are: the shared directory of a
// separated setup (mode 2750: the clients group may list it), else the
// user's private runtime directory.
func socketDir() (string, error) {
	sys, err := sysconf.Load(product.P)
	if err != nil {
		return "", err
	}
	if sys != nil {
		return sys.SocketDir, nil
	}
	return product.P.RuntimeDir()
}

// Sources lists the profiles of the running consoles, sorted. A socket
// nobody answers on is left out.
func Sources(ctx context.Context) ([]rpc.Source, error) {
	dir, err := socketDir()
	if err != nil {
		return nil, &Error{Kind: KindOther, Msg: "cannot read the lockfile setup; ask the user to run lockfile doctor"}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []rpc.Source{}, nil
		}
		return nil, &Error{Kind: KindOther, Msg: "cannot list the running consoles; ask the user to run lockfile doctor"}
	}
	// Consoles serve the lockfile account's profiles, never a project's.
	prefix := config.ProjectKeyOf("") + "-"
	var (
		mu  sync.Mutex
		out = []rpc.Source{}
		wg  sync.WaitGroup
	)
	for _, e := range entries {
		name := e.Name()
		if e.Type()&os.ModeSocket == 0 || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".sock") {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s, ok := probe(ctx, filepath.Join(dir, name), strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".sock")); ok {
				mu.Lock()
				out = append(out, s)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	slices.SortFunc(out, func(a, b rpc.Source) int { return strings.Compare(a.Profile, b.Profile) })
	return out, nil
}

// probe asks the console on path for its status. profile is the socket's
// name part; a hashed one ("~…") is read from the console's hello.
func probe(ctx context.Context, path, profile string) (rpc.Source, bool) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	if strings.HasPrefix(profile, "~") {
		p, ok := helloProfile(ctx, path)
		if !ok {
			return rpc.Source{}, false
		}
		// The name must lead back to this very socket.
		if want, err := client.SocketPath(product.P, "/", p); err != nil || want != path {
			return rpc.Source{}, false
		}
		profile = p
	}
	c, err := dial(profile)
	if err != nil {
		return rpc.Source{}, false
	}
	defer c.Close()
	var st Status
	err = c.Call(ctx, ipc.MethodStatus, nil, &st)
	switch {
	case err == nil && st.Profile == profile:
		return rpc.Source{Profile: profile, Production: st.Production}, true
	case errors.Is(err, context.DeadlineExceeded):
		// Alive but serving another request (an approval is pending).
		return rpc.Source{Profile: profile, Busy: true}, true
	}
	return rpc.Source{}, false
}

// helloProfile reads the profile a console serves from its hello answer.
func helloProfile(ctx context.Context, path string) (string, bool) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return "", false
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	req := ipc.Request{JSONRPC: "2.0", ID: 1, Method: ipc.MethodHello}
	req.Params = []byte(fmt.Sprintf(`{"protocol_major":%d,"client":%q}`, ipc.ProtocolMajor, product.P.Name))
	if ipc.WriteMsg(conn, req) != nil {
		return "", false
	}
	var resp struct {
		Result *ipc.HelloResult `json:"result"`
	}
	if ipc.ReadMsg(bufio.NewReader(conn), &resp) != nil || resp.Result == nil {
		return "", false
	}
	return resp.Result.Profile, true
}

// SourceNames lists the profiles of the running consoles (mcpkit.Profiles).
func SourceNames() ([]string, error) {
	src, err := Sources(context.Background())
	if err != nil {
		return nil, err
	}
	names := make([]string, len(src))
	for i, s := range src {
		names[i] = s.Profile
	}
	return names, nil
}

// Resolve picks the profile of a call: pinned (MCP --profile), given, or
// the only running console's. A given profile no console serves is the
// unknown_profile refusal naming its start command.
func Resolve(pinned, arg string) (string, error) {
	if pinned == "" && arg != "" {
		names, err := SourceNames()
		if err != nil {
			return "", err
		}
		if !slices.Contains(names, arg) {
			return "", unknownProfile(arg)
		}
		return arg, nil
	}
	if pinned == "" {
		names, err := SourceNames()
		if err != nil {
			return "", err
		}
		if len(names) == 0 {
			return "", &Error{Kind: KindNoConsole, Msg: "no lockfile console is running: ask the user to start one in the lockfile account (" + StartCommand("NAME") + ")"}
		}
	}
	p, err := mcpkit.Profiles{Product: product.P, Pinned: pinned, List: SourceNames}.Resolve(arg)
	if err != nil {
		return "", &Error{Kind: KindOther, Msg: err.Error()}
	}
	return p, nil
}
