// Package source resolves the token's "src" identifier to actual image bytes.
//
// Even though src arrives inside a validly signed token, it is treated as
// untrusted: a leaked signing secret must not turn into arbitrary file reads.
// Every src is confined to a fixed base directory and may additionally be
// constrained by an allowlist pattern.
package source

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ErrNotFound is returned when the resolved source image does not exist.
var ErrNotFound = errors.New("source: not found")

// ErrForbidden is returned when src fails validation (traversal, absolute path,
// allowlist mismatch, or escaping the base directory via symlink).
var ErrForbidden = errors.New("source: forbidden path")

// Source loads image bytes for a token's src identifier.
type Source interface {
	Open(src string) ([]byte, error)
}

// Dir is a Source backed by a base directory on local disk. It is the v1
// storage backend; a MinIO-backed Source can implement the same interface
// later without touching the handler.
type Dir struct {
	base    string
	allow   *regexp.Regexp
	maxSize int64
}

// NewDir creates a Dir rooted at base. allowPattern, if non-empty, is a regular
// expression that the *cleaned* src (relative, forward-slash) must fully match;
// pass "" to allow any path that stays within base. maxSize caps the file size
// read into memory (bytes); pass 0 for no cap.
func NewDir(base, allowPattern string, maxSize int64) (*Dir, error) {
	if strings.TrimSpace(base) == "" {
		return nil, errors.New("source: base directory is empty")
	}
	abs, err := filepath.Abs(base)
	if err != nil {
		return nil, fmt.Errorf("source: resolve base: %w", err)
	}
	d := &Dir{base: abs, maxSize: maxSize}
	if allowPattern != "" {
		re, err := regexp.Compile(allowPattern)
		if err != nil {
			return nil, fmt.Errorf("source: compile allow pattern: %w", err)
		}
		d.allow = re
	}
	return d, nil
}

// Resolve validates src and returns the absolute on-disk path it maps to,
// without opening it. It is exported so callers (and tests) can reason about
// path safety independently of I/O.
func (d *Dir) Resolve(src string) (string, error) {
	if src == "" {
		return "", ErrForbidden
	}
	// Reject anything that even looks like an absolute path or a Windows volume
	// before cleaning.
	if strings.HasPrefix(src, "/") || strings.HasPrefix(src, "\\") || filepath.IsAbs(src) {
		return "", ErrForbidden
	}
	// Normalise separators and clean. Clean collapses "." and resolves ".."
	// lexically; any leftover leading ".." means the path tried to escape.
	clean := filepath.Clean(filepath.FromSlash(src))
	if clean == "." || clean == ".." ||
		strings.HasPrefix(clean, ".."+string(filepath.Separator)) ||
		strings.Contains(clean, string(filepath.Separator)+".."+string(filepath.Separator)) {
		return "", ErrForbidden
	}
	if d.allow != nil && !d.allow.MatchString(filepath.ToSlash(clean)) {
		return "", ErrForbidden
	}

	full := filepath.Join(d.base, clean)
	// Final containment guard: after joining, the path must still be inside base.
	if !withinBase(d.base, full) {
		return "", ErrForbidden
	}
	return full, nil
}

// Open validates src and returns its bytes.
func (d *Dir) Open(src string) ([]byte, error) {
	full, err := d.Resolve(src)
	if err != nil {
		return nil, err
	}

	// Defend against symlinks that point outside base. EvalSymlinks needs the
	// file to exist, so a missing file is reported as not-found.
	real, err := filepath.EvalSymlinks(full)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, ErrForbidden
	}
	if !withinBase(d.base, real) {
		return nil, ErrForbidden
	}

	info, err := os.Stat(real)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("source: stat: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, ErrForbidden
	}
	if d.maxSize > 0 && info.Size() > d.maxSize {
		return nil, fmt.Errorf("source: %q exceeds max size %d bytes", src, d.maxSize)
	}

	data, err := os.ReadFile(real)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("source: read: %w", err)
	}
	return data, nil
}

// withinBase reports whether path is base itself or lives underneath it.
func withinBase(base, path string) bool {
	if path == base {
		return true
	}
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
