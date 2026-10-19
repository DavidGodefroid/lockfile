package pluginspec

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// ProtocolVersion is the only plugin protocol this package knows.
const ProtocolVersion = 1

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
)

// Limits are the resources a plugin run may use.
type Limits struct {
	CPUSeconds int
	Memory     int64 // address space, bytes
	Timeout    time.Duration
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
}

type rawLimits struct {
	CPUSeconds int    `toml:"cpu_s"`
	Memory     string `toml:"memory"`
	TimeoutS   int    `toml:"timeout_s"`
}

type rawManifest struct {
	Name     string    `toml:"name"`
	Version  string    `toml:"version"`
	Protocol int       `toml:"protocol"`
	Question string    `toml:"question"`
	One      string    `toml:"one"`
	Zero     string    `toml:"zero"`
	Accepts  []string  `toml:"accepts"`
	Limits   rawLimits `toml:"limits"`
}

// ParseManifest decodes and validates a plugin.toml. Unknown keys, notably
// params, are an error: plugins take no parameters.
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
	m := Manifest{
		Name: r.Name, Version: r.Version, Protocol: r.Protocol,
		Question: r.Question, One: r.One, Zero: r.Zero, Accepts: r.Accepts,
		Limits: Limits{
			CPUSeconds: r.Limits.CPUSeconds,
			Memory:     mem,
			Timeout:    time.Duration(r.Limits.TimeoutS) * time.Second,
		},
	}
	return m, m.Validate()
}

// Validate checks a manifest against the contract.
func (m Manifest) Validate() error {
	var errs []error
	if !nameRe.MatchString(m.Name) {
		errs = append(errs, fmt.Errorf("name %q must match %s", m.Name, nameRe))
	}
	if !versionRe.MatchString(m.Version) {
		errs = append(errs, fmt.Errorf("version %q must be X.Y.Z", m.Version))
	}
	if m.Protocol != ProtocolVersion {
		errs = append(errs, fmt.Errorf("protocol %d unsupported (want %d)", m.Protocol, ProtocolVersion))
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
			errs = append(errs, fmt.Errorf("accepts: media type %q not allowed", a))
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
		return 0, fmt.Errorf("invalid size %q", s)
	}
	return n * mult, nil
}
