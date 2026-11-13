// Package sandbox runs one plugin binary on one input file under Landlock,
// seccomp-bpf and rlimits, and turns what it produced into a
// pluginspec.Outcome.
//
// The console re-executes itself as a trampoline (os.Args[1] ==
// "__sandbox"), which restricts itself and then execs the plugin. The
// plugin gets: stdin /dev/null, the input as read-only fd 3, stdout read
// up to 3 bytes, stderr counted and discarded, an environment holding only
// LOCKFILE_PLUGIN_PROTOCOL=<its manifest protocol>, cwd /. A protocol-2
// plugin with inputs also gets their values as read-only fd 6, and one
// with detail a write fd 7 read up to 4 KiB. It may execute nothing but
// itself, open no path, and create no socket. There is no unsandboxed
// fallback.
package sandbox

import (
	"errors"
	"io"
	"os"
	"strconv"
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
	trampolineArg  = "__sandbox"
	protocolEnvKey = "LOCKFILE_PLUGIN_PROTOCOL="
	stdoutCap      = 3
)

// protocolEnv is the plugin's only environment variable.
func protocolEnv(protocol int) string {
	if protocol < 1 {
		protocol = 1
	}
	return protocolEnvKey + strconv.Itoa(protocol)
}

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
	// Protocol is the plugin's manifest protocol (0: 1), given to it as
	// LOCKFILE_PLUGIN_PROTOCOL.
	Protocol int
	// Inputs, when not nil, is the canonical JSON of the values the human
	// typed for the plugin's inputs: the plugin reads it on fd 6, a sealed
	// read-only memfd (never argv, env or a path). Run never keeps it.
	Inputs []byte
	// Detail, when not nil, gets the first pluginspec.MaxDetail bytes the
	// plugin writes on fd 7 (the rest is drained unread): a detail for the
	// human only, never for the agent or the audit.
	Detail io.Writer
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
