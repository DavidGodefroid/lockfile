// Answers 1 if it may signal a process other than itself (signal 0 checks
// the permission and delivers nothing), or change another process's
// scheduling or limits; 0 when every attempt is refused and signalling
// itself still works.
package main

import (
	"os"
	"syscall"

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
	os.Stdout.WriteString("0")
}
