// Answers 1 if it could change any file metadata: permissions, owner,
// extended attributes, times or size, by path or through its input fd.
package main

import (
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// inputPath is set with -ldflags -X to the input's path on the host.
var inputPath string

func main() {
	path := inputPath
	if p, err := os.Readlink("/proc/self/fd/3"); err == nil && p != "" {
		path = p
	}
	dir := filepath.Dir(path)
	tries := []func() error{
		func() error { return os.Chmod(path, 0o644) },
		func() error { return os.Chmod(dir, 0o755) },
		func() error { return syscall.Fchmod(3, 0o644) },
		func() error { return os.Chown(path, os.Getuid(), os.Getgid()) },
		func() error { return syscall.Fchown(3, os.Getuid(), os.Getgid()) },
		func() error { return syscall.Setxattr(path, "user.sample", []byte("x"), 0) },
		func() error { return os.Chtimes(path, time.Unix(1, 0), time.Unix(1, 0)) },
		func() error { return os.Truncate(path, 0) },
	}
	for _, try := range tries {
		if try() == nil {
			os.Stdout.WriteString("1")
			return
		}
	}
	os.Stdout.WriteString("0")
}
