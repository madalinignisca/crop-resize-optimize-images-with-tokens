package cache

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestKeyStableAndDistinct(t *testing.T) {
	a := Key("products/1.jpg", "v1", "w=100;h=100;q=80;crop=cover;fmt=webp")
	b := Key("products/1.jpg", "v1", "w=100;h=100;q=80;crop=cover;fmt=webp")
	c := Key("products/1.jpg", "v1", "w=200;h=100;q=80;crop=cover;fmt=webp")
	if a != b {
		t.Fatal("same inputs should give same key")
	}
	if a == c {
		t.Fatal("different transform should give different key")
	}
	if len(a) != 64 {
		t.Fatalf("expected 64-hex-char key, got %d", len(a))
	}
}

func TestKeyVersionInvalidates(t *testing.T) {
	// Same src + transform but a changed version token must yield a new key, so
	// replacing the source file invalidates its cached renders.
	before := Key("products/1.jpg", "1700-500", "w=100;fmt=webp")
	after := Key("products/1.jpg", "1800-512", "w=100;fmt=webp")
	if before == after {
		t.Fatal("changed source version must change the cache key")
	}
}

func TestDiskRoundTrip(t *testing.T) {
	d, err := NewDisk(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key := Key("a.jpg", "v", "w=10;fmt=png")
	if _, err := d.Get(key); !errors.Is(err, ErrMiss) {
		t.Fatalf("expected miss, got %v", err)
	}
	want := Entry{Bytes: []byte("\x89PNG-fake"), ContentType: "image/png"}
	if err := d.Put(key, want); err != nil {
		t.Fatal(err)
	}
	got, err := d.Get(key)
	if err != nil {
		t.Fatalf("expected hit, got %v", err)
	}
	if string(got.Bytes) != string(want.Bytes) {
		t.Fatalf("bytes mismatch: %q vs %q", got.Bytes, want.Bytes)
	}
	if got.ContentType != "image/png" {
		t.Fatalf("content type = %q", got.ContentType)
	}
	if got.ETag != key {
		t.Fatalf("etag = %q, want %q", got.ETag, key)
	}
}

func TestDiskPersistsAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	d1, _ := NewDisk(dir)
	key := Key("b.jpg", "v", "fmt=jpeg")
	d1.Put(key, Entry{Bytes: []byte("jpegbytes"), ContentType: "image/jpeg"})

	d2, _ := NewDisk(dir) // fresh instance, same dir
	got, err := d2.Get(key)
	if err != nil {
		t.Fatalf("second instance should see cached file: %v", err)
	}
	if string(got.Bytes) != "jpegbytes" {
		t.Fatalf("unexpected bytes %q", got.Bytes)
	}
}

func TestGetIgnoresTempAndUnknownFiles(t *testing.T) {
	dir := t.TempDir()
	d, _ := NewDisk(dir)
	key := Key("c.jpg", "v", "fmt=png")
	shard := filepath.Join(dir, key[:2])
	if err := os.MkdirAll(shard, 0o755); err != nil {
		t.Fatal(err)
	}
	// A leftover temp file and an unknown-extension file must never be served.
	os.WriteFile(filepath.Join(shard, key+"-123456.tmp"), []byte("partial"), 0o644)
	os.WriteFile(filepath.Join(shard, key+".bin"), []byte("junk"), 0o644)

	if _, err := d.Get(key); !errors.Is(err, ErrMiss) {
		t.Fatalf("Get must ignore temp/unknown files, got %v", err)
	}

	// A real png entry under the same key is served.
	if err := d.Put(key, Entry{Bytes: []byte("realpng"), ContentType: "image/png"}); err != nil {
		t.Fatal(err)
	}
	got, err := d.Get(key)
	if err != nil {
		t.Fatalf("expected hit after Put: %v", err)
	}
	if string(got.Bytes) != "realpng" || got.ContentType != "image/png" {
		t.Fatalf("served wrong entry: %q %q", got.Bytes, got.ContentType)
	}
}
