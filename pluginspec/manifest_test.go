package pluginspec

import (
	"strings"
	"testing"
	"time"
)

const validManifest = `
name = "pdf.has-blank-page"
version = "1.0.0"
protocol = 1
question = "Does the PDF have at least one page that paints nothing visible?"
one  = "at least one page has no painting operator, or only full-page white fills"
zero = "every page paints something"
accepts = ["application/pdf"]
limits = { cpu_s = 20, memory = "512MiB", timeout_s = 30 }
`

func TestParseManifestValid(t *testing.T) {
	m, err := ParseManifest([]byte(validManifest))
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "pdf.has-blank-page" || m.Version != "1.0.0" || m.Protocol != 1 {
		t.Fatalf("identity: %+v", m)
	}
	if len(m.Accepts) != 1 || m.Accepts[0] != "application/pdf" {
		t.Fatalf("accepts: %v", m.Accepts)
	}
	if m.Limits.CPUSeconds != 20 || m.Limits.Memory != 512<<20 || m.Limits.Timeout != 30*time.Second {
		t.Fatalf("limits: %+v", m.Limits)
	}
}

func TestParseManifestRejects(t *testing.T) {
	cases := map[string][2]string{
		"unknown key params": {`limits = {`, "params = [\"x\"]\nlimits = {"},
		"protocol 2":         {`protocol = 1`, `protocol = 2`},
		"empty question":     {`question = "Does the PDF have at least one page that paints nothing visible?"`, `question = ""`},
		"empty one":          {`one  = "at least one page has no painting operator, or only full-page white fills"`, `one = ""`},
		"empty zero":         {`zero = "every page paints something"`, `zero = ""`},
		"one equals zero":    {`zero = "every page paints something"`, `zero = "at least one page has no painting operator, or only full-page white fills"`},
		"bad name":           {`name = "pdf.has-blank-page"`, `name = "Blank"`},
		"name no dot":        {`name = "pdf.has-blank-page"`, `name = "pdf"`},
		"bad version":        {`version = "1.0.0"`, `version = "1.0"`},
		"version v prefix":   {`version = "1.0.0"`, `version = "v1.0.0"`},
		"empty accepts":      {`accepts = ["application/pdf"]`, `accepts = []`},
		"unknown media type": {`accepts = ["application/pdf"]`, `accepts = ["text/plain"]`},
		"timeout too long":   {`timeout_s = 30`, `timeout_s = 121`},
		"memory too big":     {`memory = "512MiB"`, `memory = "3GiB"`},
		"bad memory":         {`memory = "512MiB"`, `memory = "lots"`},
		"unknown limit key":  {`timeout_s = 30 }`, `timeout_s = 30, net = 1 }`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			src := strings.Replace(validManifest, c[0], c[1], 1)
			if src == validManifest {
				t.Fatalf("replacement %q not applied", c[0])
			}
			if _, err := ParseManifest([]byte(src)); err == nil {
				t.Fatalf("accepted:\n%s", src)
			}
		})
	}
}
