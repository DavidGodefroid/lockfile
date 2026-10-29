//go:build unix

package main

import (
	"io/fs"
	"syscall"
)

// fileOwner returns the owner of fi from the system's stat data.
func fileOwner(fi fs.FileInfo) (uid, gid int, ok bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return int(st.Uid), int(st.Gid), true
}
