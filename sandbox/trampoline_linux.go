package sandbox

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"syscall"

	"github.com/landlock-lsm/go-landlock/landlock"
	llsys "github.com/landlock-lsm/go-landlock/landlock/syscall"
	"golang.org/x/sys/unix"
)

const (
	fdBinary = 4
	fdStatus = 5
	// pluginPath is the plugin binary as the trampoline sees it: through
	// the inherited fd, so the executed file is the one the store hashed.
	pluginPath = "/proc/self/fd/4"
)

// Main must be the first call in main() of any binary that uses Run (the
// console, and test binaries via TestMain). In a normal process it returns
// at once. In the trampoline it restricts the process and executes the
// plugin; it never returns.
func Main() {
	if len(os.Args) < 2 || os.Args[1] != trampolineArg {
		return
	}
	err := trampoline(os.Args[2:])
	// Only reached on failure; nothing goes to stdout, so no answer can
	// come out of a setup error.
	os.NewFile(fdStatus, "status").WriteString(err.Error())
	os.Exit(126)
}

func trampoline(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("trampoline: want 2 arguments, got %d", len(args))
	}
	cpu, err := strconv.ParseUint(args[0], 10, 64)
	if err != nil {
		return fmt.Errorf("trampoline: cpu: %w", err)
	}
	mem, err := strconv.ParseUint(args[1], 10, 64)
	if err != nil {
		return fmt.Errorf("trampoline: memory: %w", err)
	}
	runtime.LockOSThread()
	// The plugin keeps fds 0-3 only, whatever the console left inheritable.
	if err := unix.CloseRange(fdBinary, ^uint(0), unix.CLOSE_RANGE_CLOEXEC); err != nil {
		for fd := fdBinary; fd < 4096; fd++ {
			syscall.CloseOnExec(fd)
		}
	}

	if err := llsys.AllThreadsPrctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("no_new_privs: %w", err)
	}
	abi, err := llsys.LandlockGetABIVersion()
	if err != nil || abi < 1 {
		return fmt.Errorf("%s: landlock not available (abi %d): %v", ErrUnsupported, abi, err)
	}
	// Every filesystem right of the best ABI is handled, and the only rule
	// lets the plugin execute (and map) itself. With ABI >= 4 TCP bind and
	// connect are handled with no rule, with ABI >= 6 signals and abstract
	// unix sockets are scoped to the plugin.
	rule := landlock.PathAccess(llsys.AccessFSExecute|llsys.AccessFSReadFile, pluginPath)
	if err := landlock.V10.BestEffort().Restrict(rule); err != nil {
		return fmt.Errorf("%s: landlock: %v", ErrUnsupported, err)
	}
	// The pid survives the exec: the plugin may signal and limit only it.
	filter, err := buildFilter(os.Getpid())
	if err != nil {
		return fmt.Errorf("%s: seccomp: %v", ErrUnsupported, err)
	}
	for _, rl := range []struct {
		res      int
		cur, max uint64
	}{
		{unix.RLIMIT_CORE, 0, 0},
		{unix.RLIMIT_FSIZE, 0, 0},
		{unix.RLIMIT_CPU, cpu, cpu + 1},
		// Memory is RLIMIT_DATA, not RLIMIT_AS: the Go runtime reserves
		// (PROT_NONE) far more address space than it uses and cannot start
		// under a 1 GiB RLIMIT_AS. RLIMIT_DATA counts the private writable
		// memory actually mapped.
		{unix.RLIMIT_DATA, mem, mem},
	} {
		if err := unix.Setrlimit(rl.res, &unix.Rlimit{Cur: rl.cur, Max: rl.max}); err != nil {
			return fmt.Errorf("rlimit %d: %w", rl.res, err)
		}
	}
	if err := loadFilter(filter); err != nil {
		return fmt.Errorf("%s: seccomp: %v", ErrUnsupported, err)
	}
	err = syscall.Exec(pluginPath, []string{"plugin"}, []string{protocolEnv})
	return fmt.Errorf("exec plugin: %w", err)
}
