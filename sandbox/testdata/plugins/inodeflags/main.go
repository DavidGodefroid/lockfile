// Answers 1 if any of these is not refused with EPERM: changing inode
// flags or extended attributes of its input (the same class of escape as
// chmod on fd 3), or reaching another process's memory layout through
// move_pages, migrate_pages or get_robust_list (an ASLR probe of the
// console). 0 when every call is refused with EPERM.
package main

import (
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	fsIOC32SetFlags  = 0x40046602
	fsIOCFSGetXattr  = 0x801c581f // _IOR('X', 31, struct fsxattr)
	fsIOCFSSetXattr  = 0x401c5820 // _IOW('X', 32, struct fsxattr)
	sysFileSetattr   = 469
	inputDescriptor  = 3
	atEmptyPathFlags = unix.AT_EMPTY_PATH
)

func main() {
	ppid := uintptr(os.Getppid())
	var flags int64
	// Setting back the flags it has: a change if it gets through.
	syscall.RawSyscall(syscall.SYS_IOCTL, inputDescriptor, unix.FS_IOC_GETFLAGS, uintptr(unsafe.Pointer(&flags)))
	var fsx [28]byte
	syscall.RawSyscall(syscall.SYS_IOCTL, inputDescriptor, fsIOCFSGetXattr, uintptr(unsafe.Pointer(&fsx[0])))
	empty, _ := unix.BytePtrFromString("")
	var head, size uintptr
	tries := []func() syscall.Errno{
		func() syscall.Errno {
			return raw(syscall.SYS_IOCTL, inputDescriptor, unix.FS_IOC_SETFLAGS, uintptr(unsafe.Pointer(&flags)))
		},
		func() syscall.Errno {
			return raw(syscall.SYS_IOCTL, inputDescriptor, fsIOC32SetFlags, uintptr(unsafe.Pointer(&flags)))
		},
		func() syscall.Errno {
			return raw(syscall.SYS_IOCTL, inputDescriptor, 1<<32|unix.FS_IOC_SETFLAGS, uintptr(unsafe.Pointer(&flags)))
		},
		func() syscall.Errno {
			return raw(syscall.SYS_IOCTL, inputDescriptor, fsIOCFSSetXattr, uintptr(unsafe.Pointer(&fsx[0])))
		},
		func() syscall.Errno {
			return raw(sysFileSetattr, inputDescriptor, uintptr(unsafe.Pointer(empty)), uintptr(unsafe.Pointer(&fsx[0])), uintptr(len(fsx)), atEmptyPathFlags)
		},
		func() syscall.Errno { return raw(unix.SYS_MOVE_PAGES, ppid, 0, 0, 0, 0, 0) },
		func() syscall.Errno { return raw(unix.SYS_MIGRATE_PAGES, ppid, 0, 0, 0) },
		func() syscall.Errno {
			return raw(unix.SYS_GET_ROBUST_LIST, ppid, uintptr(unsafe.Pointer(&head)), uintptr(unsafe.Pointer(&size)))
		},
	}
	for _, try := range tries {
		if try() != syscall.EPERM {
			os.Stdout.WriteString("1")
			return
		}
	}
	os.Stdout.WriteString("0")
}

func raw(nr uintptr, args ...uintptr) syscall.Errno {
	var a [6]uintptr
	copy(a[:], args)
	_, _, e := syscall.RawSyscall6(nr, a[0], a[1], a[2], a[3], a[4], a[5])
	return e
}
