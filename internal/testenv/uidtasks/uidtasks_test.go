//go:build unix

package uidtasks

import (
	"path/filepath"
	"testing"
	"time"
)

func locks(t *testing.T) (gate, budget string) {
	d := t.TempDir()
	return filepath.Join(d, "gate"), filepath.Join(d, "budget")
}

// lock acquires in the background; its channel gets the release once held.
func lock(t *testing.T, gate, budget string, exclusive bool) <-chan func() {
	t.Helper()
	c := make(chan func(), 1)
	go func() {
		r, err := acquire(gate, budget, exclusive)
		if err != nil {
			t.Error(err)
			return
		}
		c <- r
	}()
	return c
}

func got(t *testing.T, c <-chan func(), what string) func() {
	t.Helper()
	select {
	case r := <-c:
		return r
	case <-time.After(10 * time.Second):
		t.Fatalf("%s: never held", what)
		return nil
	}
}

func waiting(t *testing.T, c <-chan func(), what string) {
	t.Helper()
	select {
	case r := <-c:
		r()
		t.Fatalf("%s: held, want waiting", what)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestSharesCoexist(t *testing.T) {
	g, b := locks(t)
	r1 := got(t, lock(t, g, b, false), "first share")
	r2 := got(t, lock(t, g, b, false), "second share")
	r1()
	r2()
}

// An Exclusive waits for the Share holders, a Share asked for while it
// waits waits for it, and each goes on once the one before is released.
func TestExclusiveExcludesAndGoesFirst(t *testing.T) {
	g, b := locks(t)
	r1 := got(t, lock(t, g, b, false), "share")
	ex := lock(t, g, b, true)
	waiting(t, ex, "exclusive beside a share")
	later := lock(t, g, b, false)
	waiting(t, later, "share behind a waiting exclusive")
	r1()
	rx := got(t, ex, "exclusive")
	waiting(t, later, "share beside an exclusive")
	rx()
	got(t, later, "share after the exclusive")()
}

// A test that holds the budget keeps it: Exclusive after Share in the
// same test does not wait for itself.
func TestHoldOncePerTest(t *testing.T) {
	Share(t)
	Exclusive(t)
	if !held[t] {
		t.Fatal("not held")
	}
}
