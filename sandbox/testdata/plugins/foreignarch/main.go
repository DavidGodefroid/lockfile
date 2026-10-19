//go:build amd64

// Makes a 32-bit system call (int $0x80) from a 64-bit process; the filter
// must kill it. Prints 1 if it survives.
package main

import "os"

func int80getpid() uintptr

func main() {
	int80getpid()
	os.Stdout.WriteString("1")
}
