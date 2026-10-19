package source

import (
	"errors"
	"net/url"
	"path"
	"strings"
)

// ErrKeyOutsidePrefix refuses a key that is not plainly under the profile
// prefix. Its text is the agent code.
var ErrKeyOutsidePrefix = errors.New("key_outside_prefix")

// CheckKey percent-decodes key once and returns it cleaned, or
// ErrKeyOutsidePrefix when it is empty, absolute, has a ".." segment, a
// backslash or a control character, or is not under prefix (a directory:
// "invoices" means "invoices/").
func CheckKey(prefix, key string) (string, error) {
	k, err := url.PathUnescape(key)
	if err != nil || k == "" || strings.HasPrefix(k, "/") || strings.ContainsRune(k, '\\') {
		return "", ErrKeyOutsidePrefix
	}
	for _, r := range k {
		if r < 0x20 || r == 0x7f {
			return "", ErrKeyOutsidePrefix
		}
	}
	for _, seg := range strings.Split(k, "/") {
		if seg == ".." {
			return "", ErrKeyOutsidePrefix
		}
	}
	k = path.Clean(k)
	if k == "." {
		return "", ErrKeyOutsidePrefix
	}
	if prefix != "" {
		p := strings.TrimSuffix(prefix, "/") + "/"
		if !strings.HasPrefix(k, p) || len(k) == len(p) {
			return "", ErrKeyOutsidePrefix
		}
	}
	return k, nil
}
