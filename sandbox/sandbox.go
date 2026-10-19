// Package sandbox runs one plugin binary on one input file under Landlock,
// seccomp-bpf and rlimits, and turns what it produced into a
// pluginspec.Outcome.
//
// The console re-executes itself as a trampoline (os.Args[1] ==
// "__sandbox"), which restricts itself and then execs the plugin. The
// plugin gets: stdin /dev/null, the input as read-only fd 3, stdout read
// up to 3 bytes, stderr counted and discarded, an environment holding only
// LOCKFILE_PLUGIN_PROTOCOL=1, cwd /. It may execute nothing but itself,
// open no path, and create no socket. There is no unsandboxed fallback.
package sandbox

import (
	"errors"
	"os"
	"time"

	"github.com/DavidGodefroid/lockfile/pluginspec"
)

// ErrUnsupported is returned when the kernel or platform cannot sandbox
// plugins. Its text is the agent code for it.
var ErrUnsupported = errors.New("sandbox_unavailable")

// Defaults applied to zero fields of Spec.Limits.
const (
	DefaultTimeout = 30 * time.Second
	DefaultMemory  = 1 << 30
)

const (
	trampolineArg = "__sandbox"
	protocolEnv   = "LOCKFILE_PLUGIN_PROTOCOL=1"
	stdoutCap     = 3
)

// Spec is one plugin run.
type Spec struct {
	// Binary is the plugin executable, opened (and re-hashed) by the plugin
	// store. The trampoline executes this very file through its fd, so the
	// file that runs is the one that was hashed.
	Binary *os.File
	// Input is the file the plugin answers about. The plugin gets a fresh
	// read-only open of it as fd 3.
	Input  *os.File
	Limits pluginspec.Limits
}

// Report describes a run for the audit. It is never shown to the agent.
type Report struct {
	PID         int
	StderrBytes int64
	ExitCode    int
	Signal      string
	Duration    time.Duration
}

func withDefaults(l pluginspec.Limits) pluginspec.Limits {
	if l.Timeout <= 0 {
		l.Timeout = DefaultTimeout
	}
	if l.Memory <= 0 {
		l.Memory = DefaultMemory
	}
	if l.CPUSeconds <= 0 {
		l.CPUSeconds = int(l.Timeout / time.Second)
	}
	return l
}
