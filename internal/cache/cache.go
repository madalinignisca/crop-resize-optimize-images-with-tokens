// Package cache stores generated image bytes on local disk, keyed by a hash of
// the effective transform. Repeat requests for the same transform are served
// from disk instead of re-rendering (a CPU-heavy operation).
//
// v1 has no eviction: disk is cheap and volumes are low. A time-based cleanup
// can be layered on later (see the Entry mtime) without changing this API.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// ErrMiss is returned by Get when the key is not cached.
var ErrMiss = errors.New("cache: miss")

// Entry is a cached object's bytes plus the metadata needed to serve it.
type Entry struct {
	Bytes       []byte
	ContentType string
	ETag        string
}

// Cache is the storage contract. A MinIO-backed implementation can satisfy the
// same interface later for multi-instance deployments.
type Cache interface {
	Get(key string) (Entry, error)
	Put(key string, e Entry) error
}

// Key derives a stable, filesystem-safe cache key from the pieces that uniquely
// determine the output bytes: the resolved source identifier and the canonical
// transform description. Two different tokens (e.g. differing only by expiry)
// that render the same image share a cache entry.
func Key(src, transform string) string {
	sum := sha256.Sum256([]byte(src + "\x00" + transform))
	return hex.EncodeToString(sum[:])
}

// Disk is a filesystem-backed Cache. Objects are sharded into subdirectories by
// the first two hex characters of the key to keep directory sizes reasonable.
// Each object is a single file whose extension encodes its format; the content
// type is recovered from that extension on read.
type Disk struct {
	dir string
}

// NewDisk creates a Disk cache rooted at dir, creating it if needed.
func NewDisk(dir string) (*Disk, error) {
	if dir == "" {
		return nil, errors.New("cache: dir is empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Disk{dir: dir}, nil
}

func (d *Disk) pathFor(key, ext string) (dir, file string) {
	shard := "00"
	if len(key) >= 2 {
		shard = key[:2]
	}
	dir = filepath.Join(d.dir, shard)
	return dir, filepath.Join(dir, key+ext)
}

// Get returns the cached entry for key, or ErrMiss.
func (d *Disk) Get(key string) (Entry, error) {
	shard := "00"
	if len(key) >= 2 {
		shard = key[:2]
	}
	matches, _ := filepath.Glob(filepath.Join(d.dir, shard, key+".*"))
	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return Entry{}, err
		}
		return Entry{
			Bytes:       data,
			ContentType: contentTypeForExt(filepath.Ext(m)),
			ETag:        key,
		}, nil
	}
	return Entry{}, ErrMiss
}

// Put writes e under key. The write is atomic (temp file + rename) so a
// concurrent Get never observes a partially written object.
func (d *Disk) Put(key string, e Entry) error {
	ext := extForContentType(e.ContentType)
	dir, final := d.pathFor(key, ext)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, key+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(e.Bytes); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, final); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

func contentTypeForExt(ext string) string {
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".avif":
		return "image/avif"
	default:
		return "application/octet-stream"
	}
}

func extForContentType(ct string) string {
	switch ct {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/avif":
		return ".avif"
	default:
		return ".bin"
	}
}
