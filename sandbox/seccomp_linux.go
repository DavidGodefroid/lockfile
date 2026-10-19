package sandbox

import (
	"fmt"
	"unsafe"

	seccomp "github.com/elastic/go-seccomp-bpf"
	"github.com/elastic/go-seccomp-bpf/arch"
	"golang.org/x/net/bpf"
	"golang.org/x/sys/unix"
)

// denied are the syscalls a plugin gets EPERM for. Everything else is
// allowed: Landlock is what confines the filesystem (no path can be
// opened, nothing but the plugin itself can be executed), seccomp removes
// the network and the kernel interfaces a parser has no use for.
var denied = []string{
	"socket", "socketpair",
	"ptrace", "process_vm_readv", "process_vm_writev",
	"mount", "umount2", "pivot_root", "chroot", "unshare", "setns",
	"bpf", "perf_event_open", "userfaultfd",
	"keyctl", "add_key", "request_key",
	"io_uring_setup", "io_uring_enter", "io_uring_register",
	// Not in the design's list: a process leaving the plugin's process
	// group would survive the group kill; memfd and execveat are a way
	// to execute code that Landlock's path rules do not name.
	"setsid", "setpgid", "memfd_create", "execveat",
	"open_by_handle_at", "name_to_handle_at",
	// Landlock does not cover file metadata: without these a plugin that
	// learns its input's path (readlink of /proc/self/fd/3) could make it,
	// its directory or the console's home readable to the agent account.
	"chmod", "fchmod", "fchmodat", "fchmodat2",
	"chown", "fchown", "lchown", "fchownat",
	"setxattr", "lsetxattr", "fsetxattr", "setxattrat",
	"removexattr", "lremovexattr", "fremovexattr", "removexattrat",
	"utime", "utimes", "utimensat", "futimesat",
	"truncate",
	// Covered by Landlock's handled rights, refused here too.
	"link", "linkat", "symlink", "symlinkat", "rename", "renameat", "renameat2",
	"mknod", "mknodat", "mkdir", "mkdirat", "rmdir", "unlink", "unlinkat",
	"inotify_init", "inotify_init1", "fanotify_init",
	// Other processes of the same uid (signal scoping needs Landlock ABI 6).
	"tkill", "pidfd_open", "pidfd_send_signal", "pidfd_getfd", "kcmp", "process_madvise",
	"setpriority", "ioprio_set", "sched_setaffinity", "sched_setscheduler", "sched_setparam", "sched_setattr",
	// Child processes: only threads (clone with CLONE_THREAD, below).
	"fork", "vfork", "clone3",
}

// available keeps the names this architecture has (arm64 has no chmod,
// fork, utimes…).
func available(info *arch.Info, names []string) []string {
	var out []string
	for _, n := range names {
		if _, ok := info.SyscallNames[n]; ok {
			out = append(out, n)
		}
	}
	return out
}

// conditional returns the rules that depend on arguments: signals and
// limits may target only the plugin itself (pid self, the trampoline's pid
// kept across exec), and clone may only make threads.
func conditional(info *arch.Info, self int) []seccomp.NameWithConditions {
	notSelf := seccomp.Condition{Argument: 0, Operation: seccomp.NotEqual, Value: uint64(self)}
	rules := []seccomp.NameWithConditions{
		{Name: "kill", Conditions: seccomp.ArgumentConditions{notSelf}},
		{Name: "tgkill", Conditions: seccomp.ArgumentConditions{notSelf}},
		{Name: "rt_sigqueueinfo", Conditions: seccomp.ArgumentConditions{notSelf}},
		{Name: "rt_tgsigqueueinfo", Conditions: seccomp.ArgumentConditions{notSelf}},
		// pid 0 is the caller itself.
		{Name: "prlimit64", Conditions: seccomp.ArgumentConditions{notSelf, {Argument: 0, Operation: seccomp.NotEqual, Value: 0}}},
		{Name: "clone", Conditions: seccomp.ArgumentConditions{{Argument: 0, Operation: seccomp.BitsNotSet, Value: unix.CLONE_THREAD}}},
	}
	var out []seccomp.NameWithConditions
	for _, r := range rules {
		if _, ok := info.SyscallNames[r.Name]; ok {
			out = append(out, r)
		}
	}
	return out
}

func buildFilter(self int) ([]bpf.RawInstruction, error) {
	info, err := arch.GetInfo("")
	if err != nil {
		return nil, err
	}
	p := seccomp.Policy{
		DefaultAction: seccomp.ActionAllow,
		Syscalls: []seccomp.SyscallGroup{{
			Names:              available(info, denied),
			NamesWithCondtions: conditional(info, self),
			Action:             seccomp.ActionErrno,
		}},
	}
	insts, err := p.Assemble()
	if err != nil {
		return nil, err
	}
	// The library falls through to the default action (allow) for a
	// foreign architecture, which would let a plugin reach socket() through
	// the 32-bit ABI. Kill on any architecture but the native one.
	prefix := []bpf.Instruction{
		bpf.LoadAbsolute{Off: 4, Size: 4}, // seccomp_data.arch
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: uint32(info.ID), SkipTrue: 1},
		bpf.RetConstant{Val: uint32(seccomp.ActionKillProcess)},
	}
	return bpf.Assemble(append(prefix, insts...))
}

func loadFilter(raw []bpf.RawInstruction) error {
	filters := make([]unix.SockFilter, len(raw))
	for i, r := range raw {
		filters[i] = unix.SockFilter{Code: r.Op, Jt: r.Jt, Jf: r.Jf, K: r.K}
	}
	prog := unix.SockFprog{Len: uint16(len(filters)), Filter: &filters[0]}
	_, _, errno := unix.Syscall(unix.SYS_SECCOMP, unix.SECCOMP_SET_MODE_FILTER,
		unix.SECCOMP_FILTER_FLAG_TSYNC, uintptr(unsafe.Pointer(&prog)))
	if errno != 0 {
		return fmt.Errorf("seccomp(SET_MODE_FILTER): %w", errno)
	}
	return nil
}
