package pluginspec

import (
	"strings"
	"testing"
)

const inputsManifest = `
name = "pdf.contains"
version = "1.0.0"
protocol = 2
question = "Does the extracted text of the PDF contain ${needle}?"
one  = "the text contains the value"
zero = "the text does not contain the value"
accepts = ["application/pdf"]
inputs = [{ name = "needle", kind = "text", max_bytes = 256 }]
detail = true
limits = { cpu_s = 20, memory = "512MiB", timeout_s = 30 }
`

func TestParseManifestInputs(t *testing.T) {
	m, err := ParseManifest([]byte(inputsManifest))
	if err != nil {
		t.Fatal(err)
	}
	if m.Protocol != 2 || !m.Detail || len(m.Inputs) != 1 || m.Inputs[0] != (Input{Name: "needle", Kind: KindText, MaxBytes: 256}) {
		t.Fatalf("manifest %+v", m)
	}
	if in, ok := m.Input("needle"); !ok || in.MaxBytes != 256 {
		t.Fatalf("Input(needle) = %+v %v", in, ok)
	}
	// Protocol 2 without inputs nor detail is a plain plugin.
	plain := strings.Replace(strings.Replace(inputsManifest, "inputs = [{ name = \"needle\", kind = \"text\", max_bytes = 256 }]\n", "", 1), "detail = true\n", "", 1)
	plain = strings.Replace(plain, "${needle}", "a value", 1)
	if _, err := ParseManifest([]byte(plain)); err != nil {
		t.Fatalf("protocol 2, no inputs: %v", err)
	}
}

func TestParseManifestInputsRejects(t *testing.T) {
	in := `inputs = [{ name = "needle", kind = "text", max_bytes = 256 }]`
	cases := map[string][2]string{
		"inputs on protocol 1":    {`protocol = 2`, `protocol = 1`},
		"unknown kind":            {in, `inputs = [{ name = "needle", kind = "regex", max_bytes = 256 }]`},
		"max_bytes 0":             {in, `inputs = [{ name = "needle", kind = "text", max_bytes = 0 }]`},
		"max_bytes 1025":          {in, `inputs = [{ name = "needle", kind = "text", max_bytes = 1025 }]`},
		"bad name":                {in, `inputs = [{ name = "Needle", kind = "text", max_bytes = 256 }]`},
		"name too long":           {in, `inputs = [{ name = "` + strings.Repeat("a", 33) + `", kind = "text", max_bytes = 256 }]`},
		"unknown input key":       {in, `inputs = [{ name = "needle", kind = "text", max_bytes = 256, default = "x" }]`},
		"input not in question":   {`contain ${needle}?`, `contain a value?`},
		"stray placeholder":       {`contain ${needle}?`, `contain ${needle} or ${other}?`},
		"unclosed placeholder":    {`contain ${needle}?`, `contain ${needle} ${oops?`},
		"empty placeholder":       {`contain ${needle}?`, `contain ${needle} ${}?`},
		"declared twice":          {in, `inputs = [{ name = "needle", kind = "text", max_bytes = 256 }, { name = "needle", kind = "email", max_bytes = 256 }]`},
		"five inputs":             {in, `inputs = [{ name = "needle", kind = "text", max_bytes = 1 }, { name = "b", kind = "text", max_bytes = 1 }, { name = "c", kind = "text", max_bytes = 1 }, { name = "d", kind = "text", max_bytes = 1 }, { name = "e", kind = "text", max_bytes = 1 }]`},
		"params still unknown":    {`detail = true`, "detail = true\nparams = [\"x\"]"},
		"other key still refused": {`detail = true`, "detail = true\nsecret = 1"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			src := strings.Replace(inputsManifest, c[0], c[1], 1)
			if src == inputsManifest {
				t.Fatalf("replacement %q not applied", c[0])
			}
			if _, err := ParseManifest([]byte(src)); err == nil {
				t.Fatalf("accepted:\n%s", src)
			}
		})
	}
	// detail alone on protocol 1 is refused too.
	p1 := strings.Replace(validManifest, "limits = {", "detail = false\nlimits = {", 1)
	if _, err := ParseManifest([]byte(p1)); err == nil {
		t.Fatal("detail accepted on protocol 1")
	}
}

func TestCheckValue(t *testing.T) {
	for _, c := range []struct {
		in   Input
		v    string
		good bool
	}{
		{Input{Kind: KindText, MaxBytes: 8}, "abc def", true},
		{Input{Kind: KindText, MaxBytes: 3}, "abcd", false},
		{Input{Kind: KindText, MaxBytes: 8}, "", false},
		{Input{Kind: KindText, MaxBytes: 8}, "a\nb", false},
		{Input{Kind: KindText, MaxBytes: 8}, "\xff\xfe", false},
		{Input{Kind: KindEmail, MaxBytes: 64}, "alice@example.org", true},
		{Input{Kind: KindEmail, MaxBytes: 64}, "alice", false},
		{Input{Kind: KindEmail, MaxBytes: 64}, "a b@example.org", false},
		{Input{Kind: KindDigits, MaxBytes: 16}, "85073012345", true},
		{Input{Kind: KindDigits, MaxBytes: 16}, "85.07.30", false},
	} {
		err := c.in.CheckValue([]byte(c.v))
		if (err == nil) != c.good {
			t.Errorf("%+v %q: %v", c.in, c.v, err)
		}
		if err != nil && c.v != "" && strings.Contains(err.Error(), c.v) {
			t.Errorf("the error quotes the value: %v", err)
		}
	}
}

func TestPlaceholders(t *testing.T) {
	got, err := Placeholders("is ${a} in ${b_2} or ${a}?")
	if err != nil || strings.Join(got, ",") != "a,b_2" {
		t.Fatalf("%v %v", got, err)
	}
	for _, s := range []string{"${", "${A}", "${a", "${a-b}"} {
		if _, err := Placeholders(s); err == nil {
			t.Errorf("%q accepted", s)
		}
	}
}
