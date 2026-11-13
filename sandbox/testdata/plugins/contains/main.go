// Answers 1 if its input contains the value the human typed for its
// input "needle" (read on fd 6), and writes where on fd 7 when the console
// gave it one: the protocol-2 test plugin.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

func main() {
	var in struct {
		Needle string `json:"needle"`
	}
	b, err := io.ReadAll(os.NewFile(6, "inputs"))
	if err != nil || json.Unmarshal(b, &in) != nil || in.Needle == "" {
		os.Exit(2)
	}
	data, err := io.ReadAll(os.NewFile(3, "input"))
	if err != nil {
		os.Exit(2)
	}
	i := bytes.Index(data, []byte(in.Needle))
	// fd 7 is the console's detail pipe only when it is a pipe: without
	// one, the Go runtime may have opened fd 7 for itself.
	var st unix.Stat_t
	if unix.Fstat(7, &st) == nil && st.Mode&unix.S_IFMT == unix.S_IFIFO {
		d := os.NewFile(7, "detail")
		if i >= 0 {
			fmt.Fprintf(d, "found at byte %d\x1b[2J", i)
		} else {
			fmt.Fprint(d, "not found")
		}
		d.Close()
	}
	if i >= 0 {
		os.Stdout.WriteString("1")
		return
	}
	os.Stdout.WriteString("0")
}
