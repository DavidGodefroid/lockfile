//go:build docker

// Package docker runs the image of the Dockerfile as docs/docker.md does:
// the console in one container under the shipped seccomp profile, attached
// to a fake lockhub in the test process (hubtest; the console container is
// on the host's network to reach it, as lox up puts it on the hub's), the
// agent's commands in others, sharing only the socket volume. It needs a
// Docker daemon and the image built beforehand:
//
//	docker build -t lockfile:e2e .
//	docker build -t lockfile:e2e-allowed --build-arg ALLOWED_UIDS=4242 .
//	go test -count=1 -tags docker ./test/docker/...
//
// LOCKFILE_IMAGE and LOCKFILE_IMAGE_ALLOWED name other images; DOCKER
// another client (podman).
package docker

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/DavidGodefroid/lockkit/hubclient/hubtest"
	"github.com/DavidGodefroid/lockkit/ticket"

	"github.com/DavidGodefroid/lockfile/internal/plugin"
	"github.com/DavidGodefroid/lockfile/internal/testenv"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

var (
	dockerCLI    = env("DOCKER", "docker")
	image        = env("LOCKFILE_IMAGE", "lockfile:e2e")
	imageAllowed = env("LOCKFILE_IMAGE_ALLOWED", "lockfile:e2e-allowed")
)

// dk runs the Docker client and returns its stdout and stderr.
func dk(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	var out, errb bytes.Buffer
	cmd := exec.Command(dockerCLI, args...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	return out.String(), errb.String(), err
}

func mustDk(t *testing.T, args ...string) string {
	t.Helper()
	out, errs, err := dk(t, args...)
	if err != nil {
		t.Fatalf("%s %s: %v\n%s%s", dockerCLI, strings.Join(args, " "), err, out, errs)
	}
	return out
}

// setup is one deployment: its volumes, the local source, the profile and
// the hub the console is attached to.
type setup struct {
	run, home, data, seccomp string
	terms                    []*syncBuffer
	// consoleArgs go after the console's own arguments.
	consoleArgs []string
	// hub decides the plans; hubDir holds its pinned key (hub.pub).
	hub    *hubtest.Hub
	hubDir string
}

// hubInstance is the lock instance name the hub knows the console by.
const hubInstance = "files"

// syncBuffer is what script(1) wrote, read while it writes.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// seccompProfile is the shipped profile, absolute.
func seccompProfile(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join(testenv.RepoRoot(), "seccomp.json"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// newSetup creates the volumes, seeds the console's plugin store with the
// test plugins (the official marketplace is reached in another test: these
// need no network) and adds the profile "samples" on a mounted directory.
func newSetup(t *testing.T) *setup {
	t.Helper()
	if _, _, err := dk(t, "image", "inspect", image); err != nil {
		t.Fatalf("no image %s: docker build -t %s .", image, image)
	}
	id := fmt.Sprintf("%d", time.Now().UnixNano())
	s := &setup{run: "lockfile-e2e-run-" + id, home: "lockfile-e2e-home-" + id, seccomp: seccompProfile(t)}
	for _, v := range []string{s.run, s.home} {
		mustDk(t, "volume", "create", v)
	}
	t.Cleanup(func() { dk(t, "volume", "rm", "-f", s.run, s.home) })

	seed := t.TempDir()
	store := plugin.Store{Dir: filepath.Join(seed, "lockfile")}
	for name, bin := range map[string]string{"test.answer1": "answer1", "test.readpasswd": "readpasswd", "test.dial": "dial"} {
		testenv.Install(t, store, name, testenv.BuildPlugin(t, bin), "does "+name+" pass?", 10)
	}
	openUp(t, seed)
	mustDk(t, "run", "--rm", "--user", "lockfile", "-v", s.home+":/home/lockfile", "-v", seed+":/seed:ro",
		"--entrypoint", "sh", image, "-euc",
		"mkdir -p ~/.local/state && cp -R /seed/lockfile ~/.local/state/ && chmod -R go-rwx ~/.local/state")

	s.data = t.TempDir()
	if err := os.WriteFile(filepath.Join(s.data, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	openUp(t, s.data)
	mustDk(t, "run", "--rm", "--user", "lockfile", "-v", s.home+":/home/lockfile", image,
		"profile", "add", "samples", "--source", "local", "--path", "/srv/lockfile/samples")

	// The hub pins the instance key the console account made (hub-key, as
	// lox up runs it); the console pins the hub's key.
	pubText := mustDk(t, "run", "--rm", "--user", "lockfile", "-v", s.home+":/home/lockfile", image, "hub-key", "--instance", hubInstance)
	pub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(pubText))
	if err != nil || len(pub) != ed25519.PublicKeySize {
		t.Fatalf("hub-key printed %q: %v", pubText, err)
	}
	s.hub = hubtest.New(t, hubInstance, ed25519.PublicKey(pub))
	s.hubDir = t.TempDir()
	if err := os.WriteFile(filepath.Join(s.hubDir, "hub.pub"), []byte(base64.StdEncoding.EncodeToString(s.hub.Pub)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	openUp(t, s.hubDir)
	return s
}

// openUp lets the container's accounts read dir (a test directory of the
// host account).
func openUp(t *testing.T, dir string) {
	t.Helper()
	err := filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if fi.IsDir() || fi.Mode()&0o100 != 0 {
			mode = 0o755
		}
		return os.Chmod(p, mode)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// console starts the console container with extra docker run options and
// returns its name; it is removed at the end of the test. The console needs
// a terminal: docker run -it runs on a pty, which approves the policy once
// its prompt shows (the console checks the sandbox after that).
func (s *setup) console(t *testing.T, img string, opts ...string) string {
	t.Helper()
	name := fmt.Sprintf("lockfile-e2e-console-%d", time.Now().UnixNano())
	// As docs/docker.md runs it: no capability, no privilege gain.
	args := []string{dockerCLI, "run", "-it", "--rm", "--name", name, "--user", "lockfile",
		"--cap-drop=ALL", "--security-opt", "no-new-privileges",
		"-v", s.run + ":/run/lockfile", "-v", s.home + ":/home/lockfile",
		"-v", s.data + ":/srv/lockfile/samples:ro",
		"--network", "host", "-v", s.hubDir + ":/run/lockfile-hub:ro"}
	args = append(args, opts...)
	args = append(args, img, "console", "--profile", "samples",
		"--hub", s.hub.URL(), "--hub-key-file", "/run/lockfile-hub/hub.pub", "--instance", hubInstance)
	args = append(args, s.consoleArgs...)
	ptm, pts := openPTY(t)
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = pts, pts, pts
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pts.Close()
	term := &syncBuffer{}
	go io.Copy(term, ptm)
	exited := make(chan struct{})
	go func() { cmd.Wait(); close(exited) }()
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("console terminal:\n%s", term.String())
		}
		dk(t, "rm", "-f", name)
		<-exited
		ptm.Close()
	})
	s.terms = append(s.terms, term)
	deadline := time.Now().Add(60 * time.Second)
	for !strings.Contains(term.String(), "Apply these changes?") {
		select {
		case <-exited:
			t.Fatalf("the console stopped before its policy prompt:\n%s", term.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("no policy prompt:\n%s", term.String())
		}
		time.Sleep(200 * time.Millisecond)
	}
	if _, err := ptm.Write([]byte("y\r")); err != nil {
		t.Fatal(err)
	}
	return name
}

// agent runs one agent command in a throwaway container of img, as the
// image's agent account unless opts say otherwise.
func (s *setup) agent(t *testing.T, img string, opts []string, args ...string) (string, error) {
	t.Helper()
	a := append([]string{"run", "--rm", "-v", s.run + ":/run/lockfile"}, opts...)
	a = append(a, img)
	out, errs, err := dk(t, append(a, args...)...)
	return out + errs, err
}

// waitServed waits until an agent container lists the profile.
func (s *setup) waitServed(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		out, err := s.agent(t, image, nil, "sources", "--json")
		if err == nil && strings.Contains(out, "samples") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the console never served samples: %v\n%s", err, out)
		}
		time.Sleep(time.Second)
	}
}

// waitText waits until the last console's terminal shows want.
func (s *setup) waitText(t *testing.T, want string) {
	t.Helper()
	term := s.terms[len(s.terms)-1]
	deadline := time.Now().Add(60 * time.Second)
	for !strings.Contains(term.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("the console never printed %q", want)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// answers plans plugins on a.txt from an agent container of img (opts:
// docker run options), approves the plan on the hub, runs its ticket and
// returns the results by plugin; ok is false when the plan or the run gave
// none.
func (s *setup) answers(t *testing.T, img string, opts []string, plugins ...string) (got map[string]string, out string, ok bool) {
	t.Helper()
	args := []string{"plan", "--profile", "samples", "--key", "a.txt", "--json"}
	for _, p := range plugins {
		args = append(args, "--plugin", p)
	}
	out, err := s.agent(t, img, opts, args...)
	var pending struct{ Status, Ticket string }
	if err != nil || json.Unmarshal([]byte(out), &pending) != nil || pending.Status != "pending" || !ticket.ValidID(pending.Ticket) {
		return nil, out, false
	}
	s.hub.Decide(pending.Ticket, ticket.Approved, "")
	for deadline := time.Now().Add(60 * time.Second); ; {
		out, err = s.agent(t, img, opts, "run", pending.Ticket, "--profile", "samples", "--json")
		var p struct{ Status string }
		if err != nil || json.Unmarshal([]byte(out), &p) != nil || p.Status != "pending" {
			break
		}
		if time.Now().After(deadline) {
			return nil, out, false
		}
		time.Sleep(time.Second)
	}
	var v struct {
		Answers []struct {
			Plugin string `json:"plugin"`
			Result *int   `json:"result"`
		} `json:"answers"`
	}
	if err != nil || json.Unmarshal([]byte(out), &v) != nil || len(v.Answers) == 0 {
		return nil, out, false
	}
	got = map[string]string{}
	for _, a := range v.Answers {
		got[a.Plugin] = "none"
		if a.Result != nil {
			got[a.Plugin] = fmt.Sprint(*a.Result)
		}
	}
	return got, out, true
}

// The image has no sudo and runs no shell by default.
func TestImageDefaults(t *testing.T) {
	out := mustDk(t, "image", "inspect", "-f", "{{json .Config.Entrypoint}} {{json .Config.Cmd}} {{.Config.User}}", image)
	if strings.TrimSpace(out) != `["lockfile"] ["help"] agent` {
		t.Fatalf("entrypoint, cmd, user = %s", out)
	}
	if out, _, err := dk(t, "run", "--rm", "--entrypoint", "sh", image, "-c", "command -v sudo su-exec doas || true"); err != nil || strings.TrimSpace(out) != "" {
		t.Fatalf("privilege tools in the image: %q %v", out, err)
	}
	// lockfile's own account ids, distinct from the other Locks'.
	out = mustDk(t, "run", "--rm", "--entrypoint", "sh", image, "-c", "id -u lockfile; id -g lockfile; id -u agent; id -g agent; getent group lockfile-clients")
	if want := "10031\n10031\n10032\n10030\nlockfile-clients:x:10030:agent\n"; out != want {
		t.Errorf("accounts (uid, gid of lockfile and agent; lockfile-clients):\n%s\nwant:\n%s", out, want)
	}
	out = mustDk(t, "run", "--rm", "--entrypoint", "stat", image, "-c", "%n %U:%G %a",
		"/usr/local/bin/lockfile", "/usr/local/bin/cosign", "/etc/lockfile/system.toml", "/etc/lockfile/container.toml",
		"/run/lockfile", "/home/lockfile", "/srv/lockfile")
	for _, want := range []string{
		"/usr/local/bin/lockfile root:root 755", "/usr/local/bin/cosign root:root 755",
		"/etc/lockfile/system.toml root:root 644", "/etc/lockfile/container.toml root:root 644",
		"/run/lockfile lockfile:lockfile-clients 2750", "/home/lockfile lockfile:lockfile 700",
		"/srv/lockfile lockfile:lockfile 700",
	} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
}

// The documented deployment: the console under the shipped profile runs
// the plugins sandboxed, the agent's container gets the answers, and a
// peer that is neither in lockfile-clients nor allowed is refused.
func TestConsoleAndAgent(t *testing.T) {
	s := newSetup(t)
	c := s.console(t, image, "--security-opt", "seccomp="+s.seccomp)
	s.waitServed(t)

	got, out, ok := s.answers(t, image, nil, "test.answer1", "test.readpasswd", "test.dial")
	// readpasswd and dial answer 0 only when Landlock and seccomp stopped
	// them: the sandbox is active.
	if !ok || got["test.answer1"] != "1" || got["test.readpasswd"] != "0" || got["test.dial"] != "0" {
		t.Fatalf("answers = %v, want answer1 1, readpasswd 0, dial 0:\n%s", got, out)
	}

	out, _, err := dk(t, "exec", "--user", "lockfile", c, "lockfile", "doctor")
	if !strings.Contains(out, "Landlock ABI") || !strings.Contains(out, "container: private pid namespace") {
		t.Fatalf("doctor in the console container (%v):\n%s", err, out)
	}

	// uid 4242 reaches the socket through the group's gid, but the console
	// checks the account: not a member of lockfile-clients, not allowed.
	if got, out, ok := s.answers(t, image, []string{"--user", "4242:10030"}, "test.answer1"); ok {
		t.Fatalf("uid 4242 got answers %v:\n%s", got, out)
	}
}

// The console as scripts/lockfile starts it, confined further: a read-only
// root with a small /tmp, bounded processes and memory. The sandbox still
// runs the plugins.
func TestConsoleConfinedAsTheLauncherRunsIt(t *testing.T) {
	s := newSetup(t)
	s.console(t, image, "--security-opt", "seccomp="+s.seccomp, "--log-driver", "none",
		"--read-only", "--tmpfs", "/tmp:rw,nosuid,nodev,noexec,size=64m", "--pids-limit", "512", "--memory", "4g")
	s.waitServed(t)
	got, out, ok := s.answers(t, image, nil, "test.answer1", "test.readpasswd", "test.dial")
	if !ok || got["test.answer1"] != "1" || got["test.readpasswd"] != "0" || got["test.dial"] != "0" {
		t.Fatalf("answers = %v, want answer1 1, readpasswd 0, dial 0:\n%s", got, out)
	}
}

// ALLOWED_UIDS lets the uid of an agent in its own container through, and
// only that image.
func TestAllowedUIDs(t *testing.T) {
	if _, _, err := dk(t, "image", "inspect", imageAllowed); err != nil {
		t.Skipf("no image %s (docker build -t %s --build-arg ALLOWED_UIDS=4242 .)", imageAllowed, imageAllowed)
	}
	s := newSetup(t)
	s.console(t, imageAllowed, "--security-opt", "seccomp="+s.seccomp)
	s.waitServed(t)
	if got, out, ok := s.answers(t, imageAllowed, []string{"--user", "4242:10030"}, "test.answer1"); !ok || got["test.answer1"] != "1" {
		t.Fatalf("allowed uid 4242: %v\n%s", got, out)
	}
}

// --allowed-uids lets the uid of an agent in its own container through
// with the release image as it is (no allowed_uids in it), for that
// console only: the uid is refused without it, and no other uid is let in.
func TestAllowedUIDsAtRunTime(t *testing.T) {
	s := newSetup(t)
	s.console(t, image, "--security-opt", "seccomp="+s.seccomp)
	s.waitServed(t)
	if got, out, ok := s.answers(t, image, []string{"--user", "4242:10030"}, "test.answer1"); ok {
		t.Fatalf("uid 4242 served without --allowed-uids: %v\n%s", got, out)
	}
	// A console of its own (volumes, policy prompt) with the flag.
	s = newSetup(t)
	s.consoleArgs = []string{"--allowed-uids", "4242"}
	s.console(t, image, "--security-opt", "seccomp="+s.seccomp)
	s.waitServed(t)
	s.waitText(t, "allowed uids besides lockfile-clients (--allowed-uids): 4242")
	if got, out, ok := s.answers(t, image, []string{"--user", "4242:10030"}, "test.answer1"); !ok || got["test.answer1"] != "1" {
		t.Fatalf("--allowed-uids 4242: %v\n%s", got, out)
	}
	if got, out, ok := s.answers(t, image, []string{"--user", "4243:10030"}, "test.answer1"); ok {
		t.Fatalf("uid 4243 served with --allowed-uids 4242: %v\n%s", got, out)
	}
}

// Without Landlock the console runs no plugin: there is no unsandboxed
// fallback.
func TestNoLandlockRefuses(t *testing.T) {
	s := newSetup(t)
	raw, err := os.ReadFile(s.seccomp)
	if err != nil {
		t.Fatal(err)
	}
	var prof map[string]any
	if err := json.Unmarshal(raw, &prof); err != nil {
		t.Fatal(err)
	}
	removed := 0
	for _, r := range prof["syscalls"].([]any) {
		rule := r.(map[string]any)
		var keep []any
		for _, n := range rule["names"].([]any) {
			if strings.HasPrefix(n.(string), "landlock_") {
				removed++
				continue
			}
			keep = append(keep, n)
		}
		rule["names"] = keep
	}
	if removed != 3 {
		t.Fatalf("removed %d landlock calls from the profile, want 3", removed)
	}
	b, _ := json.Marshal(prof)
	noLL := filepath.Join(t.TempDir(), "no-landlock.json")
	if err := os.WriteFile(noLL, b, 0o644); err != nil {
		t.Fatal(err)
	}
	s.console(t, image, "--security-opt", "seccomp="+noLL)
	s.waitText(t, "the plugin sandbox is unavailable")
	out, _, _ := dk(t, "run", "--rm", "--user", "lockfile", "--security-opt", "seccomp="+noLL, image, "doctor")
	if !strings.Contains(out, "plugins cannot run here") {
		t.Fatalf("doctor without Landlock:\n%s", out)
	}
}

// A console in the host's pid namespace is refused: the host's accounts
// could read the plugins' /proc entries.
func TestHostPidNamespaceRefuses(t *testing.T) {
	s := newSetup(t)
	s.console(t, image, "--security-opt", "seccomp="+s.seccomp, "--pid=host")
	s.waitText(t, "host's pid namespace")
}

// openPTY opens a pseudo-terminal pair.
func openPTY(t *testing.T) (ptm, pts *os.File) {
	t.Helper()
	ptm, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.IoctlSetPointerInt(int(ptm.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	n, err := unix.IoctlGetInt(int(ptm.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	pts, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.IoctlSetWinsize(int(pts.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 40, Col: 120}); err != nil {
		t.Fatal(err)
	}
	return ptm, pts
}

// seccomp=unconfined lifts Docker's limits on the console itself: refused,
// although Landlock and the plugins' own filter would still work.
func TestUnconfinedRefuses(t *testing.T) {
	s := newSetup(t)
	s.console(t, image, "--security-opt", "seccomp=unconfined")
	s.waitText(t, "without a seccomp filter")
}

// An agent container that joins the console's pid namespace could read the
// plugins' /proc entries: its plan is refused.
func TestSharedPidNamespaceRefuses(t *testing.T) {
	s := newSetup(t)
	c := s.console(t, image, "--security-opt", "seccomp="+s.seccomp)
	s.waitServed(t)
	got, out, ok := s.answers(t, image, []string{"--pid=container:" + c}, "test.answer1")
	if ok || !strings.Contains(out, "proc_not_hidden") {
		t.Fatalf("agent in the console's pid namespace: %v\n%s", got, out)
	}
	// The console still serves an agent in a container of its own.
	if got, out, ok := s.answers(t, image, nil, "test.answer1"); !ok || got["test.answer1"] != "1" {
		t.Fatalf("agent after the refusal: %v\n%s", got, out)
	}
}

// migrateIDs runs migrate-ids as scripts/lockfile does before the commands
// that mount the volumes, and returns its stderr.
func (s *setup) migrateIDs(t *testing.T) string {
	t.Helper()
	_, errs, err := dk(t, "run", "--rm", "--security-opt", "seccomp="+s.seccomp, "--cap-drop=ALL",
		"--cap-add=CHOWN", "--cap-add=DAC_READ_SEARCH", "--security-opt", "no-new-privileges",
		"--read-only", "--network", "none", "--user", "0:0",
		"-v", s.run+":/run/lockfile", "-v", s.home+":/home/lockfile", image, "migrate-ids")
	if err != nil {
		t.Fatalf("migrate-ids: %v\n%s", err, errs)
	}
	return errs
}

// Volumes made by a release before 0.3, with locksql's account ids
// (10000-10002), stay readable: migrate-ids gives their files lockfile's
// own ids, once, and the console and the agent work on them.
func TestUpgradeFromTheOldAccountIDs(t *testing.T) {
	s := newSetup(t)
	// Back to the old ids, as the previous image left them.
	mustDk(t, "run", "--rm", "--user", "0:0", "-v", s.run+":/run/lockfile", "-v", s.home+":/home/lockfile",
		"--entrypoint", "sh", image, "-euc", `find /run/lockfile /home/lockfile | while read -r p; do
u=$(stat -c %u "$p"); g=$(stat -c %g "$p")
case $u in 10031) u=10001 ;; 10032) u=10002 ;; esac
case $g in 10030) g=10000 ;; 10031) g=10001 ;; 10032) g=10002 ;; esac
chown -h "$u:$g" "$p"
done`)
	old := mustDk(t, "run", "--rm", "--user", "0:0", "-v", s.home+":/home/lockfile", "--entrypoint", "stat", image,
		"-c", "%u:%g", "/home/lockfile")
	if strings.TrimSpace(old) != "10001:10001" {
		t.Fatalf("old home owner = %q", old)
	}

	if errs := s.migrateIDs(t); !strings.Contains(errs, "moved to the account ids 10030-10032") {
		t.Fatalf("first migrate-ids said %q", errs)
	}
	if errs := s.migrateIDs(t); errs != "" {
		t.Fatalf("second migrate-ids said %q, want nothing", errs)
	}
	out := mustDk(t, "run", "--rm", "--user", "0:0", "-v", s.run+":/run/lockfile", "-v", s.home+":/home/lockfile",
		"--entrypoint", "sh", image, "-c", "find /run/lockfile /home/lockfile -exec stat -c '%u:%g %n' {} + | grep -E '^1000[0-2]:|:1000[0-2] ' || true")
	if strings.TrimSpace(out) != "" {
		t.Fatalf("files left with an old id:\n%s", out)
	}

	s.console(t, image, "--security-opt", "seccomp="+s.seccomp)
	s.waitServed(t)
	if got, out, ok := s.answers(t, image, nil, "test.answer1"); !ok || got["test.answer1"] != "1" {
		t.Fatalf("answers after the upgrade = %v:\n%s", got, out)
	}
}
