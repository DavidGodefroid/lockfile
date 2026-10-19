// Answers 0 only when fd 3 is readable and every path-based open fails.
package main

import "os"

// inputPath is set with -ldflags -X to the input's path on the host.
var inputPath string

func main() {
	buf := make([]byte, 1)
	if _, err := os.NewFile(3, "input").Read(buf); err != nil {
		os.Stdout.WriteString("1")
		return
	}
	for _, p := range []string{"/etc/passwd", "/proc/self/fd/3", inputPath} {
		if p == "" {
			continue
		}
		if f, err := os.Open(p); err == nil {
			f.Close()
			os.Stdout.WriteString("1")
			return
		}
	}
	if err := os.WriteFile("/tmp/lockfile-sandbox-probe", []byte("x"), 0o600); err == nil {
		os.Stdout.WriteString("1")
		return
	}
	os.Stdout.WriteString("0")
}
