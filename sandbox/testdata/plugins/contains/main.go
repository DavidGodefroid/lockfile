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
	if _, err := unix.FcntlInt(7, unix.F_GETFD, 0); err == nil {
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
