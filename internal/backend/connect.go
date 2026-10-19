package backend

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/DavidGodefroid/lockkit/console"
	"github.com/DavidGodefroid/lockkit/secrets"

	"github.com/DavidGodefroid/lockfile/internal/profile"
	"github.com/DavidGodefroid/lockfile/internal/source"
)

// session is a connected source: what console.Session is for lockfile.
// The workspace, the plugin store and the plans are the backend's, so they
// outlive a reconnection.
type session struct {
	src source.Source
}

func (s *session) Close() error { return nil }

// maxKeyFile bounds a private key file.
const maxKeyFile = 64 << 10

// errKeyFile is the error of an unusable private key file: it names the
// option, never the path (shown on the console window).
var errKeyFile = errors.New("sftp: the private key file (key) must be a regular file of the lockfile account, mode 0600 or stricter, under 64 KiB")

// connect opens the source of p with the credentials the console obtained
// (design §4, plan Call 4). The secret is the console's: it wipes it once
// Connect returns, so nothing here keeps a reference to it beyond what the
// source client copied.
func connect(p profile.Profile, c console.Credentials) (*session, error) {
	switch p.Source {
	case profile.SourceS3:
		if c.User == "" || len(c.Secret) == 0 {
			return nil, errors.New("s3: an access key id (user) and a secret access key are required")
		}
		src, err := source.NewS3(p.S3, source.S3Creds{AccessKeyID: c.User, SecretAccessKey: string(c.Secret)})
		if err != nil {
			return nil, err
		}
		return &session{src: src}, nil
	case profile.SourceSFTP:
		creds := source.SFTPCreds{}
		if p.SFTPAuth == profile.AuthPassword {
			if len(c.Secret) == 0 {
				return nil, errors.New("sftp: no password")
			}
			creds.Password = string(c.Secret)
		} else {
			key, err := readKeyFile(p.SFTPKey)
			if err != nil {
				return nil, err
			}
			defer secrets.Wipe(key)
			creds.PrivateKey, creds.Passphrase = key, c.Secret
		}
		src, err := source.NewSFTP(p.SFTP, creds)
		if err != nil {
			return nil, err
		}
		return &session{src: src}, nil
	case profile.SourceLocal:
		src, err := source.NewLocal(p.LocalPath)
		if err != nil {
			return nil, err
		}
		return &session{src: src}, nil
	}
	return nil, fmt.Errorf("unknown source %q", p.Source)
}

// readKeyFile reads the private key at path ("~/" is the lockfile
// account's home) without following a symlink, refusing a key others may
// read.
func readKeyFile(path string) ([]byte, error) {
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, errKeyFile
		}
		path = filepath.Join(home, rest)
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
