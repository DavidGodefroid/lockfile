//go:build unix

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// launcherFixture runs scripts/lockfile, the host launcher, with a fake
// docker that records each call, one argument per line, a call ending with
// "--". PATH holds only the tools the launcher needs.
type launcherFixture struct {
	root, bin, project, calls, script, seccomp string
	env                                        []string
}

func newLauncherFixture(t *testing.T) *launcherFixture {
	t.Helper()
	root := t.TempDir()
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	f := &launcherFixture{
		root:    root,
		bin:     filepath.Join(root, "bin"),
		project: filepath.Join(root, "project"),
		calls:   filepath.Join(root, "calls"),
		seccomp: filepath.Join(root, "share", "seccomp.json"),
	}
	for _, d := range []string{f.bin, filepath.Join(f.project, ".git"), filepath.Join(f.project, "sub"), filepath.Dir(f.seccomp)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(f.seccomp, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"sh", "dirname", "id", "cat", "printf", "awk", "sha256sum", "shasum", "tar", "gzip", "install", "mktemp", "rm", "uname", "sed", "tr", "tail", "grep", "cksum", "wc"} {
		if p, err := exec.LookPath(tool); err == nil {
			if err := os.Symlink(p, filepath.Join(f.bin, tool)); err != nil {
				t.Fatal(err)
			}
		}
	}
	// docker inspect prints DOCKER_INSPECT; docker run ... version prints
	// the image's version.
	writeExec(t, filepath.Join(f.bin, "docker"), `#!/bin/sh
for a in "$@"; do printf '%s\n' "$a"; done >> '`+f.calls+`'
echo -- >> '`+f.calls+`'
case "$1" in
inspect) printf '%s\n' "${DOCKER_INSPECT:-}" ;;
run) for a in "$@"; do [ "$a" = version ] && echo "lockfile v9.9.9"; done ;;
esac
exit 0
`)
	launcher, err := os.ReadFile(filepath.Join("..", "..", "scripts", "lockfile"))
	if err != nil {
		t.Fatal(err)
	}
	f.script = filepath.Join(root, "lockfile")
	writeExec(t, f.script, strings.Replace(string(launcher), "\nseccomp_profile=\n", "\nseccomp_profile="+f.seccomp+"\n", 1))
	f.env = []string{"PATH=" + f.bin, "HOME=" + filepath.Join(root, "home")}
	return f
}

// setVersion makes the launcher one a release installed, with the digests
// of its verified images.
func (f *launcherFixture) setVersion(t *testing.T, v string) {
	t.Helper()
	b, err := os.ReadFile(f.script)
	if err != nil {
		t.Fatal(err)
	}
	s := strings.Replace(string(b), "\nlauncher_version=dev\n", "\nlauncher_version="+v+"\n", 1)
	s = strings.Replace(s, "\nimage_digest=\n", "\nimage_digest="+fakeDigest+"\n", 1)
	s = strings.Replace(s, "\nvault_image_digest=\n", "\nvault_image_digest="+fakeVaultDigest+"\n", 1)
	writeExec(t, f.script, s)
}

// run runs the launcher with args from dir, and returns its output and the
// docker calls, each a list of arguments.
func (f *launcherFixture) run(t *testing.T, dir string, env []string, args ...string) (string, [][]string, error) {
	t.Helper()
	_ = os.Remove(f.calls)
	cmd := exec.Command(filepath.Join(f.bin, "sh"), append([]string{f.script}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(append([]string{}, f.env...), env...)
	out, err := cmd.CombinedOutput()
	var calls [][]string
	b, _ := os.ReadFile(f.calls)
	var cur []string
	for _, l := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		if l == "--" {
			calls = append(calls, cur)
			cur = nil
		} else if l != "" {
			cur = append(cur, l)
		}
	}
	return string(out), calls, err
}

// lastRun is the last `docker run` call.
func lastRun(t *testing.T, calls [][]string) []string {
	t.Helper()
	for i := len(calls) - 1; i >= 0; i-- {
		if len(calls[i]) > 0 && calls[i][0] == "run" {
			return calls[i]
		}
	}
	t.Fatalf("no docker run in %q", calls)
	return nil
}

// hasSeq reports whether args hold seq, in order and adjacent.
func hasSeq(args []string, seq ...string) bool {
	for i := 0; i+len(seq) <= len(args); i++ {
		ok := true
		for j, s := range seq {
			if args[i+j] != s {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// imageArgs are the arguments from the image on: the image and its command
// (the account lockfile of --user is not the image lockfile).
func imageArgs(args []string, image string) []string {
	for i, a := range args {
		if a == image && (i == 0 || args[i-1] != "--user") {
			return args[i:]
		}
	}
	return nil
}

// hasMount reports whether args mount anything at all (-v), but the volumes.
func hasBindMount(args []string) bool {
	for i, a := range args {
		if a == "-v" && i+1 < len(args) && strings.HasPrefix(args[i+1], "/") {
			return true
		}
	}
	return false
}

// The console runs as the console account, in its named container, under
// the seccomp profile, confined, with the socket and home volumes, and no
// directory of yours.
func TestLauncherConsole(t *testing.T) {
	f := newLauncherFixture(t)
	out, calls, err := f.run(t, filepath.Join(f.project, "sub"), nil, "console", "--profile", "samples")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	run := lastRun(t, calls)
	for _, seq := range [][]string{
		{"run", "--rm"}, {"--log-driver", "none"}, {"--user", "lockfile"},
		{"--security-opt", "seccomp=" + f.seccomp}, {"--cap-drop=ALL"}, {"--security-opt", "no-new-privileges"},
		{"--read-only"}, {"--tmpfs", "/tmp:rw,nosuid,nodev,noexec,size=64m"}, {"--pids-limit", "512"}, {"--memory", "4g"},
		{"--add-host=host.docker.internal:host-gateway"}, {"-e", "VAULT_ADDR"}, {"-e", "VAULT_NAMESPACE"},
		{"-v", "lockfile-run:/run/lockfile"}, {"-v", "lockfile-home:/home/lockfile"},
	} {
		if !hasSeq(run, seq...) {
			t.Errorf("console run lacks %q:\n%q", seq, run)
		}
	}
	if hasBindMount(run) || hasSeq(run, "-it") {
		t.Errorf("a directory or a terminal: %q", run)
	}
	if got := strings.Join(imageArgs(run, "lockfile"), " "); got != "lockfile console --profile samples" {
		t.Errorf("image and command %q", got)
	}
}

// Each profile gets its own console container, so two can run; a stopped
// one of the same name is removed first; LOCKFILE_CONSOLE_NAME names it.
func TestLauncherConsoleNamePerProfile(t *testing.T) {
	f := newLauncherFixture(t)
	name := func(env []string, args ...string) (string, [][]string) {
		_, calls, err := f.run(t, f.project, env, append([]string{"console"}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		run := lastRun(t, calls)
		for i, a := range run {
			if a == "--name" && i+1 < len(run) {
				return run[i+1], calls
			}
		}
		t.Fatalf("no --name: %q", run)
		return "", nil
	}
	a, _ := name(nil, "--profile", "samples")
	b, _ := name(nil, "--profile=docs")
	if !strings.HasPrefix(a, "lockfile-console-") || !strings.HasPrefix(b, "lockfile-console-") || a == b {
		t.Errorf("names %q and %q", a, b)
	}
	if c, _ := name(nil, "--profile=samples", "--auto"); c != a {
		t.Errorf("--profile=samples: %q, want %q", c, a)
	}
	if got, _ := name([]string{"LOCKFILE_CONSOLE_NAME=lockfile-console"}); got != "lockfile-console" {
		t.Errorf("LOCKFILE_CONSOLE_NAME: %q", got)
	}
	_, calls := name([]string{"DOCKER_INSPECT=false"}, "--profile", "samples")
	removed := false
	for _, c := range calls {
		if hasSeq(c, "rm", a) {
			removed = true
		}
	}
	if !removed {
		t.Errorf("stopped console not removed: %q", calls)
	}
}

// Without its seccomp profile the console does not start: never
// seccomp=unconfined.
func TestLauncherConsoleNeedsTheSeccompProfile(t *testing.T) {
	f := newLauncherFixture(t)
	if err := os.Remove(f.seccomp); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"console"}, {"doctor"}} {
		out, calls, err := f.run(t, f.project, nil, args...)
		if err == nil || !strings.Contains(out, "no seccomp profile") || len(calls) > 1 {
			t.Errorf("%v: %v, calls %q\n%s", args, err, calls, out)
		}
	}
}

// Each command runs with the account and the volumes it needs.
func TestLauncherAccountsPerCommand(t *testing.T) {
	f := newLauncherFixture(t)
	me := strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid())
	home := filepath.Join(f.root, "home")
	run, homeVol := []string{"-v", "lockfile-run:/run/lockfile"}, []string{"-v", "lockfile-home:/home/lockfile"}
	asYou := [][]string{{"--user", me}, {"-e", "HOME=" + home}, {"-v", home + ":" + home}, {"-v", f.project + ":" + f.project}, {"-w", filepath.Join(f.project, "sub")}}
	for _, tc := range []struct {
		args       []string
		want, deny [][]string
		mounts     bool
	}{
		{[]string{"mcp"}, [][]string{{"-i"}, run}, [][]string{{"-it"}, {"--user"}, homeVol}, false},
		{[]string{"status", "--json"}, [][]string{run}, [][]string{{"--user"}, homeVol}, false},
		{[]string{"plan", "--key", "a.pdf", "--plugin", "file.non-empty", "--run"}, [][]string{run}, [][]string{{"--user"}, homeVol}, false},
		{[]string{"doctor"}, [][]string{{"--user", "lockfile"}, {"--security-opt", "seccomp=" + f.seccomp}, run, homeVol}, nil, false},
		{[]string{"doctor", "--fix"}, asYou, [][]string{run, homeVol}, true},
		{[]string{"init", "claude"}, asYou, [][]string{run, homeVol}, true},
		{[]string{"profile", "add", "samples", "--source", "local", "--path", "/srv/lockfile/samples"}, [][]string{{"--user", "lockfile"}, homeVol}, [][]string{run}, false},
		{[]string{"plugin", "install", "file.non-empty@1.0.0"}, [][]string{{"--user", "lockfile"}, homeVol}, [][]string{run}, false},
		{[]string{"marketplace", "list"}, [][]string{{"--user", "lockfile"}, homeVol}, [][]string{run}, false},
		{[]string{"audit", "--since", "24h"}, [][]string{{"--user", "lockfile"}, homeVol}, [][]string{run}, false},
		{[]string{"forget", "--profile", "p"}, [][]string{{"--user", "lockfile"}, homeVol}, [][]string{run}, false},
		{nil, [][]string{run}, [][]string{{"--user"}}, false},
	} {
		out, calls, err := f.run(t, filepath.Join(f.project, "sub"), nil, tc.args...)
		if err != nil {
			t.Fatalf("%v: %v\n%s", tc.args, err, out)
		}
		r := lastRun(t, calls)
		for _, seq := range tc.want {
			if !hasSeq(r, seq...) {
				t.Errorf("%v: lacks %q:\n%q", tc.args, seq, r)
			}
		}
		for _, seq := range tc.deny {
			if hasSeq(r, seq...) {
				t.Errorf("%v: has %q:\n%q", tc.args, seq, r)
			}
		}
		if hasBindMount(r) != tc.mounts {
			t.Errorf("%v: directories mounted %v, want %v: %q", tc.args, hasBindMount(r), tc.mounts, r)
		}
		if got, want := strings.Join(imageArgs(r, "lockfile")[1:], " "), strings.Join(tc.args, " "); got != want {
			t.Errorf("%v: command %q", tc.args, got)
		}
	}
}

// init mounts the project: the nearest directory up holding .lockfile/, or
// else .git, or the directory itself.
func TestLauncherInitProjectRoot(t *testing.T) {
	f := newLauncherFixture(t)
	inner := filepath.Join(f.project, "sub", "inner")
	if err := os.MkdirAll(filepath.Join(inner, ".lockfile"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(f.root, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	for dir, want := range map[string]string{filepath.Join(f.project, "sub"): f.project, inner: inner, outside: outside} {
		_, calls, err := f.run(t, dir, nil, "init")
		if err != nil {
			t.Fatal(err)
		}
		if r := lastRun(t, calls); !hasSeq(r, "-v", want+":"+want, "-w", dir) {
			t.Errorf("from %s: want %s mounted: %q", dir, want, r)
		}
	}
}

// The image: a release's launcher runs its verified digest, with the vault
// variant on request; LOCKFILE_IMAGE overrides it; a clone runs `lockfile`.
func TestLauncherImage(t *testing.T) {
	f := newLauncherFixture(t)
	if _, calls, err := f.run(t, f.project, []string{"LOCKFILE_VAULT=1"}, "status"); err != nil || imageArgs(lastRun(t, calls), "lockfile:vault") == nil {
		t.Errorf("clone vault: %v %q", err, calls)
	}
	f.setVersion(t, "1.2.3")
	for _, tc := range []struct {
		env  []string
		want string
	}{
		{nil, "ghcr.io/davidgodefroid/lockfile@" + fakeDigest},
		{[]string{"LOCKFILE_VAULT=1"}, "ghcr.io/davidgodefroid/lockfile@" + fakeVaultDigest},
		{[]string{"LOCKFILE_IMAGE=my/lockfile:x"}, "my/lockfile:x"},
	} {
		_, calls, err := f.run(t, f.project, tc.env, "console")
		if err != nil {
			t.Fatal(err)
		}
		if imageArgs(lastRun(t, calls), tc.want) == nil {
			t.Errorf("%v: image %q not run: %q", tc.env, tc.want, lastRun(t, calls))
		}
	}
}

// version prints the launcher's version and its image's.
func TestLauncherVersion(t *testing.T) {
	f := newLauncherFixture(t)
	f.setVersion(t, "1.2.3")
	out, calls, err := f.run(t, f.project, nil, "version")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	img := "ghcr.io/davidgodefroid/lockfile@" + fakeDigest
	if !strings.Contains(out, "lockfile launcher 1.2.3 (image "+img+")") || !strings.Contains(out, "lockfile v9.9.9") {
		t.Errorf("output:\n%s", out)
	}
	if got := imageArgs(lastRun(t, calls), img); len(got) != 2 || got[1] != "version" {
		t.Errorf("run %q", lastRun(t, calls))
	}
}

// LOCKFILE_DOCKER_ARGS go before the image, split on spaces, never globbed.
func TestLauncherExtraDockerArgs(t *testing.T) {
	f := newLauncherFixture(t)
	_, calls, err := f.run(t, f.project, []string{"LOCKFILE_DOCKER_ARGS=-v /data/samples:/srv/lockfile/samples:ro --label *"}, "console")
	if err != nil {
		t.Fatal(err)
	}
	run := lastRun(t, calls)
	if !hasSeq(run, "-v", "/data/samples:/srv/lockfile/samples:ro", "--label", "*") || imageArgs(run, "lockfile") == nil {
		t.Errorf("%q", run)
	}
}

// VAULT_CACERT reaches the console with its file, mounted read-only; a
// missing file stops it.
func TestLauncherVaultCACert(t *testing.T) {
	f := newLauncherFixture(t)
	ca := filepath.Join(f.project, "vault-ca.pem")
	if err := os.WriteFile(ca, []byte("pem"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, calls, err := f.run(t, f.project, []string{"VAULT_CACERT=vault-ca.pem"}, "console")
	if err != nil {
		t.Fatal(err)
	}
	if run := lastRun(t, calls); !hasSeq(run, "-v", ca+":"+ca+":ro") || !hasSeq(run, "-e", "VAULT_CACERT="+ca) {
		t.Errorf("%q", run)
	}
	out, calls, err := f.run(t, f.project, []string{"VAULT_CACERT=" + ca + ".missing"}, "console")
	if err == nil || !strings.Contains(out, "VAULT_CACERT") || len(lastRunOrNil(calls)) != 0 {
		t.Errorf("missing file: %v, calls %q\n%s", err, calls, out)
	}
}

func lastRunOrNil(calls [][]string) []string {
	for i := len(calls) - 1; i >= 0; i-- {
		if len(calls[i]) > 0 && calls[i][0] == "run" {
			return calls[i]
		}
	}
	return nil
}

// A path with ":" cannot be a docker -v source or target: refused, before
// docker runs.
func TestLauncherRefusesAColonInAPath(t *testing.T) {
	f := newLauncherFixture(t)
	dir := filepath.Join(f.root, "a:b")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	out, calls, err := f.run(t, dir, nil, "init", "claude")
	if err == nil || !strings.Contains(out, `":"`) || len(calls) != 0 {
		t.Errorf("project: %v, calls %q\n%s", err, calls, out)
	}
	out, calls, err = f.run(t, f.project, []string{"HOME=" + dir}, "init", "claude")
	if err == nil || !strings.Contains(out, `":"`) || len(calls) != 0 {
		t.Errorf("home: %v, calls %q\n%s", err, calls, out)
	}
}

func TestLauncherWithoutDocker(t *testing.T) {
	f := newLauncherFixture(t)
	if err := os.Remove(filepath.Join(f.bin, "docker")); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"status"}, {"console"}} {
		out, _, err := f.run(t, f.project, nil, args...)
		if err == nil || !strings.Contains(out, "docker is required") {
			t.Errorf("%v: %v\n%s", args, err, out)
		}
	}
}

// updateFixture is a launcher of 0.9.0 and a release of shVersion to update
// to, with a fake cosign.
func updateFixture(t *testing.T, blobOK, imageOK bool) (*launcherFixture, *shFixture) {
	t.Helper()
	f := newLauncherFixture(t)
	f.setVersion(t, "0.9.0")
	rel := newShFixture(t)
	rel.cosign(t, blobOK, imageOK)
	for _, tool := range []string{"curl", "cosign"} {
		if err := os.Symlink(filepath.Join(rel.bin, tool), filepath.Join(f.bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	return f, rel
}

// update replaces the launcher and its seccomp profile with a release's,
// after the signature and checksum checks, then pulls that release's image
// by its verified digest.
func TestLauncherUpdate(t *testing.T) {
	f, rel := updateFixture(t, true, true)
	out, calls, err := f.run(t, f.project, nil, "update", "v"+shVersion)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	b, _ := os.ReadFile(f.script)
	for _, want := range []string{"\nlauncher_version=" + shVersion + "\n", "\nimage_digest=" + fakeDigest + "\n", "\nseccomp_profile=" + f.seccomp + "\n"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("launcher lacks %q:\n%s", want, out)
		}
	}
	if p, _ := os.ReadFile(f.seccomp); !strings.Contains(string(p), "SCMP_ACT_ERRNO") {
		t.Errorf("seccomp profile not replaced: %q", p)
	}
	if len(calls) != 1 || !hasSeq(calls[0], "pull", "ghcr.io/davidgodefroid/lockfile@"+fakeDigest) {
		t.Errorf("calls %q", calls)
	}
	args, _ := os.ReadFile(rel.cosignArgs)
	if n := strings.Count(string(args), "--certificate-identity-regexp\n"+shIdentity+"\n"); n != 3 {
		t.Errorf("%d checks bound to v%s, want 3:\n%s", n, shVersion, args)
	}
}

// update refuses a release whose checksums.txt or image does not verify,
// and keeps the launcher.
func TestLauncherUpdateRefusesWhatDoesNotVerify(t *testing.T) {
	for _, tc := range []struct{ blobOK, imageOK bool }{{false, true}, {true, false}} {
		f, _ := updateFixture(t, tc.blobOK, tc.imageOK)
		out, calls, err := f.run(t, f.project, nil, "update", "v"+shVersion)
		if err == nil || len(calls) != 0 {
			t.Errorf("%+v: %v, calls %q\n%s", tc, err, calls, out)
		}
		if b, _ := os.ReadFile(f.script); !strings.Contains(string(b), "\nlauncher_version=0.9.0\n") {
			t.Errorf("%+v: launcher replaced", tc)
		}
	}
}

// update takes only an exact vX.Y.Z: anything else never reaches a URL or
// the sed that writes the launcher.
func TestLauncherUpdateRefusesABadTag(t *testing.T) {
	f, _ := updateFixture(t, true, true)
	for _, tag := range []string{"v1.2", "v1.2.3/x", "v1.2.3;id", "latest", "v1.2.3-rc.1&"} {
		out, calls, err := f.run(t, f.project, nil, "update", tag)
		if err == nil || !strings.Contains(out, "invalid release tag") || len(calls) != 0 {
			t.Errorf("%q: %v, calls %q\n%s", tag, err, calls, out)
		}
	}
}
