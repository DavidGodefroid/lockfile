//go:build unix

package plugin

import "syscall"

// noFollow refuses to open a symbolic link in place of the file.
const noFollow = syscall.O_NOFOLLOW
