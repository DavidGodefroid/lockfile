package procmount

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// ContainerFile declares, in an image, that the console runs in a
// container of its own. Docker cannot mount /proc with hidepid, so there
// lockfile accepts a /proc that lists every process of the console's pid
// namespace, provided that namespace holds no other account (Container).
const ContainerFile = "/etc/lockfile/container.toml"

// initPidNS is the inode of the initial pid namespace (PROC_PID_INIT_INO):
// a console there shares its processes with every account of the host.
const initPidNS = 0xEFFFFFFC

// ErrContainer is returned when a container's /proc cannot stand in for
// hidepid=invisible; it wraps the failed condition.
var ErrContainer = errors.New("not a private container")

// Container is the stand-in for hidepid=invisible in a container. All three
// conditions must hold: File is a root-owned declaration that others
// cannot write, in such a directory; the console is in a pid namespace other than the initial
// one, and Proc is that namespace's /proc; no process listed there runs
// under an account other than root and UID (the console's).
type Container struct {
	File string // ContainerFile
	Proc string // "/proc"
	PID  int    // this process, as its own pid namespace numbers it
	UID  int    // the console's account
	// Owner returns a file's owner uid (fileOwner).
	Owner func(fs.FileInfo) (uid int, ok bool)
}

// SystemContainer is the Container of this process.
func SystemContainer() Container {
	return Container{File: ContainerFile, Proc: "/proc", PID: os.Getpid(), UID: os.Getuid(), Owner: fileOwner}
}

// containerDecl is ContainerFile's content.
type containerDecl struct {
	// PidNamespace must be "private": the console's container has a pid
	// namespace of its own (never docker run --pid=host or
	// --pid=container:…).
	PidNamespace string `toml:"pid_namespace"`
}

// Declared reports whether File exists (whether this is meant to be a
// container), without checking it.
func (c Container) Declared() bool {
	_, err := os.Lstat(c.File)
	return !errors.Is(err, fs.ErrNotExist)
}

// Check returns nil when all three conditions hold, an error wrapping
// ErrContainer that names the first failed one otherwise.
func (c Container) Check() error {
	if err := c.checkFile(); err != nil {
		return fmt.Errorf("%w: %s: %v", ErrContainer, c.File, err)
	}
	if err := c.checkNamespace(); err != nil {
		return fmt.Errorf("%w: pid namespace: %v", ErrContainer, err)
	}
	if err := c.checkProcesses(); err != nil {
		return fmt.Errorf("%w: %v", ErrContainer, err)
	}
	return nil
}

func (c Container) checkFile() error {
	// The directory too: whoever may write it may replace the file.
	if di, err := os.Lstat(filepath.Dir(c.File)); err != nil {
		return err
	} else if uid, ok := c.Owner(di); !di.IsDir() || !ok || uid != 0 || di.Mode().Perm()&0o022 != 0 {
		return errors.New("its directory is not a root-owned directory closed to writes by others")
	}
	fi, err := os.Lstat(c.File)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return errors.New("not a regular file")
	}
	if uid, ok := c.Owner(fi); !ok || uid != 0 {
		return errors.New("not owned by root")
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return errors.New("writable by group or others")
	}
	raw, err := os.ReadFile(c.File)
	if err != nil {
		return err
	}
	var d containerDecl
	md, err := toml.Decode(string(raw), &d)
	if err != nil {
		return err
	}
	if und := md.Undecoded(); len(und) > 0 {
		return fmt.Errorf("unknown key %q", und[0].String())
	}
	if d.PidNamespace != "private" {
		return errors.New(`pid_namespace must be "private"`)
	}
	return nil
}

// checkNamespace refuses the initial pid namespace, and a Proc mounted for
// another namespace than this process's (its /proc/self would not be PID).
func (c Container) checkNamespace() error {
	link, err := os.Readlink(filepath.Join(c.Proc, "self", "ns", "pid"))
	if err != nil {
		return err
	}
	ino, ok := strings.CutPrefix(link, "pid:[")
	ino, ok2 := strings.CutSuffix(ino, "]")
	n, err := strconv.ParseUint(ino, 10, 64)
	if !ok || !ok2 || err != nil {
		return fmt.Errorf("unexpected %q", link)
	}
	if n == initPidNS {
		return errors.New("the console runs in the host's pid namespace (docker run --pid=host?)")
	}
	self, err := os.Readlink(filepath.Join(c.Proc, "self"))
	if err != nil {
		return err
	}
	if self != strconv.Itoa(c.PID) {
		return fmt.Errorf("%s belongs to another pid namespace", c.Proc)
	}
	return nil
}

// checkProcesses refuses any listed process (real, effective, saved or
// filesystem uid) of an account other than root and UID.
func (c Container) checkProcesses() error {
	ents, err := os.ReadDir(c.Proc)
	if err != nil {
		return err
	}
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join(c.Proc, e.Name(), "status"))
		if errors.Is(err, fs.ErrNotExist) {
			continue // exited since the listing
		}
		if err != nil {
			return fmt.Errorf("process %d: %v", pid, err)
		}
		uids, ok := statusUIDs(string(b))
		if !ok {
			return fmt.Errorf("process %d: no Uid line", pid)
		}
		for _, u := range uids {
			if u != 0 && u != c.UID {
				return fmt.Errorf("process %d runs as uid %d, which could read the plugins' /proc entries (share no pid namespace with the agent: never --pid=host or --pid=container:…)", pid, u)
			}
		}
	}
	return nil
}

// statusUIDs returns the four uids of a /proc/<pid>/status "Uid:" line.
func statusUIDs(status string) ([]int, bool) {
	for _, line := range strings.Split(status, "\n") {
		rest, ok := strings.CutPrefix(line, "Uid:")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) != 4 {
			return nil, false
		}
		out := make([]int, 4)
		for i, s := range f {
			n, err := strconv.Atoi(s)
			if err != nil {
				return nil, false
			}
			out[i] = n
		}
		return out, true
	}
	return nil, false
}
