// Writes 1 MiB of detail on fd 7, then answers 1: the console keeps the
// first 4 KiB and drains the rest.
package main

import (
	"bytes"
	"os"
)

func main() {
	d := os.NewFile(7, "detail")
	chunk := bytes.Repeat([]byte("d"), 64<<10)
	for range 16 {
		if _, err := d.Write(chunk); err != nil {
			os.Exit(3)
		}
	}
	d.Close()
	os.Stdout.WriteString("1")
}
