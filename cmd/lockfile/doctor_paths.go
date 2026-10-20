package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/DavidGodefroid/lockfile/internal/procmount"
)

// fileOwner returns the owner of fi from the system's stat data.
func fileOwner(fi fs.FileInfo) (uid, gid int, ok bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return int(st.Uid), int(st.Gid), true
}

// sharedFS are filesystem types whose files other machines, or other
// clients of the share, may read.
var sharedFS = map[string]bool{
	"nfs": true, "nfs4": true, "cifs": true, "smb3": true, "smbfs": true, "9p": true, "virtiofs": true,
	"ceph": true, "glusterfs": true, "lustre": true, "gpfs": true, "afs": true,
	"fuse.sshfs": true, "fuse.rclone": true, "fuse.s3fs": true,
}

// workDirFindings checks the work directory: its mode, its parents (other
// accounts may not traverse them, nor may it sit under a world-writable
// directory such as /tmp) and the filesystem it is on. table is the mount
// table ("" when unreadable). No detail names the path.
func workDirFindings(work, table string) []finding {
	var out []finding
	add := func(status, detail string) { out = append(out, finding{"work dir", status, detail}) }
	fi, err := os.Stat(work)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		add("ok", "not created yet: the console creates it 0700 at start")
	case err != nil:
		add("warn", "cannot check the work directory")
	case fi.Mode().Perm()&0o077 != 0:
		add("fail", fmt.Sprintf("the work directory has mode %04o: other accounts may reach fetched files; chmod 0700 it", fi.Mode().Perm()))
	default:
		add("ok", "0700")
	}

	closed, underTmp := false, false
	existing := ""
	for p := filepath.Dir(filepath.Clean(work)); ; p = filepath.Dir(p) {
		if fi, err := os.Stat(p); err == nil {
			if existing == "" {
				existing = p
			}
			perm := fi.Mode().Perm()
			// Above a closed directory nothing is reachable any more.
			if !closed && fi.Mode()&fs.ModeSticky != 0 && perm&0o002 != 0 {
				underTmp = true
			}
			if perm&0o011 == 0 {
				closed = true
			}
		}
		if p == "/" || p == "." {
			break
		}
	}
	if underTmp {
		add("warn", "the work directory sits under a world-writable directory (such as /tmp): keep work_dir under the console account's home")
	}
	if !closed {
		add("warn", "other accounts can traverse the work directory's parents and watch it change: keep work_dir under the console account's home (0700)")
	}

	if table == "" {
		return out
	}
	if _, err := os.Stat(work); err == nil {
		existing = work
	}
	if real, err := filepath.EvalSymlinks(existing); err == nil {
		if m, ok := procmount.MountOf(table, real); ok && sharedFS[m.FSType] {
			add("warn", "the work directory is on a network or shared filesystem ("+m.FSType+"): fetched files would be readable beyond this machine's accounts; use a local disk")
		}
	}
	return out
}

// reachableByOthers reports whether accounts other than the owner can
// reach files under dir: every directory from / to dir lets "other", or
// the clients group (clientGID, -1 for none), traverse it. Traversal is
// enough, the agent knows the keys it asks for. Other groups the agent
// may belong to are not known from the console account.
func reachableByOthers(dir string, clientGID int, owner func(fs.FileInfo) (int, int, bool)) (bool, error) {
	if _, err := os.Stat(dir); err != nil {
		return false, err
	}
	for p := filepath.Clean(dir); ; p = filepath.Dir(p) {
		fi, err := os.Stat(p)
		if err != nil {
			return false, err
		}
		perm := fi.Mode().Perm()
		_, gid, ok := owner(fi)
		group := ok && clientGID >= 0 && gid == clientGID && perm&0o010 != 0
		if perm&0o001 == 0 && !group {
			return false, nil
		}
		if p == "/" || p == "." {
			return true, nil
		}
	}
}
