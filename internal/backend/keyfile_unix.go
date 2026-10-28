//go:build unix

package backend

import (
	"io"
	"os"
	"syscall"

	"github.com/DavidGodefroid/lockkit/secrets"
)

// readKeyFile reads the private key at path ("~/" is the lockfile
// account's home) without following a symlink, refusing a key others may
// read.
func readKeyFile(path string) ([]byte, error) {
	path, err := expandHome(path)
	if err != nil {
		return nil, errKeyFile
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, errKeyFile
	}
	f := os.NewFile(uintptr(fd), "key")
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm()&0o077 != 0 || fi.Size() > maxKeyFile {
		return nil, errKeyFile
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Geteuid() {
		return nil, errKeyFile
	}
	b, err := io.ReadAll(io.LimitReader(f, maxKeyFile+1))
	if err != nil || len(b) > maxKeyFile {
		secrets.Wipe(b)
		return nil, errKeyFile
	}
	return b, nil
}
