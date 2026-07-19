//go:build !vips

package transform

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
)

// Native is the default, pure-Go, stdlib-only backend. It supports jpeg, png
// and gif, which is enough for development, tests and CI where libvips is not
// installed. Production builds should use the vips backend (build tag "vips")
// for webp/avif and libvips performance.
//
// Resizing uses a bilinear resampler (see resize.go). Quality applies to jpeg
// output; png/gif ignore it.
type Native struct {
	// bounds carries the server-side limits: the source-pixel guard and the
	// output clamp (MaxWidth/MaxHeight/MaxOutputPixel) applied to the derived
	// render size.
	bounds Bounds
}

// NewNative builds the default backend with the given server-side bounds. A
// zero MaxSourcePixel disables the source-dimension guard.
func NewNative(b Bounds) *Native {
	return &Native{bounds: b}
}

func (n *Native) Name() string { return "native (pure-Go stdlib)" }

func (n *Native) Supports(format string) bool {
	switch format {
	case FormatJPEG, FormatPNG, FormatGIF:
		return true
	default:
		return false
	}
}

func (n *Native) Transform(src []byte, p Params) (Result, error) {
	if !n.Supports(p.Format) {
		return Result{}, fmt.Errorf("%w: native backend cannot encode %q", ErrUnsupportedFormat, p.Format)
	}

	// Header-only dimension check BEFORE the full decode — this is the
	// decompression-bomb guard. DecodeConfig reads just the header, so an
	// oversized source is rejected without ever allocating its pixel buffer.
	if n.bounds.MaxSourcePixel > 0 {
		cfg, _, err := image.DecodeConfig(bytes.NewReader(src))
		if err != nil {
			return Result{}, fmt.Errorf("transform: decode config: %w", err)
		}
		if px := int64(cfg.Width) * int64(cfg.Height); px > n.bounds.MaxSourcePixel {
			return Result{}, fmt.Errorf("transform: source %dx%d exceeds max source pixels %d",
				cfg.Width, cfg.Height, n.bounds.MaxSourcePixel)
		}
	}

	img, _, err := image.Decode(bytes.NewReader(src))
	if err != nil {
		return Result{}, fmt.Errorf("transform: decode: %w", err)
	}
	if b := img.Bounds(); b.Dx() <= 0 || b.Dy() <= 0 {
		return Result{}, fmt.Errorf("transform: source has empty dimensions %dx%d", b.Dx(), b.Dy())
	}

	out := resizeCrop(img, p.Width, p.Height, p.Crop, n.bounds)
	b := out.Bounds()

	var buf bytes.Buffer
	switch p.Format {
	case FormatJPEG:
		err = jpeg.Encode(&buf, out, &jpeg.Options{Quality: p.Quality})
	case FormatPNG:
		enc := png.Encoder{CompressionLevel: png.DefaultCompression}
		err = enc.Encode(&buf, out)
	case FormatGIF:
		err = gif.Encode(&buf, out, nil)
	default:
		err = errors.New("unreachable")
	}
	if err != nil {
		return Result{}, fmt.Errorf("transform: encode %s: %w", p.Format, err)
	}

	return Result{
		Bytes:       buf.Bytes(),
		ContentType: ContentTypeFor(p.Format),
		Width:       b.Dx(),
		Height:      b.Dy(),
	}, nil
}
