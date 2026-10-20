// Answers 1 if it could reach System V IPC (shared memory, message queues,
// semaphores) or POSIX message queues: objects that outlive the plugin and
// that other accounts in the same IPC namespace may read. 0 when every
// call is refused with EPERM.
package main

import (
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

func main() {
	name, _ := unix.BytePtrFromString("lockfile-sysvipc-test")
	tries := []struct {
		call    func() (uintptr, syscall.Errno)
		cleanup func(id uintptr)
	}{
		{
			func() (uintptr, syscall.Errno) {
				r, _, e := syscall.RawSyscall(unix.SYS_SHMGET, unix.IPC_PRIVATE, 4096, unix.IPC_CREAT|0o600)
				return r, e
			},
			func(id uintptr) { syscall.RawSyscall(unix.SYS_SHMCTL, id, unix.IPC_RMID, 0) },
		},
		{
			func() (uintptr, syscall.Errno) {
				r, _, e := syscall.RawSyscall(unix.SYS_MSGGET, unix.IPC_PRIVATE, unix.IPC_CREAT|0o600, 0)
				return r, e
			},
			func(id uintptr) { syscall.RawSyscall(unix.SYS_MSGCTL, id, unix.IPC_RMID, 0) },
		},
		{
			func() (uintptr, syscall.Errno) {
				r, _, e := syscall.RawSyscall(unix.SYS_SEMGET, unix.IPC_PRIVATE, 1, unix.IPC_CREAT|0o600)
				return r, e
			},
			func(id uintptr) { syscall.RawSyscall6(unix.SYS_SEMCTL, id, 0, unix.IPC_RMID, 0, 0, 0) },
		},
		{
			func() (uintptr, syscall.Errno) {
				r, _, e := syscall.RawSyscall6(unix.SYS_MQ_OPEN, uintptr(unsafe.Pointer(name)), unix.O_CREAT|unix.O_RDWR, 0o600, 0, 0, 0)
				return r, e
			},
			func(fd uintptr) {
				syscall.Close(int(fd))
				syscall.RawSyscall(unix.SYS_MQ_UNLINK, uintptr(unsafe.Pointer(name)), 0, 0)
			},
		},
	}
	answer := "0"
	for _, try := range tries {
		id, errno := try.call()
		if errno == 0 {
			try.cleanup(id)
		}
		if errno != syscall.EPERM {
			answer = "1"
		}
	}
	os.Stdout.WriteString(answer)
}
