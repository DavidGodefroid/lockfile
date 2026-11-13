package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/DavidGodefroid/lockkit/agentwire"
	"github.com/DavidGodefroid/lockkit/console"
	"github.com/DavidGodefroid/lockkit/managed"
	"github.com/DavidGodefroid/lockkit/sysconf"
	"github.com/DavidGodefroid/lockkit/verbs"

	"github.com/DavidGodefroid/lockfile/internal/procmount"
	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/profile"
	"github.com/DavidGodefroid/lockfile/internal/wiring"
	"github.com/DavidGodefroid/lockfile/sandbox"
)

func init() {
	commands["doctor"] = command{human: true, help: "check the setup from this account: sandbox, separation, agent wiring [--fix [--yes]] [--json]", run: runDoctor}
}

// doctorHooks replace the system in tests.
type doctorHooks struct {
	probe    func() (int, bool, error)
	sys      func() (*sysconf.Config, error)
	uid      func() int
	home     func(account string) (string, error)
	lookPath func(string) (string, error)
	agents   func() (agentwire.Env, error)
	// mounts reads this process's mount table (/proc/self/mountinfo).
	mounts func() ([]byte, error)
	// owner returns a file's owner uid and gid.
	owner func(fs.FileInfo) (uid, gid int, ok bool)
	// container is the container stand-in for hidepid (procmount).
	container func() procmount.Container
}

var testDoctorHooks *doctorHooks

// finding is one doctor line, as lockkit's verbs reports it. Detail never
// holds a key, a secret or a path inside the console account's home.
type finding = verbs.Check

const doctorUsage = "doctor [--fix [--yes]] [--json]"

// runDoctor is `lockfile doctor`, the launcher verb of lockkit's GUIDE.md
// §14: it checks the setup from this account; --fix wires the agents not
// wired yet (the only repair it makes), asking before each unless --yes.
func runDoctor(e env, args []string) int {
	jsonWanted := verbs.WantsJSON(args)
	refuse := func(err error) int {
		if jsonWanted {
			if werr := verbs.WriteJSON(e.stdout, verbs.Error{Lock: product.P.Name, Code: verbs.CodeUsage, Error: err.Error()}); werr != nil {
				return product.ExitError
			}
			return product.ExitUsage
		}
		fmt.Fprintln(e.stderr, "lockfile doctor:", err)
		fmt.Fprintln(e.stderr, "usage: lockfile "+doctorUsage)
		return product.ExitUsage
	}
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fix := fs.Bool(verbs.FlagFix, false, "wire the installed agents that are not wired yet (nothing else)")
	yes := fs.Bool(verbs.FlagYes, false, "with --fix: repair without asking")
	asJSON := fs.Bool(verbs.FlagJSON, false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return refuse(errors.New("bad or unknown flag"))
	}
	if fs.NArg() != 0 {
		return refuse(errors.New("doctor takes no argument"))
	}
	jsonWanted = *asJSON
	if *yes && !*fix {
		return refuse(fmt.Errorf("--%s goes with --%s", verbs.FlagYes, verbs.FlagFix))
	}
	if *fix && !*yes && !isTerminal(e.stdin) {
		return refuse(fmt.Errorf("--%s asks before each repair: run it in a terminal, or add --%s", verbs.FlagFix, verbs.FlagYes))
	}
	in := bufio.NewReader(e.stdin)
	ask := func(prompt string) bool {
		if *yes {
			return true
		}
		// The question goes to stderr: with --json, stdout carries the
		// JSON line only.
		fmt.Fprint(e.stderr, prompt+" [y/N] ")
		line, err := in.ReadString('\n')
		if err != nil && line == "" {
			fmt.Fprintln(e.stderr)
			return false
		}
		line = strings.TrimSpace(line)
		return strings.EqualFold(line, "y") || strings.EqualFold(line, "yes")
	}
	h := doctorHooks{}
	if testDoctorHooks != nil {
		h = *testDoctorHooks
	}
	if h.probe == nil {
		h.probe = sandbox.Probe
	}
	if h.sys == nil {
		h.sys = func() (*sysconf.Config, error) { return sysconf.Load(product.P) }
	}
	if h.uid == nil {
		h.uid = os.Getuid
	}
	if h.home == nil {
		h.home = func(account string) (string, error) {
			u, err := user.Lookup(account)
			if err != nil {
				return "", err
			}
			return u.HomeDir, nil
		}
	}
	if h.lookPath == nil {
		h.lookPath = exec.LookPath
	}
	if h.agents == nil {
		h.agents = agentwire.SystemEnv
	}
	if h.mounts == nil {
		h.mounts = procmount.ReadMountinfo
	}
	if h.owner == nil {
		h.owner = fileOwner
	}
	if h.container == nil {
		h.container = procmount.SystemContainer
	}
	var out []finding
	var fixed []string
	add := func(check, status, detail string) {
		out = append(out, finding{Check: check, Status: status, Detail: detail})
	}

	if abi, sec, err := h.probe(); err != nil {
		add("sandbox", "fail", fmt.Sprintf("plugins cannot run here (Landlock ABI %d, seccomp %t): Linux ≥ 5.13 with Landlock and seccomp is required", abi, sec))
	} else {
		add("sandbox", "ok", fmt.Sprintf("Landlock ABI %d, seccomp", abi))
	}

	if runtime.GOOS == "linux" {
		doctorProc(h, add)
	}

	sys, err := h.sys()
	service := false
	switch {
	case err != nil:
		add("separation", "fail", "/etc/lockfile/system.toml is unreadable or invalid: "+err.Error())
	case sys == nil:
		add("separation", "fail", "no /etc/lockfile/system.toml: the console refuses to start; "+imageHint)
	default:
		service = doctorSeparation(h, sys, add)
	}

	if _, err := h.lookPath("cosign"); err != nil {
		add("cosign", "warn", "cosign is not on PATH: plugin installs and updates are refused until it is")
	} else {
		add("cosign", "ok", "on PATH")
	}

	if service || sys == nil {
		doctorConsoleSide(h, sys, add)
	}

	if !service {
		fixed = doctorWiring(h, *fix, ask, func(c finding) { out = append(out, c) })
	}

	report := verbs.NewDoctor(product.P, out, fixed)
	if *asJSON {
		if err := verbs.WriteJSON(e.stdout, report); err != nil {
			return product.ExitError
		}
	} else {
		for _, f := range report.Fixed {
			fmt.Fprintf(e.stdout, "fixed %s\n", f)
		}
		for _, f := range report.Checks {
			mark := "ok  "
			switch f.Status {
			case verbs.StatusWarn:
				mark = yellow("warn")
			case verbs.StatusFail:
				mark = red("FAIL")
			}
			line := fmt.Sprintf("%s  %-12s %s", mark, f.Check, f.Detail)
			if f.Fix != "" {
				line += " (" + f.Fix + ")"
			}
			fmt.Fprintln(e.stdout, line)
		}
	}
	if !report.OK {
		return product.ExitError
	}
	return product.ExitOK
}

// imageHint is the fix for a separation check that fails.
const imageHint = "run lockfile through the launcher, which runs it in its image (scripts/install.sh, docs/docker.md)"

// doctorSeparation checks the separated setup; it reports whether this
// is the console account.
//
// The image holds the separation: a failure means doctor runs outside it,
// or in an image or with volumes not set up as the launcher runs them.
func doctorSeparation(h doctorHooks, sys *sysconf.Config, add func(string, string, string)) bool {
	svcUID, err := sys.ServiceUID()
	if err != nil {
		add("separation", "fail", "the console account "+sys.ServiceUser+" does not exist: "+imageHint)
		return false
	}
	if _, err := user.LookupGroup(sys.ClientGroup); err != nil {
		add("separation", "fail", "the group "+sys.ClientGroup+" does not exist: "+imageHint)
	} else {
		add("separation", "ok", fmt.Sprintf("console account %s, clients group %s", sys.ServiceUser, sys.ClientGroup))
	}
	if fi, err := os.Stat(sys.SocketDir); err != nil {
		add("socket dir", "fail", sys.SocketDir+" is missing: "+imageHint)
	} else if fi.Mode()&fs.ModeSetgid == 0 || fi.Mode().Perm() != 0o750 {
		add("socket dir", "fail", fmt.Sprintf("%s has mode %04o, want 2750 (the clients group lists the running consoles): %s", sys.SocketDir, fi.Mode().Perm()|setgidBits(fi), imageHint))
	} else {
		add("socket dir", "ok", sys.SocketDir+" 2750")
	}
	if fi, err := os.Stat(sys.SocketDir); err == nil {
		g, gerr := user.LookupGroup(sys.ClientGroup)
		uid, gid, ok := h.owner(fi)
		switch {
		case !ok || gerr != nil:
			add("socket dir", "warn", "cannot check the owner of "+sys.SocketDir)
		case uid != svcUID || strconv.Itoa(gid) != g.Gid:
			add("socket dir", "fail", fmt.Sprintf("%s is owned by %d:%d, want %s:%s (another owner could list or replace the consoles' sockets): %s", sys.SocketDir, uid, gid, sys.ServiceUser, sys.ClientGroup, imageHint))
		default:
			add("socket dir", "ok", "owned by "+sys.ServiceUser+":"+sys.ClientGroup)
		}
	}
	uid := h.uid()
	if uid == svcUID {
		return true
	}
	_, noAccount := user.LookupId(strconv.Itoa(uid))
	switch {
	case !sys.ClientAllowed(uid) && noAccount != nil:
		// An agent in a container of its own: a console started with
		// --allowed-uids serves it, which this side cannot see.
		add("clients group", "warn", fmt.Sprintf("uid %d has no account here: a console serves it only when started with --allowed-uids %d (docs/docker.md#agent-in-its-own-container)", uid, uid))
	case !sys.ClientAllowed(uid):
		add("clients group", "fail", "this account is not in "+sys.ClientGroup+": "+imageHint)
	default:
		add("clients group", "ok", "this account may reach the consoles")
	}
	home, err := h.home(sys.ServiceUser)
	if err != nil {
		add("console home", "fail", "cannot find the console account's home")
		return false
	}
	f, err := os.Open(home)
	switch {
	case err == nil:
		f.Close()
		add("console home", "fail", "this account can open "+home+": the agent could read fetched files and plugins; chmod 0700 it as root")
	case errors.Is(err, syscall.EACCES):
		add("console home", "ok", "closed to this account")
	default:
		add("console home", "warn", "cannot check "+home)
	}
	return false
}

// doctorProc requires /proc to hide other accounts' processes: a running
// plugin's /proc entries are a channel to the agent otherwise. Checked
// from both accounts: each sees its own mount namespace.
func doctorProc(h doctorHooks, add func(string, string, string)) {
	c := h.container()
	switch err := procmount.CheckWith(h.mounts, c); {
	case err == nil && c.Declared() && procmount.Check(h.mounts) != nil:
		add("proc", "ok", "container: private pid namespace, no other account's process (hidepid is not available in Docker)")
	case err == nil:
		add("proc", "ok", "hidepid=invisible: other accounts' processes are hidden")
	case errors.Is(err, procmount.ErrContainer):
		add("proc", "fail", "other accounts could read a running plugin's /proc entries; consoles refuse to run plugins: "+err.Error())
	case errors.Is(err, procmount.ErrNotHidden), errors.Is(err, procmount.ErrNoProc):
		add("proc", "fail", "other accounts can read a running plugin's /proc entries (a covert channel to the agent); consoles refuse to run plugins: "+procmount.Fix)
	default:
		add("proc", "fail", "cannot read the mount table to check /proc hidepid: "+procmount.Fix)
	}
}

func setgidBits(fi fs.FileInfo) fs.FileMode {
	if fi.Mode()&fs.ModeSetgid != 0 {
		return 0o2000
	}
	return 0
}

// doctorConsoleSide checks the console account's own files: the work
// directory (mode, parents, mount), the local sources' directories and
// the unofficial marketplaces. Paths are not printed: they may sit in the
// console's home or name the data.
func doctorConsoleSide(h doctorHooks, sys *sysconf.Config, add func(string, string, string)) {
	add(doctorManaged())
	path, err := configPath()
	if err != nil {
		return
	}
	f, err := profile.ReadFile(path)
	switch {
	case errors.Is(err, console.ErrManaged):
		add("config", "fail", "the config file is refused by the company's managed configuration: lockfile managed check names why")
		return
	case errors.Is(err, fs.ErrNotExist):
		add("config", "warn", "no profile yet: lockfile profile add --profile NAME --source … from the workspace, then lox run")
		return
	case err != nil:
		add("config", "fail", "the config file does not load: the console names the error when `lox run` starts it")
		return
	}
	add("config", "ok", fmt.Sprintf("%d profile(s)", len(f.Profiles)))
	work := f.Workspace.Dir
	if work == "" {
		if _, home, err := stateHome(); err == nil {
			work = filepath.Join(home, "work")
		}
	}
	table, _ := h.mounts()
	for _, f := range workDirFindings(work, string(table)) {
		add("work dir", f.Status, f.Detail)
	}
	clientGID := -1
	if sys != nil {
		if g, err := user.LookupGroup(sys.ClientGroup); err == nil {
			clientGID, _ = strconv.Atoi(g.Gid)
		}
	}
	names := make([]string, 0, len(f.Profiles))
	for name := range f.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p := f.Profiles[name]
		for _, w := range p.EndpointWarnings() {
			add("s3 endpoint", "warn", "profile "+name+": "+w)
		}
		if p.Source != "local" {
			continue
		}
		switch exposed, err := reachableByOthers(p.LocalPath, clientGID, h.owner); {
		case err != nil:
			add("local source", "warn", "profile "+name+": cannot check its directory (missing, or not readable by the console)")
		case exposed:
			add("local source", "fail", "profile "+name+": its directory can be reached by other accounts (the agent could read the files without lockfile): remove o+rx, and g+rx for the clients group, on it or a parent")
		default:
			add("local source", "ok", "profile "+name+": closed to other accounts")
		}
	}
	for _, m := range f.Marketplaces {
		add("marketplace", "warn", "UNOFFICIAL "+m.Name+" ("+m.IndexURL+")")
	}
	doctorYanked(add)
}

// doctorManaged is the managed line: the company's managed file in force,
// none, or the error that refuses it (every console refuses to start then).
func doctorManaged() (name, status, detail string) {
	m, err := managed.Load(context.Background())
	switch {
	case err != nil:
		return "managed", "fail", err.Error()
	case m == nil:
		return "managed", "ok", noManaged
	}
	return "managed", "ok", m.Describe()
}

// doctorYanked reports the installed versions plugins.lock records as
// yanked (they refuse to run). It reads no index: lockfile plugin verify
// refreshes the status from the marketplaces.
func doctorYanked(add func(string, string, string)) {
	store, err := pluginStore()
	if err != nil {
		return
	}
	installed, err := store.Installed()
	if err != nil || len(installed) == 0 {
		return
	}
	yanked := 0
	for _, in := range installed {
		if in.Yanked {
			yanked++
			add("plugin", "fail", in.Name+"@"+in.Version+" was withdrawn by its marketplace and no longer runs: lockfile plugin update")
		}
	}
	if yanked == 0 {
		add("plugin", "ok", fmt.Sprintf("%d installed, none recorded as yanked (lockfile plugin verify refreshes the status)", len(installed)))
	}
}

// wireFix is the fix of an agent not wired to lockfile.
const wireFix = "lockfile doctor --fix wires it for your account (or lockfile init <agent> for one project)"

// doctorWiring reports the agents not wired to lockfile and, with --fix,
// wires those ask accepts; it returns the repairs made.
func doctorWiring(h doctorHooks, fix bool, ask func(string) bool, add func(finding)) []string {
	env, err := h.agents()
	if err != nil {
		add(finding{Check: "agents", Status: verbs.StatusWarn, Detail: "cannot look for agents: " + err.Error()})
		return nil
	}
	found, pending := wiring.Wiring.Unwired(env)
	if len(found) == 0 {
		add(finding{Check: "agents", Status: verbs.StatusWarn, Detail: "no agent found (Claude Code, Codex, Cursor, Gemini CLI)"})
		return nil
	}
	if len(pending) == 0 {
		add(finding{Check: "agents", Status: verbs.StatusOK, Detail: "wired: " + strings.Join(found, ", ")})
		return nil
	}
	var fixed []string
	for _, a := range pending {
		switch {
		case !fix || !ask("wire "+a+" to lockfile for your account?"):
			add(finding{Check: "agents", Status: verbs.StatusWarn, Detail: "not wired: " + a, Fix: wireFix})
		default:
			if _, err := wiring.Wiring.InitUser(env, a); err != nil {
				add(finding{Check: "agents", Status: verbs.StatusFail, Detail: "wiring " + a + ": " + err.Error(), Fix: wireFix})
				continue
			}
			fixed = append(fixed, "wired "+a+" for your account")
			add(finding{Check: "agents", Status: verbs.StatusOK, Detail: "wired " + a})
		}
	}
	return fixed
}
