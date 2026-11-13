// Answers 1 if its typed values reach it on fd 6 only: fd 6 is readable
// and refuses writes and resizes, argv and the environment do not hold
// the value, and fd 7 is open only as the build flag wantDetail says.
package main

import (
	"encoding/json"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// wantDetail is "1" when the test gives the plugin a detail fd.
var wantDetail = "0"

func main() {
	b, err := io.ReadAll(os.NewFile(6, "inputs"))
	var in map[string]string
	if err != nil || json.Unmarshal(b, &in) != nil || in["needle"] == "" {
		os.Stdout.WriteString("0")
		return
	}
	v := in["needle"]
	ok := len(os.Args) == 1 && os.Args[0] == "plugin"
	for _, e := range os.Environ() {
		ok = ok && e == "LOCKFILE_PLUGIN_PROTOCOL=2" && !strings.Contains(e, v)
	}
	if _, err := unix.Write(6, []byte("x")); err == nil {
		ok = false
	}
	if err := unix.Ftruncate(6, 0); err == nil {
		ok = false
	}
	if _, err := unix.Pwrite(6, []byte("x"), 0); err == nil {
		ok = false
	}
	// Without a detail fd the Go runtime may hold fd 7 itself: only a
	// pipe there is the console's.
	var st unix.Stat_t
	pipe := unix.Fstat(7, &st) == nil && st.Mode&unix.S_IFMT == unix.S_IFIFO
	if pipe != (wantDetail == "1") {
		ok = false
	}
	if ok {
		os.Stdout.WriteString("1")
		return
	}
	os.Stdout.WriteString("0")
}
