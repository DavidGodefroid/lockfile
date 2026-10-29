//go:build unix

package workspace

import "syscall"

// noFollow refuses to open a symbolic link in place of the fetched file.
const noFollow = syscall.O_NOFOLLOW
