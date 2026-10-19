package main

import (
	"os"
	"time"
)

func main() {
	os.Stdout.WriteString("1")
	time.Sleep(time.Hour)
}
