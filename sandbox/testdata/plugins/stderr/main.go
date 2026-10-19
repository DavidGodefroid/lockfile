package main

import (
	"bytes"
	"os"
)

func main() {
	os.Stderr.Write(bytes.Repeat([]byte("S"), 10240))
	os.Stdout.WriteString("1")
}
