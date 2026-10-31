package profile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/DavidGodefroid/lockkit/config"
)

// The config file is the human's: these writers append or cut whole
// [[marketplaces]] blocks as text, keep every other byte, and check the
// result with Parse before it replaces the file.

// readConfig returns the file's bytes and mode; a missing file is empty.
func readConfig(path string) ([]byte, fs.FileMode, error) {
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, 0o600, nil
	case err != nil:
		return nil, 0, fmt.Errorf("config: %w", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil, 0, fmt.Errorf("config: %w", err)
	}
	return b, fi.Mode().Perm(), nil
}

// writeConfig checks data with Parse, then replaces the file atomically.
func writeConfig(path string, data []byte, mode fs.FileMode) error {
	if _, err := Parse(data, path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.toml")
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("config: %w", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	return os.Rename(tmp.Name(), path)
}

// tomlString is s as a TOML basic string.
func tomlString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// AppendMarketplace adds an unofficial marketplace at the end of the
// config file at path (created 0600 when missing).
func AppendMarketplace(path string, r RawMarketplace) (Trust, error) {
	t, err := CheckMarketplace(r)
	if err != nil {
		return Trust{}, err
	}
	data, mode, err := readConfig(path)
	if err != nil {
		return Trust{}, err
	}
	if len(data) > 0 {
		f, err := Parse(data, path)
		if err != nil {
			return Trust{}, err
		}
		for _, m := range f.Marketplaces {
			if m.Name == t.Name {
				return Trust{}, fmt.Errorf("marketplace %s is already configured", t.Name)
			}
		}
	}
	var b strings.Builder
	b.Write(data)
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		b.WriteString("\n")
	}
	if len(data) > 0 {
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "[[marketplaces]]\nname = %s\nindex_url = %s\nidentity_regexp = %s\noidc_issuer = %s\n",
		tomlString(t.Name), tomlString(t.IndexURL), tomlString(t.IdentityRegexp), tomlString(t.OIDCIssuer))
	return t, writeConfig(path, []byte(b.String()), mode)
}

// AppendProfile adds [profiles.<name>] with the given keys (TOML names;
// string, int or bool values, written in the order of keys) at the end of
// the config file at path, after checking the whole file with Parse.
func AppendProfile(path, name string, keys []string, values map[string]any) error {
	production, _ := values["production"].(bool)
	if err := config.CheckProfileName(name, production); err != nil {
		return err
	}
	data, mode, err := readConfig(path)
	if err != nil {
		return err
	}
	if len(data) > 0 {
		f, err := Parse(data, path)
		if err != nil {
			return err
		}
		if _, ok := f.Profiles[name]; ok {
			return fmt.Errorf("profile %s already exists: edit the config file to change it", name)
		}
	}
	var b strings.Builder
	b.Write(data)
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		b.WriteString("\n")
	}
	if len(data) > 0 {
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "[profiles.%s]\n", name)
	for _, k := range keys {
		switch v := values[k].(type) {
		case string:
			fmt.Fprintf(&b, "%s = %s\n", k, tomlString(v))
		case int:
			fmt.Fprintf(&b, "%s = %d\n", k, v)
		case bool:
			fmt.Fprintf(&b, "%s = %t\n", k, v)
		default:
			return fmt.Errorf("profile %s: unsupported value for %s", name, k)
		}
	}
	return writeConfig(path, []byte(b.String()), mode)
}

var (
	tableHeader = regexp.MustCompile(`^\s*\[`)
	marketBlock = regexp.MustCompile(`^\s*\[\[\s*marketplaces\s*\]\]\s*(#.*)?$`)
	nameLine    = regexp.MustCompile(`^\s*name\s*=\s*["']([^"']*)["']\s*(#.*)?$`)
)

func commentOrBlank(line string) bool {
	l := strings.TrimSpace(line)
	return l == "" || strings.HasPrefix(l, "#")
}

// RemoveMarketplace cuts the [[marketplaces]] block named name from the
// config file at path.
func RemoveMarketplace(path, name string) error {
	data, mode, err := readConfig(path)
	if err != nil {
		return err
	}
	lines := strings.SplitAfter(string(data), "\n")
	var out []string
	found := false
	for i := 0; i < len(lines); {
		if !marketBlock.MatchString(strings.TrimRight(lines[i], "\n")) {
			out = append(out, lines[i])
			i++
			continue
		}
		end := i + 1
		for end < len(lines) && !tableHeader.MatchString(lines[end]) {
			end++
		}
		// Comments and blank lines at the end of the block belong to what
		// follows (or to the file): the block stops at its last key line.
		for end > i+1 && commentOrBlank(lines[end-1]) {
			end--
		}
		match := false
		for _, l := range lines[i+1 : end] {
			if m := nameLine.FindStringSubmatch(strings.TrimRight(l, "\n")); m != nil && m[1] == name {
				match = true
			}
		}
		if !match {
			out = append(out, lines[i:end]...)
		}
		found = found || match
		i = end
	}
	if !found {
		if !ValidMarketplaceName(name) {
			return errors.New("marketplace: invalid name (lower-case letters, digits and '-', at most 40)")
		}
		return fmt.Errorf("marketplace %s is not configured", name)
	}
	return writeConfig(path, []byte(strings.Join(out, "")), mode)
}
