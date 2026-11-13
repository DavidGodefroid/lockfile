// Allocates and touches mib MiB (set with -ldflags -X main.mib=N), then
// answers 1: it answers only when its memory limit allows that much.
package main

import (
	"os"
	"strconv"
)

var mib = "16"

func main() {
	n, err := strconv.Atoi(mib)
	if err != nil {
		os.Exit(2)
	}
	b := make([]byte, n<<20)
	for i := 0; i < len(b); i += 4096 {
		b[i] = 1
	}
	os.Stdout.WriteString("1")
}
