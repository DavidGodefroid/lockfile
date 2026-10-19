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
}

func buildFilter() ([]bpf.RawInstruction, error) {
	info, err := arch.GetInfo("")
	if err != nil {
		return nil, err
	}
	p := seccomp.Policy{
		DefaultAction: seccomp.ActionAllow,
		Syscalls:      []seccomp.SyscallGroup{{Names: denied, Action: seccomp.ActionErrno}},
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
