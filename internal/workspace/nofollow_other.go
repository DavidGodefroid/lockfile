//go:build !unix

package workspace

// noFollow is 0 where the system has no O_NOFOLLOW: lockfile runs in
// Docker, on Linux; this file only keeps the package building elsewhere
// (GOOS=windows go vet).
const noFollow = 0
