package config

import (
	"os"
	"path/filepath"
	"testing"
)

// clearEnv removes every IMG_ variable so tests are hermetic.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, e := range os.Environ() {
		if len(e) >= 4 && e[:4] == "IMG_" {
			key := e[:indexByte(e, '=')]
			t.Setenv(key, "")
			os.Unsetenv(key)
		}
	}
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return len(s)
}

func TestLoadRequiresSecret(t *testing.T) {
	clearEnv(t)
	t.Setenv("IMG_SOURCE_DIR", "/srv/img")
	t.Setenv("IMG_CACHE_DIR", "/var/cache/img")
	if _, err := Load(); err == nil {
		t.Fatal("expected error when secret missing")
	}
}

func TestLoadRequiresSourceAndCacheDir(t *testing.T) {
	clearEnv(t)
	t.Setenv("IMG_SECRET", "s3cr3t")
	if _, err := Load(); err == nil {
		t.Fatal("expected error when source dir missing")
	}
	t.Setenv("IMG_SOURCE_DIR", "/srv/img")
	if _, err := Load(); err == nil {
		t.Fatal("expected error when cache dir missing")
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)
	t.Setenv("IMG_SECRET", "s3cr3t")
	t.Setenv("IMG_SOURCE_DIR", "/srv/img")
	t.Setenv("IMG_CACHE_DIR", "/var/cache/img")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.MaxWidth != 4000 || c.MaxHeight != 4000 {
		t.Errorf("unexpected max dims: %dx%d", c.MaxWidth, c.MaxHeight)
	}
	if c.SignatureBytes != 32 {
		t.Errorf("default signature bytes = %d, want 32", c.SignatureBytes)
	}
	if c.DefaultQuality != 82 {
		t.Errorf("default quality = %d", c.DefaultQuality)
	}
	if c.PublicAddr != ":8080" {
		t.Errorf("default addr = %q", c.PublicAddr)
	}
}

func TestLoadSecretFromFile(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	if err := os.WriteFile(path, []byte("  file-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("IMG_SECRET_FILE", path)
	t.Setenv("IMG_SOURCE_DIR", "/srv/img")
	t.Setenv("IMG_CACHE_DIR", "/var/cache/img")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.PrimarySecret != "file-secret" {
		t.Fatalf("secret from file = %q, want trimmed 'file-secret'", c.PrimarySecret)
	}
}

func TestLoadRotationSecret(t *testing.T) {
	clearEnv(t)
	t.Setenv("IMG_SECRET", "new")
	t.Setenv("IMG_SECRET_PREVIOUS", "old")
	t.Setenv("IMG_SOURCE_DIR", "/srv/img")
	t.Setenv("IMG_CACHE_DIR", "/var/cache/img")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.PreviousSecrets) != 1 || c.PreviousSecrets[0] != "old" {
		t.Fatalf("previous secrets = %v", c.PreviousSecrets)
	}
}

func TestLoadRejectsBadSignatureBytes(t *testing.T) {
	clearEnv(t)
	t.Setenv("IMG_SECRET", "s")
	t.Setenv("IMG_SOURCE_DIR", "/srv/img")
	t.Setenv("IMG_CACHE_DIR", "/var/cache/img")
	t.Setenv("IMG_SIGNATURE_BYTES", "8")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for signature bytes below 16")
	}
}

func TestLoadOverrides(t *testing.T) {
	clearEnv(t)
	t.Setenv("IMG_SECRET", "s")
	t.Setenv("IMG_SOURCE_DIR", "/srv/img")
	t.Setenv("IMG_CACHE_DIR", "/var/cache/img")
	t.Setenv("IMG_MAX_WIDTH", "2000")
	t.Setenv("IMG_SIGNATURE_BYTES", "16")
	t.Setenv("IMG_MAX_SOURCE_PIXELS", "5000000")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.MaxWidth != 2000 {
		t.Errorf("max width override = %d", c.MaxWidth)
	}
	if c.SignatureBytes != 16 {
		t.Errorf("signature bytes override = %d", c.SignatureBytes)
	}
	if c.MaxSourcePixel != 5_000_000 {
		t.Errorf("max source pixels override = %d", c.MaxSourcePixel)
	}
}
