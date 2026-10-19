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

const (
	// asHeadroom is the address space a plugin gets above its memory
	// limit. A Go program reserves about 600 MiB at start (page allocator
	// summaries, arena hints) and fails below 768 MiB in total.
	asHeadroom = 1 << 30
	// threadAllowance is how many tasks a plugin may add to its uid. A Go
	// plugin needs GOMAXPROCS threads plus a few; the margin absorbs other
	// tasks of the uid (the console, other consoles) starting while it runs.
	// A plugin over it gets EAGAIN from clone (the Go runtime then dies):
	// the account keeps tasks for the console.
	threadAllowance = 512
	// maxTasks bounds the search for the uid's task count (PID_MAX_LIMIT).
	maxTasks = 1 << 22
)

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
	nproc, err := nprocLimit()
	if err != nil {
		return fmt.Errorf("rlimit nproc: %w", err)
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
		// Memory is RLIMIT_DATA: the private writable memory actually
		// mapped. RLIMIT_DATA does not count shared, grows-down or
		// read-only mappings (seccomp refuses the first two), so RLIMIT_AS
		// bounds everything at the limit plus asHeadroom: the Go runtime
		// reserves (PROT_NONE) more address space than it uses and cannot
		// start under an RLIMIT_AS near the memory limit.
		{unix.RLIMIT_DATA, mem, mem},
		{unix.RLIMIT_AS, mem + asHeadroom, mem + asHeadroom},
		// RLIMIT_NPROC counts every task (thread) of the real uid, the
		// console's and other plugins' included, so it is set relative to
		// what the uid runs now (measured, see uidTasks): the plugin may
		// add threadAllowance tasks. Not enforced for root.
		{unix.RLIMIT_NPROC, nproc, nproc},
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

// uidTasks measures how many tasks the kernel counts against RLIMIT_NPROC
// for this process's real uid: the smallest soft limit under which a fork
// still succeeds, minus one. /proc cannot tell: tasks in other pid
// namespaces, or hidden by hidepid, are counted by the kernel but not
// listed. It leaves the soft limit changed; the caller sets the final one.
// ok is false when no fork succeeds up to max.
func uidTasks(max uint64) (n uint64, ok bool) {
	if !forkUnder(max, max) {
		return 0, false
	}
	lo, hi := uint64(1), max // forkUnder(hi) holds
	for lo < hi {
		mid := lo + (hi-lo)/2
		if forkUnder(mid, max) {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo - 1, true
}

// forkUnder reports whether a child process can be created with the soft
// RLIMIT_NPROC at limit. The child exits at once without running Go code.
func forkUnder(limit, max uint64) bool {
	if err := unix.Setrlimit(unix.RLIMIT_NPROC, &unix.Rlimit{Cur: limit, Max: max}); err != nil {
		return false
	}
	pid, _, errno := syscall.RawSyscall(syscall.SYS_CLONE, uintptr(syscall.SIGCHLD), 0, 0)
	if errno != 0 {
		return false
	}
	if pid == 0 {
		syscall.RawSyscall(syscall.SYS_EXIT_GROUP, 0, 0, 0)
	}
	var ws syscall.WaitStatus
	for {
		if _, err := syscall.Wait4(int(pid), &ws, 0, nil); err != syscall.EINTR {
			break
		}
	}
	return true
}

// nprocLimit is the RLIMIT_NPROC of the plugin: the uid's tasks now plus
// threadAllowance, never above the current hard limit.
func nprocLimit() (uint64, error) {
	var cur unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NPROC, &cur); err != nil {
		return 0, err
	}
	max := cur.Max
	if max > maxTasks {
		max = maxTasks
	}
	n, ok := uidTasks(max)
	if !ok {
		// Forks fail for another reason (a cgroup pids limit, memory):
		// keep the inherited limit, the cgroup already caps the tasks.
		return cur.Max, nil
	}
	return min(n+threadAllowance, cur.Max), nil
}
