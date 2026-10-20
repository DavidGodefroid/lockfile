// Package procmount checks that /proc hides other accounts' processes.
//
// While a plugin runs, its /proc/<pid> entries (cmdline, comm, the names
// and number of its threads, its memory counters) are written by the
// plugin and readable by every account unless /proc is mounted with
// hidepid=invisible (hidepid=2). The agent account could poll them: a
// covert channel seccomp cannot close, since the plugin writes them from
// inside its own process. A pid namespace would not help: the parent
// namespace still lists the plugin. So lockfile requires the mount option.
package procmount

import (
	"errors"
	"os"
	"strings"
)

// Fix is the remedy, for the human: the remount and the fstab line.
const Fix = "remount /proc with hidepid=invisible as root: " +
	"mount -o remount,hidepid=invisible,gid=<admin group> /proc " +
	"and make it permanent in /etc/fstab: " +
	"proc /proc proc defaults,hidepid=invisible,gid=<admin group> 0 0 " +
	"(gid= names a group whose members still see every process, for monitoring tools)"

// ErrNotHidden is returned when /proc shows other accounts' processes.
var ErrNotHidden = errors.New("/proc is not mounted with hidepid=invisible")

// ErrNoProc is returned when no proc mount is found at /proc.
var ErrNoProc = errors.New("no proc filesystem mounted at /proc")

// Hidden reports whether the proc filesystem mounted at /proc (the last
// one, if mounted over) has hidepid=invisible or its numeric spelling
// hidepid=2. table is /proc/self/mountinfo or /proc/mounts.
func Hidden(table string) (bool, error) {
	found, hidden := false, false
	for _, line := range strings.Split(table, "\n") {
		point, fstype, opts, ok := parseLine(line)
		if !ok || point != "/proc" || fstype != "proc" {
			continue
		}
		found, hidden = true, false
		for _, o := range strings.Split(opts, ",") {
			if o == "hidepid=invisible" || o == "hidepid=2" {
				hidden = true
			}
		}
	}
	if !found {
		return false, ErrNoProc
	}
	return hidden, nil
}

// Mount is one line of a mount table.
type Mount struct {
	Point, FSType, Options string
}

// Mounts parses a mount table (mountinfo or /proc/mounts), in order.
func Mounts(table string) []Mount {
	var out []Mount
	for _, line := range strings.Split(table, "\n") {
		if point, fstype, opts, ok := parseLine(line); ok {
			out = append(out, Mount{point, fstype, opts})
		}
	}
	return out
}

// MountOf returns the mount holding path (absolute, symlinks resolved):
// the last mount whose point is path or one of its parents.
func MountOf(table, path string) (Mount, bool) {
	var best Mount
	found := false
	for _, m := range Mounts(table) {
		if m.Point == path || m.Point == "/" || strings.HasPrefix(path, m.Point+"/") {
			if !found || len(m.Point) >= len(best.Point) {
				best, found = m, true
			}
		}
	}
	return best, found
}

// parseLine returns the mount point, filesystem type and options of one
// line of mountinfo (fields: id parent dev root point opts [tags] -
// fstype source superopts) or of /proc/mounts (source point fstype opts
// freq passno). For mountinfo the super block options are returned, where
// proc reports hidepid.
func parseLine(line string) (point, fstype, opts string, ok bool) {
	f := strings.Fields(line)
	for i, x := range f {
		if x == "-" && i >= 6 && len(f) >= i+4 {
			return unescape(f[4]), f[i+1], f[i+3], true
		}
	}
	if len(f) == 6 {
		return unescape(f[1]), f[2], f[3], true
	}
	return "", "", "", false
}

// unescape decodes the octal escapes (\040 for a space) of mount tables.
func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) && isOctal(s[i+1]) && isOctal(s[i+2]) && isOctal(s[i+3]) {
			b.WriteByte((s[i+1]-'0')<<6 | (s[i+2]-'0')<<3 | (s[i+3] - '0'))
			i += 3
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isOctal(c byte) bool { return c >= '0' && c <= '7' }

// Check reads a mount table with read and returns nil when /proc hides
// other accounts' processes, ErrNotHidden or ErrNoProc otherwise, or the
// read error.
func Check(read func() ([]byte, error)) error {
	b, err := read()
	if err != nil {
		return err
	}
	hidden, err := Hidden(string(b))
	if err != nil {
		return err
	}
	if !hidden {
		return ErrNotHidden
	}
	return nil
}

// ReadMountinfo reads this process's mount table.
func ReadMountinfo() ([]byte, error) { return os.ReadFile("/proc/self/mountinfo") }

// CheckSystem checks this process's /proc.
func CheckSystem() error { return Check(ReadMountinfo) }
