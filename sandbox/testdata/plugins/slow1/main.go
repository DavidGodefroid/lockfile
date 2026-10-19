// slow1 answers 1 after 1.2 s.
package main

import (
	"os"
	"time"
)

func main() {
	time.Sleep(1200 * time.Millisecond)
	os.Stdout.WriteString("1")
}
