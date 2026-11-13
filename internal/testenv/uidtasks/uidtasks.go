// Package uidtasks serializes the tests that exhaust their account's task
// budget with every other test that runs a plugin, across test binaries.
//
// The sandbox sets a plugin's RLIMIT_NPROC to the tasks its uid runs when
// it starts plus a fixed allowance, and the kernel counts every task of the
// uid against it. A test whose plugin takes the whole allowance (the
// sandbox's thread cap) therefore leaves a plugin started just before it,
// in any test binary of the same account (go test ./... runs packages in
// parallel), without threads: its Go runtime dies on clone's EAGAIN (exit
// 2) and the answer becomes a plugin_error. Tests that run plugins hold
// Share; a test that takes the allowance holds Exclusive.
package uidtasks

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

var (
	mu   sync.Mutex
	held = map[testing.TB]bool{}
)

// Share holds the account's task budget shared until t ends: no Exclusive
// holder runs meanwhile. A test that already holds the budget keeps it.
func Share(t testing.TB) { hold(t, false) }

// Exclusive holds the account's task budget alone until t ends: it waits
// for the Share holders of every process of the account, and a Share asked
// for meanwhile waits for it.
func Exclusive(t testing.TB) { hold(t, true) }

func hold(t testing.TB, exclusive bool) {
	t.Helper()
	mu.Lock()
	if held[t] {
		mu.Unlock()
		return
	}
	held[t] = true
	mu.Unlock()
	release, err := acquire(path("gate"), path("budget"), exclusive)
	if err != nil {
		t.Fatalf("uidtasks: %v", err)
	}
	t.Cleanup(func() {
		release()
		mu.Lock()
		delete(held, t)
		mu.Unlock()
	})
}

// path is a lock file shared by every test binary of this account.
func path(name string) string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("lockfile-test-uidtasks-%d.%s", os.Getuid(), name))
}
