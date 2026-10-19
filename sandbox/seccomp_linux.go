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
	// System V IPC and POSIX message queues are not files: Landlock does
	// not see them, the objects outlive the plugin, and every account in
	// the same IPC namespace (the agent's too) can attach to a segment or
	// queue made with permissive modes. A covert channel to the agent.
	"shmget", "shmat", "shmdt", "shmctl",
	"msgget", "msgsnd", "msgrcv", "msgctl",
	"semget", "semop", "semtimedop", "semctl",
	"mq_open", "mq_unlink", "mq_timedsend", "mq_timedreceive", "mq_notify", "mq_getsetattr",
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

// argDenial refuses a system call when one 32-bit argument (a command
// number) is one of values.
type argDenial struct {
	name   string
	arg    int
	values []uint32
}

// argDenied are compared on the low 32 bits of the argument only: the
// kernel reads fcntl's and ioctl's command as an unsigned int, so a
// 64-bit comparison (the library's) would let a plugin through with the
// upper half set.
var argDenied = []argDenial{
	// Naming another process as a descriptor's owner makes the kernel
	// signal it on I/O (O_ASYNC), with any signal (F_SETSIG): a way around
	// the kill rules below, and around Landlock signal scoping below ABI 6.
	{"fcntl", 1, []uint32{unix.F_SETOWN, unix.F_SETOWN_EX, unix.F_SETSIG}},
	{"ioctl", 1, []uint32{ioctlFIOSETOWN, unix.SIOCSPGRP}},
}

// FIOSETOWN is not in x/sys/unix.
const ioctlFIOSETOWN = 0x8901

// errnoEPERM is the action of every denial.
const errnoEPERM = uint32(seccomp.ActionErrno) | uint32(unix.EPERM)

// argOffset is the offset of the low half of argument i in struct
// seccomp_data (nr, arch, instruction_pointer, args[6]); the architectures
// with a syscall table are little-endian.
func argOffset(i int) uint32 { return 16 + 8*uint32(i) }

// argBlock returns the instructions refusing d: load the number, skip the
// block unless it matches, load the argument's low half, compare.
func argBlock(nr uint32, d argDenial) []bpf.Instruction {
	k := len(d.values)
	out := []bpf.Instruction{
		bpf.LoadAbsolute{Off: 0, Size: 4},
		// Skip the argument load, the k comparisons, the jump and the return.
		bpf.JumpIf{Cond: bpf.JumpNotEqual, Val: nr, SkipTrue: uint8(k + 3)},
		bpf.LoadAbsolute{Off: argOffset(d.arg), Size: 4},
	}
	for i, v := range d.values {
		out = append(out, bpf.JumpIf{Cond: bpf.JumpEqual, Val: v, SkipTrue: uint8(k - i)})
	}
	return append(out, bpf.Jump{Skip: 1}, bpf.RetConstant{Val: errnoEPERM})
}

// argBlocks assembles argDenied for info's architecture.
func argBlocks(info *arch.Info) ([]bpf.Instruction, error) {
	var out []bpf.Instruction
	for _, d := range argDenied {
		nr, ok := info.SyscallNames[d.name]
		if !ok {
			return nil, fmt.Errorf("no system call %q on %s", d.name, info.Name)
		}
		out = append(out, argBlock(uint32(nr), d)...)
	}
	return out, nil
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
	args, err := argBlocks(info)
	if err != nil {
		return nil, err
	}
	prefix = append(prefix, args...)
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
