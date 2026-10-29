//go:build unix

// Package idmigrate moves the files of the lockfile volumes from the
// account ids of the releases before 0.3 (10000-10002, the ones locksql
// uses) to lockfile's own (10030-10032). It is idempotent: it changes only
// the old ids and leaves every other owner alone. It stays for one release.
package idmigrate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// UIDs and GIDs map each old id to its new one: the console account
// lockfile (10001, with its group of the same id), the agent account
// (10002) and the group lockfile-clients (10000).
var (
	UIDs = map[uint32]uint32{10001: 10031, 10002: 10032}
	GIDs = map[uint32]uint32{10000: 10030, 10001: 10031, 10002: 10032}
)

// Dirs are the mount points of the volumes the image declares.
var Dirs = []string{"/run/lockfile", "/home/lockfile"}

// Lchown changes the owner of one file, not following a symlink; tests
// replace it.
var Lchown = os.Lchown

// statOwner reads the owner of p from its stat data; tests replace it.
var statOwner = defaultStatOwner

func defaultStatOwner(_ string, st *syscall.Stat_t) (uint32, uint32) { return st.Uid, st.Gid }

// Migrate walks each of dirs (a missing one is skipped) and gives every
// file owned by an old id its new one. It returns the number of files
// changed.
func Migrate(dirs []string) (int, error) {
	n := 0
	for _, d := range dirs {
		err := filepath.WalkDir(d, func(p string, de fs.DirEntry, err error) error {
			if err != nil {
				if p == d && errors.Is(err, fs.ErrNotExist) {
					return filepath.SkipDir
				}
				return err
			}
			fi, err := de.Info()
			if err != nil {
				return err
			}
			st, ok := fi.Sys().(*syscall.Stat_t)
			if !ok {
				return fmt.Errorf("%s: no owner", p)
			}
			ou, og := statOwner(p, st)
			uid, uok := UIDs[ou]
			gid, gok := GIDs[og]
			if !uok && !gok {
				return nil
			}
			if !uok {
				uid = ou
			}
			if !gok {
				gid = og
			}
			if err := Lchown(p, int(uid), int(gid)); err != nil {
				return err
			}
			n++
			return nil
		})
		if err != nil {
			return n, err
		}
	}
	return n, nil
}
