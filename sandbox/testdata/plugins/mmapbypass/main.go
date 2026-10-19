// Answers 1 if it could map memory that RLIMIT_DATA does not count:
// shared anonymous memory, a grows-down mapping, or a huge read-only
// private mapping (address space). 0 when every mapping is refused.
package main

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func main() {
	tries := []struct {
		size  uintptr
		prot  uintptr
		flags uintptr
	}{
		{1 << 30, unix.PROT_READ | unix.PROT_WRITE, unix.MAP_SHARED | unix.MAP_ANONYMOUS},
		{1 << 30, unix.PROT_READ | unix.PROT_WRITE, unix.MAP_SHARED_VALIDATE | unix.MAP_ANONYMOUS},
		{1 << 20, unix.PROT_READ | unix.PROT_WRITE, unix.MAP_PRIVATE | unix.MAP_ANONYMOUS | unix.MAP_GROWSDOWN},
		// Not writable, so not data: only RLIMIT_AS bounds it.
		{64 << 30, unix.PROT_READ, unix.MAP_PRIVATE | unix.MAP_ANONYMOUS | unix.MAP_NORESERVE},
	}
	for _, try := range tries {
		addr, _, errno := syscall.RawSyscall6(syscall.SYS_MMAP, 0, try.size, try.prot, try.flags, ^uintptr(0), 0)
		if errno == 0 {
			syscall.RawSyscall(syscall.SYS_MUNMAP, addr, try.size, 0)
			os.Stdout.WriteString("1")
			return
		}
	}
	os.Stdout.WriteString("0")
}
