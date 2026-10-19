// Answers 0 only when creating any socket fails with EPERM.
package main

import (
	"errors"
	"net"
	"os"
	"syscall"
)

func main() {
	_, tcp := net.Dial("tcp", "127.0.0.1:1")
	_, unix := net.Dial("unix", "/run/lockfile-sandbox-probe.sock")
	_, sockErr := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, 0)
	if errors.Is(tcp, syscall.EPERM) && errors.Is(unix, syscall.EPERM) && errors.Is(sockErr, syscall.EPERM) {
		os.Stdout.WriteString("0")
		return
	}
	os.Stdout.WriteString("1")
}
