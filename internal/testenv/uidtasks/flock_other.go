//go:build !unix

package uidtasks

// acquire holds nothing: plugins run only where the sandbox does (Linux).
func acquire(_, _ string, _ bool) (release func(), err error) { return func() {}, nil }
