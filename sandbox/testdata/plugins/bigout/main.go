package main

import (
	"bytes"
	"os"
)

func main() { os.Stdout.Write(bytes.Repeat([]byte("1"), 1<<20)) }
