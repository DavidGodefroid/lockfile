// Answers 0 when running another program fails; /bin/sh would print 1.
package main

import (
	"os"
	"os/exec"
	"syscall"
)

func main() {
	if out, err := exec.Command("/bin/sh", "-c", "echo 1").Output(); err == nil {
		os.Stdout.Write(out)
		return
	}
	err := syscall.Exec("/bin/sh", []string{"sh", "-c", "echo 1"}, nil)
	_ = err
	os.Stdout.WriteString("0")
}
