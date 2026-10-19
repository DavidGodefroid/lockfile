// Answers 1 if any descriptor above 3 is open: the plugin must get only
// stdin, stdout, stderr and its input.
package main

import (
	"os"

	"golang.org/x/sys/unix"
)

func main() {
	for fd := 4; fd < 1024; fd++ {
		if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err == nil {
			os.Stdout.WriteString("1")
			return
		}
	}
	os.Stdout.WriteString("0")
}
