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
	// Other processes' memory layout (an ASLR probe of the console):
	// pages of another pid, its robust futex list head.
	"move_pages", "migrate_pages", "get_robust_list",
}

// deniedNumbers are denied system calls newer than the filter library's
// table, by number. Numbers from 424 on are the same on every
// architecture.
var deniedNumbers = map[string]uint32{
	// Linux 6.17: inode flags and fs xattrs of a file, by path or fd (the
	// same class of change as chmod on the input).
	"file_setattr": 469,
}

// available keeps the names this architecture has (arm64 has no chmod,
// fork, utimes…). A name that amd64 does not know either is a mistake (a
// typo, or a call newer than the table that belongs in deniedNumbers):
// it is refused rather than silently left allowed.
func available(info *arch.Info, names []string) ([]string, error) {
	var out []string
	for _, n := range names {
		if _, ok := info.SyscallNames[n]; ok {
			out = append(out, n)
			continue
		}
		if _, ok := arch.X86_64.SyscallNames[n]; !ok {
			return nil, fmt.Errorf("unknown system call %q in the deny list", n)
		}
	}
	return out, nil
}

// numberBlocks denies deniedNumbers.
func numberBlocks() []bpf.Instruction {
	var out []bpf.Instruction
	for _, nr := range deniedNumbers {
		out = append(out,
			bpf.LoadAbsolute{Off: 0, Size: 4},
			bpf.JumpIf{Cond: bpf.JumpNotEqual, Val: nr, SkipTrue: 1},
			bpf.RetConstant{Val: errnoEPERM},
		)
	}
	return out
}

// conditional returns the rules that depend on arguments: signals and
// limits may target only the plugin itself (pid self, the trampoline's pid
// kept across exec), clone may only make threads, and mmap may not make
// shared anonymous or grows-down mappings.
func conditional(info *arch.Info, self int) ([]seccomp.NameWithConditions, error) {
	notSelf := seccomp.Condition{Argument: 0, Operation: seccomp.NotEqual, Value: uint64(self)}
	rules := []seccomp.NameWithConditions{
		{Name: "kill", Conditions: seccomp.ArgumentConditions{notSelf}},
		{Name: "tgkill", Conditions: seccomp.ArgumentConditions{notSelf}},
		{Name: "rt_sigqueueinfo", Conditions: seccomp.ArgumentConditions{notSelf}},
		{Name: "rt_tgsigqueueinfo", Conditions: seccomp.ArgumentConditions{notSelf}},
		// pid 0 is the caller itself.
		{Name: "prlimit64", Conditions: seccomp.ArgumentConditions{notSelf, {Argument: 0, Operation: seccomp.NotEqual, Value: 0}}},
		{Name: "clone", Conditions: seccomp.ArgumentConditions{{Argument: 0, Operation: seccomp.BitsNotSet, Value: unix.CLONE_THREAD}}},
		// Memory RLIMIT_DATA does not count (RLIMIT_AS bounds it too): shared
		// anonymous memory and grows-down mappings. flags is an unsigned
		// long, so the library's 64-bit test is exact.
		{Name: "mmap", Conditions: seccomp.ArgumentConditions{{Argument: 3, Operation: seccomp.BitsSet, Value: unix.MAP_GROWSDOWN}}},
		{Name: "mmap", Conditions: seccomp.ArgumentConditions{
			{Argument: 3, Operation: seccomp.BitsSet, Value: unix.MAP_SHARED},
			{Argument: 3, Operation: seccomp.BitsSet, Value: unix.MAP_ANONYMOUS},
		}},
	}
	for _, r := range rules {
		if _, ok := info.SyscallNames[r.Name]; !ok {
			return nil, fmt.Errorf("no system call %q on %s", r.Name, info.Name)
		}
	}
	return rules, nil
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
	// Inode flags and fs xattrs of the input (fd 3 is its owner's file):
	// Landlock does not cover metadata, see chmod above.
	{"ioctl", 1, []uint32{ioctlFIOSETOWN, unix.SIOCSPGRP,
		unix.FS_IOC_SETFLAGS, ioctlFSIOC32SetFlags, ioctlFSIOCFSSetXattr}},
}

// Not in x/sys/unix.
const (
	ioctlFIOSETOWN       = 0x8901
	ioctlFSIOC32SetFlags = 0x40046602 // _IOW('f', 2, int)
	ioctlFSIOCFSSetXattr = 0x401c5820 // _IOW('X', 32, struct fsxattr)
)

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
	names, err := available(info, denied)
	if err != nil {
		return nil, err
	}
	cond, err := conditional(info, self)
	if err != nil {
		return nil, err
	}
	p := seccomp.Policy{
		DefaultAction: seccomp.ActionAllow,
		Syscalls: []seccomp.SyscallGroup{{
			Names:              names,
			NamesWithCondtions: cond,
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
	prefix = append(prefix, numberBlocks()...)
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
