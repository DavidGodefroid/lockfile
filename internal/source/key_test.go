package source

import (
	"errors"
	"testing"
)

func TestKeyEscapes(t *testing.T) {
	refused := []struct{ prefix, key string }{
		{"invoices/", "../x"},
		{"invoices/", "/etc/passwd"},
		{"invoices/", "invoices/../../x"},
		{"invoices/", "invoices/../other/x"},
		{"invoices/", "%2e%2e/x"},
		{"invoices/", "invoices/%2e%2e/%2e%2e/x"},
		{"invoices/", "invoices%2f..%2f..%2fx"},
		{"invoices/", ""},
		{"invoices/", "invoices/"},
		{"invoices/", "other/a.pdf"},
		{"invoices/", "invoicesX/a.pdf"},
		{"invoices", "invoicesX/a.pdf"},
		{"invoices/", "invoices/a\x00.pdf"},
		{"invoices/", "invoices\\..\\x"},
		{"invoices/", "%zz"},
		{"", "../x"},
		{"", "a/../../x"},
		{"", "/x"},
		{"", "."},
	}
	for _, c := range refused {
		if got, err := CheckKey(c.prefix, c.key); !errors.Is(err, ErrKeyOutsidePrefix) {
			t.Errorf("CheckKey(%q, %q) = %q, %v; want ErrKeyOutsidePrefix", c.prefix, c.key, got, err)
		}
	}
	accepted := []struct{ prefix, key, want string }{
		{"invoices/", "invoices/./a.pdf", "invoices/a.pdf"},
		{"invoices/", "invoices/2026/10/abc123.pdf", "invoices/2026/10/abc123.pdf"},
		{"invoices/", "invoices//2026/a.pdf", "invoices/2026/a.pdf"},
		{"invoices", "invoices/a.pdf", "invoices/a.pdf"},
		{"", "a.pdf", "a.pdf"},
		{"", "dir/a%20b.pdf", "dir/a b.pdf"},
	}
	for _, c := range accepted {
		got, err := CheckKey(c.prefix, c.key)
		if err != nil || got != c.want {
			t.Errorf("CheckKey(%q, %q) = %q, %v; want %q", c.prefix, c.key, got, err, c.want)
		}
	}
}
