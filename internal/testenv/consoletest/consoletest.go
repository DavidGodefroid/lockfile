// Package consoletest runs a real lockfile console in the test process:
// lockkit's console.Run on lockfile's backend, a local source, plugins
// built from sandbox/testdata, a scripted human and a real socket in the
// test's private runtime directory.
package consoletest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DavidGodefroid/lockkit/client"
	"github.com/DavidGodefroid/lockkit/console"
	"github.com/DavidGodefroid/lockkit/fingerprint"
	"github.com/DavidGodefroid/lockkit/ipc"
	"github.com/DavidGodefroid/lockkit/sysconf"

	"github.com/DavidGodefroid/lockfile/internal/backend"
	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/testenv"
)

// ScriptIO answers the console's prompts from a script and records
// everything the console prints.
type ScriptIO struct {
	mu      sync.Mutex
	answers []string
	out     []string
}

// NewScriptIO answers the prompts with answers, in order.
func NewScriptIO(answers ...string) *ScriptIO { return &ScriptIO{answers: answers} }

func (s *ScriptIO) Println(l string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.out = append(s.out, l)
}

func (s *ScriptIO) Ask(ctx context.Context, prompt string, _ time.Duration) (string, bool) {
	s.mu.Lock()
	s.out = append(s.out, "PROMPT "+prompt)
	if len(s.answers) == 0 {
		s.mu.Unlock()
		return "", false
	}
	a := s.answers[0]
	s.answers = s.answers[1:]
	s.mu.Unlock()
	if d, ok := strings.CutPrefix(a, "sleep "); ok {
		// "sleep 2s y": the human answers y after 2 s.
		dur, rest, _ := strings.Cut(d, " ")
		if t, err := time.ParseDuration(dur); err == nil {
			time.Sleep(t)
		}
		a = rest
	}
	return a, true
}

// Push adds answers to the script.
func (s *ScriptIO) Push(answers ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.answers = append(s.answers, answers...)
}

func (s *ScriptIO) AskSecret(context.Context, string) ([]byte, error) {
	return nil, errors.New("no secret in this test")
}

// Output is everything printed so far, one line each.
func (s *ScriptIO) Output() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.out, "\n")
}

// Lines is the number of lines printed so far.
func (s *ScriptIO) Lines() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.out)
}

// OutputFrom is what was printed from line n on.
func (s *ScriptIO) OutputFrom(n int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.out[n:], "\n")
}

// SameUserIsolation lets a test console run in the test's own account
// (lockkit's AllowSameUser, a test-only switch), with its runtime dir in a
// fresh short directory under /tmp (socket path length).
func SameUserIsolation(t testing.TB) *console.IsolationEnv {
	t.Helper()
	d, err := os.MkdirTemp("/tmp", "lfrun")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	t.Setenv(product.P.RuntimeDirEnv(), d)
	uid := os.Getuid()
	return &console.IsolationEnv{
		LoadSys:        func() (*sysconf.Config, error) { return nil, nil },
		Display:        func() sysconf.Display { return sysconf.Display{Kind: sysconf.DisplayWayland} },
		UID:            func() int { return uid },
		UserName:       func() (string, error) { return "tester", nil },
		LoginUID:       func() (int, bool) { return uid, true },
		TerminalOwner:  func() (int, bool) { return uid, true },
		ClientAllowed:  func(*sysconf.Config, int) bool { return true },
		ClientGID:      func(*sysconf.Config) (int, error) { return os.Getgid(), nil },
		AccountExists:  func(int) bool { return true },
		Relay:          func() string { return "" },
		MachinectlUIDs: func() ([]int, bool) { return nil, true },
		DesktopEnv:     func() bool { return false },
		DisplaySet:     func() bool { return false },
		ParentChain:    func() ([]sysconf.Proc, error) { return nil, nil },
		NewVerifier:    func(string) (fingerprint.Verifier, error) { return &fingerprint.Fake{}, nil },
		AllowSameUser:  true,
	}
}

// Plugin is a test plugin to install: Name is qualified ("test.answer1",
// "acme/test.answer0"); Binary is the sandbox/testdata/plugins directory
// (the name's last dot part when empty).
type Plugin struct {
	Name, Binary, Question string
	TimeoutS               int
}

// Options shape a test console.
type Options struct {
	// Answers script the human: the policy approval first, then each
	// decision.
	Answers []string
	Plugins []Plugin
	// Files are created in the local source, by key.
	Files map[string]string
	// Profile is extra TOML for the profile "samples" (local source,
	// prefix "invoices/", answer_quantum "1s" unless set here).
	Profile string
	// Path replaces the local source's root (a missing one makes every
	// fetch fail).
	Path    string
	Backend func(*backend.Options)
}

// Console is a running test console.
type Console struct {
	IO      *ScriptIO
	Socket  string
	Work    string
	Root    string
	Profile string
	done    chan error
}

// Start runs a console serving profile "samples" until the test ends.
func Start(t testing.TB, o Options) *Console {
	t.Helper()
	iso := SameUserIsolation(t)
	root := t.TempDir()
	files := o.Files
	if files == nil {
		files = map[string]string{"invoices/a.pdf": "%PDF-1.7 synthetic"}
	}
	for k, v := range files {
		p := filepath.Join(root, filepath.FromSlash(k))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(v), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	path := root
	if o.Path != "" {
		path = o.Path
	}
	body := fmt.Sprintf("[profiles.samples]\nsource = \"local\"\npath = %q\nprefix = \"invoices/\"\n", path)
	if !strings.Contains(o.Profile, "answer_quantum") {
		body += "answer_quantum = \"1s\"\n"
	}
	body += o.Profile
	cfg := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	store := testenv.TempStore(t)
	for _, p := range o.Plugins {
		bin := p.Binary
		if bin == "" {
			bin = p.Name[strings.LastIndex(p.Name, ".")+1:]
		}
		q := p.Question
		if q == "" {
			q = "Is it " + p.Name + "?"
		}
		to := p.TimeoutS
		if to == 0 {
			to = 2
		}
		testenv.Install(t, store, p.Name, testenv.BuildPlugin(t, bin), q, to)
	}
	c := &Console{IO: NewScriptIO(o.Answers...), Work: filepath.Join(t.TempDir(), "work"), Root: root, Profile: "samples", done: make(chan error, 1)}
	state := t.TempDir()
	bo := backend.Options{ConfigPath: cfg, Profile: "samples", StateDir: state, PluginDir: store.Dir, WorkDir: c.Work}
	if o.Backend != nil {
		o.Backend(&bo)
	}
	b, err := backend.New(bo)
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		defer b.Close()
		c.done <- console.Run(ctx, product.P, b, console.Options{
			Profile: "samples", IO: c.IO, StateDir: state, Quantum: -1, Sanitizer: backend.Sanitizer(),
			Isolation: iso, Harden: func() error { return nil }, Ready: func(s string) { ready <- s },
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-c.done:
		case <-time.After(10 * time.Second):
			t.Error("console did not stop")
		}
	})
	select {
	case c.Socket = <-ready:
	case err := <-c.done:
		t.Fatalf("console exited before listening: %v\n%s", err, c.IO.Output())
	case <-time.After(30 * time.Second):
		t.Fatalf("no socket:\n%s", c.IO.Output())
	}
	return c
}

// Stop logs out and waits for the console to end.
func (c *Console) Stop(t testing.TB) {
	t.Helper()
	cl, err := client.Dial(product.P, "/", c.Profile)
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	_ = cl.Call(context.Background(), ipc.MethodLogout, nil, nil)
	select {
	case err := <-c.done:
		c.done <- err
	case <-time.After(10 * time.Second):
		t.Fatal("console did not stop")
	}
}
