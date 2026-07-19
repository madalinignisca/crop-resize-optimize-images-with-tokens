package cache

import (
	"errors"
	"testing"
)

func TestKeyStableAndDistinct(t *testing.T) {
	a := Key("products/1.jpg", "w=100;h=100;q=80;crop=cover;fmt=webp")
	b := Key("products/1.jpg", "w=100;h=100;q=80;crop=cover;fmt=webp")
	c := Key("products/1.jpg", "w=200;h=100;q=80;crop=cover;fmt=webp")
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

func TestDiskRoundTrip(t *testing.T) {
	d, err := NewDisk(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key := Key("a.jpg", "w=10;fmt=png")
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
	key := Key("b.jpg", "fmt=jpeg")
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
