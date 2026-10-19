// slow6 answers 1 after 6 s.
package main

import (
	"os"
	"time"
)

func main() {
	time.Sleep(6 * time.Second)
	os.Stdout.WriteString("1")
}
