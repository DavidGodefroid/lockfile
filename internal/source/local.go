package source

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
)

type local struct{ root *os.Root }

// NewLocal serves files under root. os.Root refuses any path, symlinks
// included, that resolves outside it.
func NewLocal(root string) (Source, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, wrap("open root", err)
	}
	return local{root: r}, nil
}

func (l local) Stat(_ context.Context, key string) (ObjectInfo, error) {
	k, err := CheckKey("", key)
	if err != nil {
		return ObjectInfo{}, err
	}
	fi, err := l.root.Stat(filepath.FromSlash(k))
	if err != nil {
		return ObjectInfo{}, wrap("stat", err)
	}
	if !fi.Mode().IsRegular() {
		return ObjectInfo{}, wrap("stat", errors.New("not a regular file"))
	}
	return ObjectInfo{Size: fi.Size()}, nil
}

func (l local) Open(_ context.Context, key string) (io.ReadCloser, error) {
	k, err := CheckKey("", key)
	if err != nil {
		return nil, err
	}
	f, err := l.root.Open(filepath.FromSlash(k))
	if err != nil {
		return nil, wrap("open", err)
	}
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		f.Close()
		return nil, wrap("open", errors.New("not a regular file"))
	}
	return f, nil
}
