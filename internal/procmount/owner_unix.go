//go:build unix

package procmount

import (
	"io/fs"
	"syscall"
)

// fileOwner returns the owner of a file.
func fileOwner(fi fs.FileInfo) (uid int, ok bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(st.Uid), true
}
