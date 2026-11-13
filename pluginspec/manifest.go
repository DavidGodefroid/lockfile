package pluginspec

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/BurntSushi/toml"
)

// ProtocolVersion is the newest plugin protocol this package knows.
// Protocol 1 plugins take no input; protocol 2 adds the inputs the human
// types (Manifest.Inputs, fd 6) and the human-only detail (fd 7).
const ProtocolVersion = 2

// Bounds on a protocol-2 manifest's inputs and detail.
const (
	MaxInputs     = 4
	MaxInputBytes = 1024
	// MaxDetail is how much of a plugin's detail (fd 7) is kept.
	MaxDetail = 4 << 10
)

// Input kinds: the closed list of what an input may hold.
const (
	KindText   = "text"
	KindEmail  = "email"
	KindDigits = "digits"
)

// Bounds on what a manifest may ask for.
const (
	MaxTimeout = 120 * time.Second
	MaxMemory  = 2 << 30
)

// MediaTypes is the closed list of values allowed in a manifest's accepts.
var MediaTypes = map[string]bool{
	"*/*":             true,
	"application/pdf": true,
	"application/zip": true,
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": true,
}

var (
	nameRe    = regexp.MustCompile(`^[a-z0-9]+(\.[a-z0-9-]+)+$`)
	versionRe = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	// inputNameRe is an input's name, and a placeholder's (${name}).
	inputNameRe = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)
	emailRe     = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s.]+$`)
	digitsRe    = regexp.MustCompile(`^[0-9]+$`)
)

// Limits are the resources a plugin run may use.
type Limits struct {
	CPUSeconds int
	Memory     int64 // private writable memory (RLIMIT_DATA), bytes
	Timeout    time.Duration
}

// Input is a value the human types for a protocol-2 plugin: the agent
// binds it to a placeholder, never to a value of its own.
type Input struct {
	Name     string
	Kind     string
	MaxBytes int
}

// Manifest is a plugin's plugin.toml.
type Manifest struct {
	Name     string
	Version  string
	Protocol int
	Question string
	One      string
	Zero     string
	Accepts  []string
	Limits   Limits
	// Inputs (protocol 2) are named in Question as ${name}.
	Inputs []Input
	// Detail (protocol 2): the plugin may write a human-only detail on
	// fd 7, shown on the console with clear results on, never to the
	// agent.
	Detail bool
}

type rawLimits struct {
	CPUSeconds int    `toml:"cpu_s"`
	Memory     string `toml:"memory"`
	TimeoutS   int    `toml:"timeout_s"`
}

type rawInput struct {
	Name     string `toml:"name"`
	Kind     string `toml:"kind"`
	MaxBytes int    `toml:"max_bytes"`
}

type rawManifest struct {
	Name     string     `toml:"name"`
	Version  string     `toml:"version"`
	Protocol int        `toml:"protocol"`
	Question string     `toml:"question"`
	One      string     `toml:"one"`
	Zero     string     `toml:"zero"`
	Accepts  []string   `toml:"accepts"`
	Limits   rawLimits  `toml:"limits"`
	Inputs   []rawInput `toml:"inputs"`
	Detail   bool       `toml:"detail"`
}

// ParseManifest decodes and validates a plugin.toml. Unknown keys, notably
// params, are an error: a plugin takes no value the agent chooses. A
// protocol-2 manifest may declare inputs (values the human types) and
// detail; a protocol-1 manifest may not.
func ParseManifest(b []byte) (Manifest, error) {
	var r rawManifest
	md, err := toml.NewDecoder(bytes.NewReader(b)).Decode(&r)
	if err != nil {
		return Manifest{}, fmt.Errorf("manifest: %w", err)
	}
	if u := md.Undecoded(); len(u) > 0 {
		keys := make([]string, len(u))
		for i, k := range u {
			keys[i] = k.String()
		}
		return Manifest{}, fmt.Errorf("manifest: unknown keys %s", strings.Join(keys, ", "))
	}
	mem, err := ParseBytes(r.Limits.Memory)
	if err != nil {
		return Manifest{}, fmt.Errorf("manifest: limits.memory: %w", err)
	}
	if r.Protocol < 2 && (md.IsDefined("inputs") || md.IsDefined("detail")) {
		return Manifest{}, errors.New("manifest: inputs and detail need protocol = 2")
	}
	m := Manifest{
		Name: r.Name, Version: r.Version, Protocol: r.Protocol,
		Question: r.Question, One: r.One, Zero: r.Zero, Accepts: r.Accepts,
		Limits: Limits{
			CPUSeconds: r.Limits.CPUSeconds,
			Memory:     mem,
			Timeout:    time.Duration(r.Limits.TimeoutS) * time.Second,
		},
		Detail: r.Detail,
	}
	for _, in := range r.Inputs {
		m.Inputs = append(m.Inputs, Input(in))
	}
	return m, m.Validate()
}

// Validate checks a manifest against the contract.
func (m Manifest) Validate() error {
	var errs []error
	if !nameRe.MatchString(m.Name) {
		errs = append(errs, fmt.Errorf("name must match %s", nameRe))
	}
	if !versionRe.MatchString(m.Version) {
		errs = append(errs, errors.New("version must be X.Y.Z"))
	}
	if m.Protocol < 1 || m.Protocol > ProtocolVersion {
		errs = append(errs, fmt.Errorf("protocol %d unsupported (want 1..%d)", m.Protocol, ProtocolVersion))
	}
	if m.Protocol < 2 && (len(m.Inputs) > 0 || m.Detail) {
		errs = append(errs, errors.New("inputs and detail need protocol = 2"))
	}
	if m.Protocol >= 2 {
		errs = append(errs, m.validateInputs()...)
	}
	for k, v := range map[string]string{"question": m.Question, "one": m.One, "zero": m.Zero} {
		if strings.TrimSpace(v) == "" {
			errs = append(errs, fmt.Errorf("%s is empty", k))
		}
	}
	if m.One != "" && m.One == m.Zero {
		errs = append(errs, errors.New("one and zero must differ"))
	}
	if len(m.Accepts) == 0 {
		errs = append(errs, errors.New("accepts is empty"))
	}
	for _, a := range m.Accepts {
		if !MediaTypes[a] {
			errs = append(errs, fmt.Errorf("accepts: a media type is not allowed (want %s)", strings.Join(mediaTypeList(), ", ")))
			break
		}
	}
	l := m.Limits
	if l.CPUSeconds <= 0 {
		errs = append(errs, errors.New("limits.cpu_s must be > 0"))
	}
	if l.Timeout <= 0 || l.Timeout > MaxTimeout {
		errs = append(errs, fmt.Errorf("limits.timeout_s must be in 1..%d", int(MaxTimeout/time.Second)))
	}
	if l.Memory <= 0 || l.Memory > MaxMemory {
		errs = append(errs, errors.New("limits.memory must be in 1B..2GiB"))
	}
	return errors.Join(errs...)
}

// validateInputs checks the inputs of a protocol-2 manifest, and that its
// question names each of them as ${name} and nothing else in ${...}.
func (m Manifest) validateInputs() []error {
	var errs []error
	if len(m.Inputs) > MaxInputs {
		errs = append(errs, fmt.Errorf("inputs: at most %d", MaxInputs))
	}
	names := map[string]bool{}
	for _, in := range m.Inputs {
		if !inputNameRe.MatchString(in.Name) {
			errs = append(errs, fmt.Errorf("inputs: a name must match %s", inputNameRe))
		}
		if names[in.Name] {
			errs = append(errs, fmt.Errorf("inputs: %s is declared twice", in.Name))
		}
		names[in.Name] = true
		switch in.Kind {
		case KindText, KindEmail, KindDigits:
		default:
			errs = append(errs, fmt.Errorf("inputs: %s: kind must be one of %s, %s, %s", in.Name, KindText, KindEmail, KindDigits))
		}
		if in.MaxBytes < 1 || in.MaxBytes > MaxInputBytes {
			errs = append(errs, fmt.Errorf("inputs: %s: max_bytes must be in 1..%d", in.Name, MaxInputBytes))
		}
	}
	named, err := Placeholders(m.Question)
	if err != nil {
		return append(errs, fmt.Errorf("question: %w", err))
	}
	for _, n := range named {
		if !names[n] {
			errs = append(errs, fmt.Errorf("question: ${%s} is not an input", n))
		}
	}
	for _, in := range m.Inputs {
		if !slices.Contains(named, in.Name) {
			errs = append(errs, fmt.Errorf("question: input %s is not named as ${%s}", in.Name, in.Name))
		}
	}
	return errs
}

// Input returns the input named name.
func (m Manifest) Input(name string) (Input, bool) {
	for _, in := range m.Inputs {
		if in.Name == name {
			return in, true
		}
	}
	return Input{}, false
}

// Placeholders lists the names a text writes as ${name}, in order of
// first appearance; a "${" that does not open a valid name closed by "}"
// is an error.
func Placeholders(s string) ([]string, error) {
	var out []string
	for {
		i := strings.Index(s, "${")
		if i < 0 {
			return out, nil
		}
		s = s[i+2:]
		j := strings.IndexByte(s, '}')
		if j < 0 || !inputNameRe.MatchString(s[:j]) {
			return nil, fmt.Errorf("a ${...} must hold a name matching %s", inputNameRe)
		}
		if !slices.Contains(out, s[:j]) {
			out = append(out, s[:j])
		}
		s = s[j+1:]
	}
}

// ValidInputName reports whether s is a valid input or placeholder name.
func ValidInputName(s string) bool { return inputNameRe.MatchString(s) }

// errInputValue is the one error of a value that does not fit its input:
// it never quotes the value.
var errInputValue = errors.New("the value does not fit the input")

// CheckValue checks a typed value against in: 1..MaxBytes bytes of UTF-8
// without control characters, of in's kind. The error never quotes v.
func (in Input) CheckValue(v []byte) error {
	if len(v) == 0 || len(v) > in.MaxBytes || !utf8.Valid(v) {
		return errInputValue
	}
	for _, r := range string(v) {
		if unicode.IsControl(r) {
			return errInputValue
		}
	}
	switch in.Kind {
	case KindText:
		return nil
	case KindEmail:
		if emailRe.Match(v) {
			return nil
		}
	case KindDigits:
		if digitsRe.Match(v) {
			return nil
		}
	}
	return errInputValue
}

// ParseBytes reads a size such as "512MiB", "1GiB", "64KiB" or "4096".
func ParseBytes(s string) (int64, error) {
	mult := int64(1)
	num := s
	for _, u := range []struct {
		suffix string
		mult   int64
	}{{"KiB", 1 << 10}, {"MiB", 1 << 20}, {"GiB", 1 << 30}} {
		if strings.HasSuffix(s, u.suffix) {
			mult, num = u.mult, strings.TrimSuffix(s, u.suffix)
			break
		}
	}
	n, err := strconv.ParseInt(num, 10, 64)
	if err != nil || n <= 0 || n > (1<<62)/mult {
		return 0, errors.New(`invalid size (want a positive number of bytes, or with KiB, MiB or GiB: "512MiB")`)
	}
	return n * mult, nil
}

// ValidName reports whether s is a valid plugin name.
func ValidName(s string) bool { return nameRe.MatchString(s) }

// ValidVersion reports whether s is an exact X.Y.Z version.
func ValidVersion(s string) bool { return versionRe.MatchString(s) }

// mediaTypeList is MediaTypes, sorted.
func mediaTypeList() []string {
	out := make([]string, 0, len(MediaTypes))
	for t := range MediaTypes {
		out = append(out, t)
	}
	slices.Sort(out)
	return out
}
