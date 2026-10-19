package main

import "os"

func main() {
	b := make([]byte, 2<<30)
	for i := 0; i < len(b); i += 4096 {
		b[i] = 1
	}
	os.Stdout.WriteString("1")
}
