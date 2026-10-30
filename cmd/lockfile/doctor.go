package main

import (
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
	"github.com/DavidGodefroid/lockkit/render"
	"github.com/DavidGodefroid/lockkit/sysconf"

	"github.com/DavidGodefroid/lockfile/internal/procmount"
	"github.com/DavidGodefroid/lockfile/internal/product"
	"github.com/DavidGodefroid/lockfile/internal/profile"
	"github.com/DavidGodefroid/lockfile/internal/wiring"
	"github.com/DavidGodefroid/lockfile/sandbox"
)

func init() {
	commands["doctor"] = command{human: true, help: "check the setup from this account: sandbox, separation, agent wiring [--fix] [--json]", run: runDoctor}
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

// finding is one doctor line. Detail never holds a key, a secret or a path
// inside the console account's home.
type finding struct {
	Check  string `json:"check"`
	Status string `json:"status"` // ok, warn, fail
	Detail string `json:"detail"`
}

func runDoctor(e env, args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fix := fs.Bool("fix", false, "wire the installed agents that are not wired yet (nothing else)")
	asJSON := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return usageErr(e, "doctor [--fix] [--json]")
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
	add := func(check, status, detail string) { out = append(out, finding{check, status, detail}) }

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
		doctorWiring(h, *fix, add)
	}

	failed := false
	for _, f := range out {
		failed = failed || f.Status == "fail"
	}
	if *asJSON {
		if err := render.Value(e.stdout, out, outCaps); err != nil {
			return product.ExitError
		}
	} else {
		for _, f := range out {
			mark := "ok  "
			switch f.Status {
			case "warn":
				mark = yellow("warn")
			case "fail":
				mark = red("FAIL")
			}
			fmt.Fprintf(e.stdout, "%s  %-12s %s\n", mark, f.Check, f.Detail)
		}
	}
	if failed {
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
	path, err := configPath()
	if err != nil {
		return
	}
	f, err := profile.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		add("config", "warn", "no profile yet: lockfile profile add NAME --source …")
		return
	case err != nil:
		add("config", "fail", "the config file does not load: lockfile console --profile NAME names the error")
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

// doctorWiring reports the agents not wired to lockfile, and wires them
// with --fix.
func doctorWiring(h doctorHooks, fix bool, add func(string, string, string)) {
	env, err := h.agents()
	if err != nil {
		add("agents", "warn", "cannot look for agents: "+err.Error())
		return
	}
	found, pending := wiring.Wiring.Unwired(env)
	if len(found) == 0 {
		add("agents", "warn", "no agent found (Claude Code, Codex, Cursor, Gemini CLI)")
		return
	}
	if len(pending) == 0 {
		add("agents", "ok", "wired: "+strings.Join(found, ", "))
		return
	}
	if !fix {
		add("agents", "warn", "not wired: "+strings.Join(pending, ", ")+" (lockfile doctor --fix, or lockfile init <agent> --user)")
		return
	}
	for _, a := range pending {
		if _, err := wiring.Wiring.InitUser(env, a); err != nil {
			add("agents", "fail", "wiring "+a+": "+err.Error())
			continue
		}
		add("agents", "ok", "wired "+a)
	}
}
