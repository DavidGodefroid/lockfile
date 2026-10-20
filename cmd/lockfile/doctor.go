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
		add("separation", "fail", "no /etc/lockfile/system.toml: the console refuses to start; run sudo lockfile install")
	default:
		service = doctorSeparation(h, sys, add)
	}

	if _, err := h.lookPath("cosign"); err != nil {
		add("cosign", "warn", "cosign is not on PATH: plugin installs and updates are refused until it is")
	} else {
		add("cosign", "ok", "on PATH")
	}

	if service || sys == nil {
		doctorConsoleSide(add)
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

// doctorSeparation checks the separated setup; it reports whether this
// is the console account.
func doctorSeparation(h doctorHooks, sys *sysconf.Config, add func(string, string, string)) bool {
	svcUID, err := sys.ServiceUID()
	if err != nil {
		add("separation", "fail", "the console account "+sys.ServiceUser+" does not exist: run sudo lockfile install")
		return false
	}
	if _, err := user.LookupGroup(sys.ClientGroup); err != nil {
		add("separation", "fail", "the group "+sys.ClientGroup+" does not exist: run sudo lockfile install")
	} else {
		add("separation", "ok", fmt.Sprintf("console account %s, clients group %s", sys.ServiceUser, sys.ClientGroup))
	}
	if fi, err := os.Stat(sys.SocketDir); err != nil {
		add("socket dir", "fail", sys.SocketDir+" is missing: run sudo lockfile install")
	} else if fi.Mode()&fs.ModeSetgid == 0 || fi.Mode().Perm() != 0o750 {
		add("socket dir", "fail", fmt.Sprintf("%s has mode %04o, want 2750 (the clients group lists the running consoles): run sudo lockfile install", sys.SocketDir, fi.Mode().Perm()|setgidBits(fi)))
	} else {
		add("socket dir", "ok", sys.SocketDir+" 2750")
	}
	uid := h.uid()
	if uid == svcUID {
		return true
	}
	if !sys.ClientAllowed(uid) {
		add("clients group", "fail", "this account is not in "+sys.ClientGroup+": run sudo lockfile install --client <this account>, then log in again")
	} else {
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
	switch err := procmount.Check(h.mounts); {
	case err == nil:
		add("proc", "ok", "hidepid=invisible: other accounts' processes are hidden")
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
// directory's mode and the unofficial marketplaces. Paths inside the home
// are not printed.
func doctorConsoleSide(add func(string, string, string)) {
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
	if fi, err := os.Stat(work); err == nil && fi.Mode().Perm()&0o077 != 0 {
		add("work dir", "fail", fmt.Sprintf("the work directory has mode %04o: other accounts may reach fetched files; chmod 0700 it", fi.Mode().Perm()))
	} else if err == nil {
		add("work dir", "ok", "0700")
	}
	for _, m := range f.Marketplaces {
		add("marketplace", "warn", "UNOFFICIAL "+m.Name+" ("+m.IndexURL+")")
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
