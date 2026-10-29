//go:build !unix

package main

import "io/fs"

// fileOwner reports no owner where the system has no Unix stat data, as
// fileowner_unix.go does for a file without it. lockfile runs in Docker, on Linux;
// this file only keeps the package building elsewhere (GOOS=windows go vet).
func fileOwner(fs.FileInfo) (uid, gid int, ok bool) { return 0, 0, false }
