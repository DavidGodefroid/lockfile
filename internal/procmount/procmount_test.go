package procmount

import (
	"errors"
	"testing"
)

func TestHidden(t *testing.T) {
	const (
		open      = "22 1 0:21 / /proc rw,nosuid,nodev,noexec,relatime shared:12 - proc proc rw\n"
		invisible = "22 1 0:21 / /proc rw,nosuid,nodev,noexec,relatime shared:12 - proc proc rw,hidepid=invisible,gid=27\n"
		numeric   = "22 1 0:21 / /proc rw,relatime - proc proc rw,hidepid=2\n"
		noaccess  = "22 1 0:21 / /proc rw,relatime - proc proc rw,hidepid=noaccess\n"
		other     = "30 22 0:40 / /proc/sys/fs/binfmt_misc rw,relatime shared:13 - autofs systemd-1 rw,fd=32\n" +
			"40 1 0:5 / /dev rw,nosuid shared:2 - devtmpfs udev rw,hidepid=2\n"
	)
	cases := []struct {
		name   string
		table  string
		hidden bool
		err    error
	}{
		{"mountinfo open", open + other, false, nil},
		{"mountinfo invisible", invisible + other, true, nil},
		{"mountinfo numeric", numeric, true, nil},
		{"noaccess is not enough", noaccess, false, nil},
		{"mounts invisible", "proc /proc proc rw,nosuid,nodev,noexec,relatime,hidepid=invisible,gid=27 0 0\n", true, nil},
		{"mounts numeric", "proc /proc proc rw,relatime,hidepid=2 0 0\n", true, nil},
		{"mounts open", "proc /proc proc rw,relatime 0 0\nsysfs /sys sysfs rw 0 0\n", false, nil},
		{"last mount wins (hidden then open)", invisible + open, false, nil},
		{"last mount wins (open then hidden)", open + invisible, true, nil},
		{"option on another mount only", other, false, ErrNoProc},
		{"hidepid in the per-mount field only", "22 1 0:21 / /proc rw,hidepid=2 - proc proc rw\n", false, nil},
		{"not proc at /proc", "22 1 0:21 / /proc rw - tmpfs tmpfs rw,hidepid=2\n", false, ErrNoProc},
		{"escaped mount point", `22 1 0:21 / /pro\143 rw - proc proc rw,hidepid=2` + "\n", true, nil},
		{"empty", "", false, ErrNoProc},
		{"garbage", "x y z\n", false, ErrNoProc},
	}
	for _, c := range cases {
		hidden, err := Hidden(c.table)
		if hidden != c.hidden || !errors.Is(err, c.err) {
			t.Errorf("%s: Hidden = %v, %v; want %v, %v", c.name, hidden, err, c.hidden, c.err)
		}
	}
}

func TestCheck(t *testing.T) {
	read := func(s string, err error) func() ([]byte, error) {
		return func() ([]byte, error) { return []byte(s), err }
	}
	if err := Check(read("proc /proc proc rw,hidepid=invisible 0 0\n", nil)); err != nil {
		t.Errorf("hidden: %v", err)
	}
	if err := Check(read("proc /proc proc rw 0 0\n", nil)); !errors.Is(err, ErrNotHidden) {
		t.Errorf("open: %v", err)
	}
	boom := errors.New("boom")
	if err := Check(read("", boom)); !errors.Is(err, boom) {
		t.Errorf("read error: %v", err)
	}
}

func TestMountOf(t *testing.T) {
	table := "1 0 0:1 / / rw - ext4 /dev/sda1 rw\n" +
		"2 1 0:2 / /srv rw - nfs4 server:/export rw\n" +
		"3 2 0:3 / /srv/local rw - ext4 /dev/sdb1 rw\n" +
		`4 1 0:4 / /mnt/my\040share rw - cifs //server/share rw` + "\n"
	for path, want := range map[string]string{
		"/":               "ext4",
		"/home/x":         "ext4",
		"/srv":            "nfs4",
		"/srv/data/work":  "nfs4",
		"/srv/local/work": "ext4",
		"/srvx/work":      "ext4",
		"/mnt/my share/w": "cifs",
	} {
		m, ok := MountOf(table, path)
		if !ok || m.FSType != want {
			t.Errorf("MountOf(%q) = %+v %v, want %s", path, m, ok, want)
		}
	}
	if _, ok := MountOf("", "/x"); ok {
		t.Errorf("empty table found a mount")
	}
}
