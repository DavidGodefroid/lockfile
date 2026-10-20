// Package workspace holds fetched files in the lockfile account: one
// private directory per handle, a random file name, never the source name.
// Nothing here is shown to the agent but the handle; Info is for the audit.
package workspace

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"syscall"
	"time"
)

var (
	ErrTooLarge      = errors.New("workspace: file too large")
	ErrExpired       = errors.New("handle_expired")
	ErrUnknownHandle = errors.New("workspace: unknown handle")
)

// Defaults for zero Config fields.
const (
	DefaultMaxBytes = 1 << 30
	DefaultTTL      = 30 * time.Minute
)

// Handle names a fetched file for the agent: "f_" and 10 base62 characters
// from crypto/rand, unrelated to anything about the file.
type Handle string

type Config struct {
	Dir      string
	MaxBytes int64 // all handles together
	TTL      time.Duration
	Now      func() time.Time
}

// Info is audit-only data about a fetched file.
type Info struct {
	Size   int64
	SHA256 string
}

type entry struct {
	dir     string
	file    string
	info    Info
	created time.Time
	ttl     time.Duration // fixed at fetch: a later policy does not extend it
	gone    bool          // expired and removed; kept so the handle still says expired
}

type Manager struct {
	c       Config
	mu      sync.Mutex
	handles map[Handle]*entry
	used    int64
}

// Open prepares the workspace root (0700) and removes everything a previous
// console left in it.
func Open(c Config) (*Manager, error) {
	if c.Dir == "" {
		return nil, errors.New("workspace: no directory")
	}
	if c.MaxBytes <= 0 {
		c.MaxBytes = DefaultMaxBytes
	}
	if c.TTL <= 0 {
		c.TTL = DefaultTTL
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return nil, err
	}
	fi, err := os.Lstat(c.Dir)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("workspace: %s is not a directory", c.Dir)
	}
	if err := os.Chmod(c.Dir, 0o700); err != nil {
		return nil, err
	}
	m := &Manager{c: c, handles: map[Handle]*entry{}}
	if err := m.sweep(); err != nil {
		return nil, err
	}
	return m, nil
}

// ownEntry matches the handle directories the workspace creates.
var ownEntry = regexp.MustCompile(`^[0-9a-f]{32}$`)

// sweep removes the handle directories in Dir, and nothing else: a work_dir
// set by mistake to a directory with other content keeps that content.
func (m *Manager) sweep() error {
	des, err := os.ReadDir(m.c.Dir)
	if err != nil {
		return err
	}
	var errs []error
	for _, d := range des {
		if d.IsDir() && ownEntry.MatchString(d.Name()) {
			errs = append(errs, os.RemoveAll(filepath.Join(m.c.Dir, d.Name())))
		}
	}
	return errors.Join(errs...)
}

const base62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

func newHandle() (Handle, error) {
	b := []byte("f_")
	for i := 0; i < 10; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(base62))))
		if err != nil {
			return "", err
		}
		b = append(b, base62[n.Int64()])
	}
	return Handle(b), nil
}

func randomHex() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Create streams r into a new handle, hashing as it copies. It fails with
// ErrTooLarge, and leaves nothing behind, as soon as the file exceeds max or
// the space left in the workspace.
func (m *Manager) Create(r io.Reader, max int64) (h Handle, info Info, err error) {
	m.mu.Lock()
	budget := m.c.MaxBytes - m.used
	m.mu.Unlock()
	limit := budget
	if max > 0 && max < limit {
		limit = max
	}
	if limit < 0 {
		limit = 0
	}
	dirName, err := randomHex()
	if err != nil {
		return "", Info{}, err
	}
	fileName, err := randomHex()
	if err != nil {
		return "", Info{}, err
	}
	dir := filepath.Join(m.c.Dir, dirName)
	if err := os.Mkdir(dir, 0o700); err != nil {
		return "", Info{}, err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(dir)
		}
	}()
	path := filepath.Join(dir, fileName)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o600)
	if err != nil {
		return "", Info{}, err
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hash), io.LimitReader(r, limit+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", Info{}, err
	}
	if n > limit {
		return "", Info{}, ErrTooLarge
	}
	info = Info{Size: n, SHA256: hex.EncodeToString(hash.Sum(nil))}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.used+n > m.c.MaxBytes {
		return "", Info{}, ErrTooLarge
	}
	for {
		if h, err = newHandle(); err != nil {
			return "", Info{}, err
		}
		if _, taken := m.handles[h]; !taken {
			break
		}
	}
	m.handles[h] = &entry{dir: dir, file: path, info: info, created: m.c.Now(), ttl: m.c.TTL}
	m.used += n
	return h, info, nil
}

// Dir is the workspace root.
func (m *Manager) Dir() string { return m.c.Dir }

// SetLimits applies the approved policy's workspace budget and handle TTL
// to the handles fetched from now on (zero keeps the default). A handle
// keeps the TTL it was fetched with.
func (m *Manager) SetLimits(maxBytes int64, ttl time.Duration) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.c.MaxBytes, m.c.TTL = maxBytes, ttl
}

func (m *Manager) live(h Handle) (*entry, error) {
	e, ok := m.handles[h]
	if !ok {
		return nil, ErrUnknownHandle
	}
	if e.gone || !m.c.Now().Before(e.created.Add(e.ttl)) {
		return nil, ErrExpired
	}
	return e, nil
}

// OpenFile opens a handle's file read-only, from its start.
func (m *Manager) OpenFile(h Handle) (*os.File, Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, err := m.live(h)
	if err != nil {
		return nil, Info{}, err
	}
	f, err := os.OpenFile(e.file, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, Info{}, err
	}
	return f, e.info, nil
}

func (m *Manager) remove(e *entry) error {
	if e.gone {
		return nil
	}
	e.gone = true
	m.used -= e.info.Size
	return os.RemoveAll(e.dir)
}

// Release deletes a handle's file now.
func (m *Manager) Release(h Handle) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.handles[h]
	if !ok {
		return ErrUnknownHandle
	}
	err := m.remove(e)
	delete(m.handles, h)
	return err
}

// Expire deletes the files of every handle past its TTL. The handles keep
// answering ErrExpired.
func (m *Manager) Expire() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.c.Now()
	for _, e := range m.handles {
		if !now.Before(e.created.Add(e.ttl)) {
			_ = m.remove(e)
		}
	}
}

// Close deletes every handle's file.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var errs []error
	for h, e := range m.handles {
		errs = append(errs, m.remove(e))
		delete(m.handles, h)
	}
	return errors.Join(append(errs, m.sweep())...)
}
