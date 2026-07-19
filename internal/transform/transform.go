// Package transform defines the image-transform contract shared by every
// backend, plus the server-side clamping that keeps a validly-signed token from
// asking for a decompression-bomb-sized render.
//
// Two backends implement Transformer:
//
//   - native  (default build): pure-Go, stdlib only, jpeg/png/gif. Used for
//     development, tests, and CI where libvips is not installed.
//   - vips     (build tag "vips"): bimg/libvips, adds webp/avif and is the
//     intended production backend.
package transform

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

// Crop modes. Semantics are identical across backends:
//
//   - "" / CropContain: scale to fit *within* WxH, preserving aspect ratio
//     (output may be smaller than the box in one dimension). This is also the
//     behaviour when only one of W/H is given.
//   - CropCover: scale to cover WxH, then centre-crop to exactly WxH.
//   - CropFill: stretch to exactly WxH, ignoring aspect ratio.
const (
	CropContain = "contain"
	CropCover   = "cover"
	CropFill    = "fill"
)

// Supported canonical output formats.
const (
	FormatJPEG = "jpeg"
	FormatPNG  = "png"
	FormatGIF  = "gif"
	FormatWEBP = "webp"
	FormatAVIF = "avif"
)

// ErrUnsupportedFormat is returned by a backend asked to encode a format it does
// not support.
var ErrUnsupportedFormat = errors.New("transform: unsupported output format")

// Params is a fully-resolved, clamped transform request handed to a backend.
type Params struct {
	Width   int    // 0 = keep/auto
	Height  int    // 0 = keep/auto
	Quality int    // 1..100
	Crop    string // one of the Crop* constants (normalised)
	Format  string // one of the Format* constants (never empty once resolved)
}

// Bounds are the hard server-side limits enforced regardless of token contents.
type Bounds struct {
	MaxWidth       int   // hard cap on output width (e.g. 4000)
	MaxHeight      int   // hard cap on output height (e.g. 4000)
	MaxOutputPixel int64 // hard cap on output Width*Height (decompression-bomb guard)
	MaxSourcePixel int64 // hard cap on decoded source Width*Height
	DefaultQuality int   // used when a token requests quality 0
}

// Result is a backend's encoded output.
type Result struct {
	Bytes       []byte
	ContentType string
	Width       int
	Height      int
}

// Transformer renders source image bytes into a transformed, encoded result.
// Implementations must be safe for concurrent use.
type Transformer interface {
	Transform(src []byte, p Params) (Result, error)
	// Supports reports whether the backend can encode the given canonical
	// format. The handler uses this to negotiate a fallback.
	Supports(format string) bool
	// Name identifies the backend, for logging/health output.
	Name() string
}

// NormalizeCrop maps a raw token crop value to a canonical constant. Unknown or
// empty values become CropContain.
func NormalizeCrop(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case CropCover:
		return CropCover
	case CropFill:
		return CropFill
	default:
		return CropContain
	}
}

// NormalizeFormat maps a raw token/Accept format value to a canonical constant.
// Returns "" (meaning "unspecified / negotiate") for empty or unknown input.
func NormalizeFormat(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "jpg", "jpeg":
		return FormatJPEG
	case "png":
		return FormatPNG
	case "gif":
		return FormatGIF
	case "webp":
		return FormatWEBP
	case "avif":
		return FormatAVIF
	default:
		return ""
	}
}

// ContentTypeFor returns the MIME type for a canonical format.
func ContentTypeFor(format string) string {
	switch format {
	case FormatJPEG:
		return "image/jpeg"
	case FormatPNG:
		return "image/png"
	case FormatGIF:
		return "image/gif"
	case FormatWEBP:
		return "image/webp"
	case FormatAVIF:
		return "image/avif"
	default:
		return "application/octet-stream"
	}
}

// Clamp bounds the *requested* parameters. It caps the explicitly-requested
// Width/Height (which also keeps the geometry intermediates in native/vips
// small) and normalises quality/format.
//
// It is NOT the last line of defense on output size: a proportional request
// (one of Width/Height is 0) or a no-resize request (both 0) has its final
// dimensions derived from the source aspect ratio, which Clamp cannot see.
// Backends must therefore run [ClampDims] on the geometry output before
// allocating pixels — that is what actually stops a decompression-bomb render.
//
//   - Width/Height are capped to MaxWidth/MaxHeight.
//   - If Width*Height still exceeds MaxOutputPixel, both are scaled down
//     proportionally until they fit, preserving the requested aspect ratio.
//   - Quality is coerced into 1..100, defaulting to DefaultQuality when 0.
func Clamp(p Params, b Bounds) Params {
	if p.Width < 0 {
		p.Width = 0
	}
	if p.Height < 0 {
		p.Height = 0
	}
	if b.MaxWidth > 0 && p.Width > b.MaxWidth {
		p.Width = b.MaxWidth
	}
	if b.MaxHeight > 0 && p.Height > b.MaxHeight {
		p.Height = b.MaxHeight
	}

	if b.MaxOutputPixel > 0 && p.Width > 0 && p.Height > 0 {
		if pixels := int64(p.Width) * int64(p.Height); pixels > b.MaxOutputPixel {
			// Scale both dimensions by sqrt(limit/pixels) so aspect ratio holds.
			scale := math.Sqrt(float64(b.MaxOutputPixel) / float64(pixels))
			p.Width = maxInt(1, int(float64(p.Width)*scale))
			p.Height = maxInt(1, int(float64(p.Height)*scale))
		}
	}

	if p.Quality == 0 {
		p.Quality = b.DefaultQuality
	}
	if p.Quality < 1 {
		p.Quality = 1
	}
	if p.Quality > 100 {
		p.Quality = 100
	}
	if p.Format == "" {
		p.Format = FormatJPEG
	}
	return p
}

// ClampDims scales a concrete, already-computed output size down until it
// satisfies MaxWidth, MaxHeight and MaxOutputPixel, preserving aspect ratio. It
// is applied by every backend to the dimensions produced by resizeGeometry, so
// that proportional and no-resize requests — whose output size is derived from
// the source, not from the token — are bounded just like explicit ones. This is
// the guard that prevents a validly-signed token from driving an unbounded
// (OOM-inducing) allocation.
func ClampDims(w, h int, b Bounds) (int, int) {
	if w <= 0 || h <= 0 {
		return maxInt(w, 1), maxInt(h, 1)
	}
	scale := 1.0
	if b.MaxWidth > 0 && w > b.MaxWidth {
		scale = math.Min(scale, float64(b.MaxWidth)/float64(w))
	}
	if b.MaxHeight > 0 && h > b.MaxHeight {
		scale = math.Min(scale, float64(b.MaxHeight)/float64(h))
	}
	if b.MaxOutputPixel > 0 {
		if pixels := int64(w) * int64(h); pixels > b.MaxOutputPixel {
			scale = math.Min(scale, math.Sqrt(float64(b.MaxOutputPixel)/float64(pixels)))
		}
	}
	if scale < 1.0 {
		w = maxInt(1, int(float64(w)*scale))
		h = maxInt(1, int(float64(h)*scale))
	}
	return w, h
}

// String renders Params in a stable form used as part of the cache key.
func (p Params) String() string {
	return fmt.Sprintf("w=%d;h=%d;q=%d;crop=%s;fmt=%s", p.Width, p.Height, p.Quality, p.Crop, p.Format)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
