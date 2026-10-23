//go:build !unix

package procmount

import "io/fs"

// fileOwner cannot tell the owner here: the container check fails.
func fileOwner(fs.FileInfo) (uid int, ok bool) { return 0, false }
