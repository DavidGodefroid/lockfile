package main

import "os"

func main() {
	e := os.Environ()
	if len(e) == 1 && e[0] == "LOCKFILE_PLUGIN_PROTOCOL=1" {
		os.Stdout.WriteString("1")
		return
	}
	os.Stdout.WriteString("0")
}
