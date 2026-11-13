// Answers 1 if it could raise its stack limit above 8 MiB: the main
// thread's stack is not data, so RLIMIT_DATA does not bound it. 0 when
// the limit is fixed.
package main

import (
	"os"

	"golang.org/x/sys/unix"
)

func main() {
	for _, want := range []uint64{64 << 20, unix.RLIM_INFINITY} {
		var rl unix.Rlimit
		if unix.Getrlimit(unix.RLIMIT_STACK, &rl) != nil {
			os.Exit(2)
		}
		rl.Cur = want
		if rl.Max < want {
			rl.Max = want
		}
		if unix.Setrlimit(unix.RLIMIT_STACK, &rl) == nil {
			os.Stdout.WriteString("1")
			return
		}
	}
	os.Stdout.WriteString("0")
}
