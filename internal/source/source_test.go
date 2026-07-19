package source

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAllowlistIsFullMatchAnchored(t *testing.T) {
	// An UNanchored pattern must still be treated as a full-string match, not a
	// substring match, so it cannot leak access to longer paths.
	d, err := NewDir(t.TempDir(), `products/1\.png`, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Resolve("products/1.png"); err != nil {
		t.Errorf("exact match should be allowed: %v", err)
	}
	for _, s := range []string{"products/1.png.bak", "a/products/1.png"} {
		if _, err := d.Resolve(s); !errors.Is(err, ErrForbidden) {
			t.Errorf("Resolve(%q) = %v, want ErrForbidden (must be full match)", s, err)
		}
	}
}

func TestVersionChangesWithContent(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "img.png")
	if err := os.WriteFile(path, []byte("aaaa"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, _ := NewDir(base, "", 0)
	v1, err := d.Version("img.png")
	if err != nil {
		t.Fatal(err)
	}
	// Different size => different version.
	if err := os.WriteFile(path, []byte("bbbbbbbb"), 0o644); err != nil {
		t.Fatal(err)
	}
	v2, err := d.Version("img.png")
	if err != nil {
		t.Fatal(err)
	}
	if v1 == v2 {
		t.Fatalf("version should change when content changes: %q == %q", v1, v2)
	}
}

func TestVersionErrors(t *testing.T) {
	d, _ := NewDir(t.TempDir(), "", 0)
	if _, err := d.Version("missing.png"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing: want ErrNotFound, got %v", err)
	}
	if _, err := d.Version("../escape"); !errors.Is(err, ErrForbidden) {
		t.Errorf("traversal: want ErrForbidden, got %v", err)
	}
}

func TestResolveRejectsTraversal(t *testing.T) {
	d, err := NewDir(t.TempDir(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	bad := []string{
		"../etc/passwd",
		"../../etc/passwd",
		"a/../../etc/passwd",
		"/etc/passwd",
		"foo/../../bar",
		"..",
		".",
		"",
		"a/../..",
	}
	for _, s := range bad {
		if _, err := d.Resolve(s); !errors.Is(err, ErrForbidden) {
			t.Errorf("Resolve(%q) = %v, want ErrForbidden", s, err)
		}
	}
}

func TestResolveAllowsSafePaths(t *testing.T) {
	base := t.TempDir()
	d, _ := NewDir(base, "", 0)
	good := []string{"a.jpg", "products/123.jpg", "deep/nested/path/img.png", "./a.jpg"}
	for _, s := range good {
		got, err := d.Resolve(s)
		if err != nil {
			t.Errorf("Resolve(%q) unexpected error: %v", s, err)
			continue
		}
		if !withinBase(base, got) {
			t.Errorf("Resolve(%q) = %q escaped base %q", s, got, base)
		}
	}
}

func TestAllowlistPattern(t *testing.T) {
	base := t.TempDir()
	d, err := NewDir(base, `^products/[0-9]+\.(jpg|png)$`, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Resolve("products/42.jpg"); err != nil {
		t.Errorf("allowed path rejected: %v", err)
	}
	for _, s := range []string{"secrets/key.pem", "products/abc.jpg", "products/42.gif"} {
		if _, err := d.Resolve(s); !errors.Is(err, ErrForbidden) {
			t.Errorf("Resolve(%q) = %v, want ErrForbidden", s, err)
		}
	}
}

func TestOpenReadsFile(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "products"), 0o755); err != nil {
		t.Fatal(err)
	}
	want := []byte("fake-image-bytes")
	if err := os.WriteFile(filepath.Join(base, "products", "1.jpg"), want, 0o644); err != nil {
		t.Fatal(err)
	}
	d, _ := NewDir(base, "", 0)
	got, err := d.Open("products/1.jpg")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestOpenNotFound(t *testing.T) {
	d, _ := NewDir(t.TempDir(), "", 0)
	if _, err := d.Open("missing.jpg"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestOpenMaxSize(t *testing.T) {
	base := t.TempDir()
	os.WriteFile(filepath.Join(base, "big.jpg"), make([]byte, 100), 0o644)
	d, _ := NewDir(base, "", 50)
	if _, err := d.Open("big.jpg"); err == nil {
		t.Fatal("expected max size error")
	}
}

func TestOpenRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	base := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	os.WriteFile(secret, []byte("top-secret"), 0o644)

	// A symlink inside base pointing outside base.
	link := filepath.Join(base, "escape.jpg")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	d, _ := NewDir(base, "", 0)
	if _, err := d.Open("escape.jpg"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("symlink escape: want ErrForbidden, got %v", err)
	}
}

func TestOpenRejectsDirectory(t *testing.T) {
	base := t.TempDir()
	os.MkdirAll(filepath.Join(base, "sub"), 0o755)
	d, _ := NewDir(base, "", 0)
	if _, err := d.Open("sub"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("directory: want ErrForbidden, got %v", err)
	}
}
