// Answers 1 if it may signal a process other than itself (signal 0 checks
// the permission and delivers nothing), change another process's
// scheduling or limits, or name another process as the owner of a
// descriptor (with O_ASYNC the kernel then signals that owner, SIGKILL
// with F_SETSIG); 0 when every attempt is refused and signalling itself
// still works.
package main

import (
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

func main() {
	ppid := os.Getppid()
	self := os.Getpid()
	if syscall.Kill(self, 0) != nil {
		os.Stdout.WriteString("1") // the runtime needs to signal itself
		return
	}
	tries := []func() error{
		func() error { return syscall.Kill(ppid, 0) },
		func() error { return syscall.Kill(-1, 0) },
		func() error { return syscall.Kill(0, 0) },
		func() error { return unix.Tgkill(ppid, ppid, 0) },
		func() error { return syscall.Setpriority(syscall.PRIO_PROCESS, ppid, 19) },
		func() error {
			var r unix.Rlimit
			return unix.Prlimit(ppid, unix.RLIMIT_NOFILE, nil, &r)
		},
	}
	for _, try := range tries {
		if try() == nil {
			os.Stdout.WriteString("1")
			return
		}
	}
	// The owner route must be refused by the filter (EPERM) whatever the
	// descriptor: a pipe here, the input or a socket elsewhere.
	var p [2]int
	if syscall.Pipe(p[:]) != nil {
		os.Stdout.WriteString("1")
		return
	}
	fd := uintptr(p[0])
	pid := uintptr(ppid)
	owner := struct{ typ, pid int32 }{1 /* F_OWNER_PID */, int32(ppid)}
	ownerTries := []func() syscall.Errno{
		func() syscall.Errno { return fcntl(fd, unix.F_SETOWN, pid) },
		func() syscall.Errno { return fcntl(fd, unix.F_SETOWN, uintptr(-ppid)) },
		// The kernel reads the command as 32 bits: the upper half must not
		// get past the filter.
		func() syscall.Errno { return fcntl(fd, 1<<32|unix.F_SETOWN, pid) },
		func() syscall.Errno { return fcntl(fd, unix.F_SETOWN_EX, uintptr(unsafe.Pointer(&owner))) },
		func() syscall.Errno { return fcntl(fd, unix.F_SETSIG, uintptr(syscall.SIGKILL)) },
		func() syscall.Errno { return ioctl(fd, 0x8901 /* FIOSETOWN */, uintptr(unsafe.Pointer(&ppid))) },
		func() syscall.Errno { return ioctl(fd, unix.SIOCSPGRP, uintptr(unsafe.Pointer(&ppid))) },
		func() syscall.Errno { return ioctl(fd, 1<<32|unix.SIOCSPGRP, uintptr(unsafe.Pointer(&ppid))) },
	}
	for _, try := range ownerTries {
		if try() != syscall.EPERM {
			os.Stdout.WriteString("1")
			return
		}
	}
	os.Stdout.WriteString("0")
}

func fcntl(fd, cmd, arg uintptr) syscall.Errno {
	_, _, e := syscall.RawSyscall(syscall.SYS_FCNTL, fd, cmd, arg)
	return e
}

func ioctl(fd, cmd, arg uintptr) syscall.Errno {
	_, _, e := syscall.RawSyscall(syscall.SYS_IOCTL, fd, cmd, arg)
	return e
}
