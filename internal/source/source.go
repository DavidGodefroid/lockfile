// Package source fetches one object by its exact key from S3 (or an
// S3-compatible store), SFTP or a local directory. There is no listing: the
// agent must name the key. Errors from the libraries are kept for the audit;
// what leaves Fetch towards the agent is only "fetch_failed".
package source

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/DavidGodefroid/lockfile/internal/workspace"
)

// ErrSource wraps every error a source library or the OS returned.
var ErrSource = errors.New("source error")

// Source reads single objects by key. Implementations check the key
// themselves (CheckKey) and return ErrKeyOutsidePrefix for one that escapes.
type Source interface {
	Stat(ctx context.Context, key string) (ObjectInfo, error)
	Open(ctx context.Context, key string) (io.ReadCloser, error)
}

// ObjectInfo is audit-only.
type ObjectInfo struct{ Size int64 }

// FetchError is any failure of Fetch. Its text is the agent code; Cause is
// for the audit only.
type FetchError struct{ Cause error }

func (e *FetchError) Error() string { return "fetch_failed" }
func (e *FetchError) Unwrap() error { return e.Cause }

// Fetch copies key from s into a new workspace handle, refusing objects
// above max both from Stat and while streaming.
func Fetch(ctx context.Context, s Source, key string, max int64, ws *workspace.Manager) (workspace.Handle, workspace.Info, error) {
	fi, err := s.Stat(ctx, key)
	if err != nil {
		return "", workspace.Info{}, &FetchError{Cause: err}
	}
	if fi.Size > max {
		return "", workspace.Info{}, &FetchError{Cause: fmt.Errorf("%w: %d > %d bytes", workspace.ErrTooLarge, fi.Size, max)}
	}
	rc, err := s.Open(ctx, key)
	if err != nil {
		return "", workspace.Info{}, &FetchError{Cause: err}
	}
	defer rc.Close()
	h, info, err := ws.Create(rc, max)
	if err != nil {
		return "", workspace.Info{}, &FetchError{Cause: err}
	}
	return h, info, nil
}

func wrap(op string, err error) error {
	return fmt.Errorf("%w: %s: %w", ErrSource, op, err)
}
