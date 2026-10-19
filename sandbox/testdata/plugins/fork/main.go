// Answers 1 if it could create a child process, 0 when forking is refused
// while its own threads keep working. The child is made with a raw clone
// (no exec, which Landlock refuses anyway) and exits at once.
package main

import (
	"os"
	"sync"
	"syscall"
)

func main() {
	// Threads must still be created: run work on many goroutines.
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = make([]byte, 1<<16) }()
	}
	wg.Wait()
	pid, _, errno := syscall.RawSyscall(syscall.SYS_CLONE, uintptr(syscall.SIGCHLD), 0, 0)
	if errno != 0 {
		os.Stdout.WriteString("0")
		return
	}
	if pid == 0 {
		syscall.RawSyscall(syscall.SYS_EXIT_GROUP, 0, 0, 0)
	}
	var ws syscall.WaitStatus
	syscall.Wait4(int(pid), &ws, 0, nil)
	os.Stdout.WriteString("1")
}
