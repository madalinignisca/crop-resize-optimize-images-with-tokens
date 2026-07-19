// Package config loads runtime configuration from environment variables (and
// optional secret files). Nothing sensitive is ever hardcoded; the HMAC secret
// comes from IMG_SECRET or, preferably, IMG_SECRET_FILE (a mounted secret).
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config is the fully-resolved service configuration.
type Config struct {
	// Secrets. Primary signs new tokens; all are accepted when verifying, which
	// is what enables zero-downtime rotation.
	PrimarySecret   string
	PreviousSecrets []string
	SignatureBytes  int // bytes of HMAC kept in tokens (16..32); 32 = full

	// Storage.
	SourceDir     string
	SourceAllow   string // optional regexp constraining src
	CacheDir      string
	MaxSourceByte int64

	// Server-side limits (defense in depth against oversized renders).
	MaxWidth       int
	MaxHeight      int
	MaxOutputPixel int64
	MaxSourcePixel int64
	DefaultQuality int

	// HTTP.
	PublicAddr    string
	InternalAddr  string // "" disables the /internal/sign HTTP endpoint
	InternalToken string // shared header secret guarding /internal/sign; "" = no header check
	PublicBaseURL string // used to build absolute URLs in sign responses; "" = relative
	CacheMaxAge   int    // seconds for Cache-Control on served images
}

// Load reads configuration from the environment, applying defaults and
// validating required fields.
func Load() (Config, error) {
	c := Config{
		SignatureBytes: 32,
		MaxWidth:       4000,
		MaxHeight:      4000,
		MaxOutputPixel: 24_000_000,
		MaxSourcePixel: 100_000_000,
		DefaultQuality: 82,
		MaxSourceByte:  50 << 20, // 50 MiB
		PublicAddr:     ":8080",
		CacheMaxAge:    31_536_000, // 1 year
	}

	secret, err := loadSecret("IMG_SECRET", "IMG_SECRET_FILE")
	if err != nil {
		return Config{}, err
	}
	if secret == "" {
		return Config{}, fmt.Errorf("config: IMG_SECRET or IMG_SECRET_FILE is required")
	}
	c.PrimarySecret = secret

	prev, err := loadSecret("IMG_SECRET_PREVIOUS", "IMG_SECRET_PREVIOUS_FILE")
	if err != nil {
		return Config{}, err
	}
	if prev != "" {
		c.PreviousSecrets = []string{prev}
	}

	c.SourceDir = getenv("IMG_SOURCE_DIR", "")
	if c.SourceDir == "" {
		return Config{}, fmt.Errorf("config: IMG_SOURCE_DIR is required")
	}
	c.CacheDir = getenv("IMG_CACHE_DIR", "")
	if c.CacheDir == "" {
		return Config{}, fmt.Errorf("config: IMG_CACHE_DIR is required")
	}
	c.SourceAllow = getenv("IMG_SOURCE_ALLOW", "")

	if err := setInt("IMG_SIGNATURE_BYTES", &c.SignatureBytes); err != nil {
		return Config{}, err
	}
	if c.SignatureBytes < 16 || c.SignatureBytes > 32 {
		return Config{}, fmt.Errorf("config: IMG_SIGNATURE_BYTES must be 16..32, got %d", c.SignatureBytes)
	}
	if err := setInt("IMG_MAX_WIDTH", &c.MaxWidth); err != nil {
		return Config{}, err
	}
	if err := setInt("IMG_MAX_HEIGHT", &c.MaxHeight); err != nil {
		return Config{}, err
	}
	if err := setInt("IMG_DEFAULT_QUALITY", &c.DefaultQuality); err != nil {
		return Config{}, err
	}
	if err := setInt("IMG_CACHE_MAX_AGE", &c.CacheMaxAge); err != nil {
		return Config{}, err
	}
	if err := setInt64("IMG_MAX_OUTPUT_PIXELS", &c.MaxOutputPixel); err != nil {
		return Config{}, err
	}
	if err := setInt64("IMG_MAX_SOURCE_PIXELS", &c.MaxSourcePixel); err != nil {
		return Config{}, err
	}
	if err := setInt64("IMG_MAX_SOURCE_BYTES", &c.MaxSourceByte); err != nil {
		return Config{}, err
	}

	c.PublicAddr = getenv("IMG_PUBLIC_ADDR", c.PublicAddr)
	c.InternalAddr = getenv("IMG_INTERNAL_ADDR", "")
	c.PublicBaseURL = strings.TrimRight(getenv("IMG_PUBLIC_BASE_URL", ""), "/")

	it, err := loadSecret("IMG_INTERNAL_TOKEN", "IMG_INTERNAL_TOKEN_FILE")
	if err != nil {
		return Config{}, err
	}
	c.InternalToken = it

	return c, nil
}

// loadSecret returns the value of a secret from a *_FILE path (preferred) or an
// inline env var. File contents are trimmed of surrounding whitespace/newlines.
func loadSecret(envKey, fileKey string) (string, error) {
	if path := os.Getenv(fileKey); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("config: read %s=%q: %w", fileKey, path, err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	return strings.TrimSpace(os.Getenv(envKey)), nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func setInt(key string, dst *int) error {
	v := os.Getenv(key)
	if v == "" {
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("config: %s must be an integer: %w", key, err)
	}
	*dst = n
	return nil
}

func setInt64(key string, dst *int64) error {
	v := os.Getenv(key)
	if v == "" {
		return nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return fmt.Errorf("config: %s must be an integer: %w", key, err)
	}
	*dst = n
	return nil
}
