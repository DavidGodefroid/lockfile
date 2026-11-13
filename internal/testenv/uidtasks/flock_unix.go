//go:build unix

package uidtasks

import (
	"os"

	"golang.org/x/sys/unix"
)

// acquire locks budget, shared or exclusive, through gate: a holder of
// gate is the only one waiting for budget, so a waiting Exclusive keeps
// later Share holders out (no writer starvation).
func acquire(gate, budget string, exclusive bool) (release func(), err error) {
	g, err := lockFile(gate, unix.LOCK_EX)
	if err != nil {
		return nil, err
	}
	how := unix.LOCK_SH
	if exclusive {
		how = unix.LOCK_EX
	}
	b, err := lockFile(budget, how)
	g.Close()
	if err != nil {
		return nil, err
	}
	return func() { b.Close() }, nil
}

// lockFile opens p (its own open file description) and flocks it how; the
// lock goes with the file's close.
func lockFile(p string, how int) (*os.File, error) {
	f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		err = unix.Flock(int(f.Fd()), how)
		if err != unix.EINTR {
			break
		}
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
