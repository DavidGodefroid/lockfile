//go:build !unix

package backend

// readKeyFile refuses every key where the system cannot open it without
// following a symbolic link nor tell its owner: lockfile runs in Docker, on
// Linux; this file only keeps the package building elsewhere (GOOS=windows
// go vet).
func readKeyFile(string) ([]byte, error) { return nil, errKeyFile }
