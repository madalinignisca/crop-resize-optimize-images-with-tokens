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
	// MaxSourcePixel rejects source images whose decoded dimensions exceed this
	// pixel count, guarding against decompression bombs before full decode.
	MaxSourcePixel int64
}

// NewNative builds the default backend. maxSourcePixel of 0 disables the
// source-dimension guard.
func NewNative(maxSourcePixel int64) *Native {
	return &Native{MaxSourcePixel: maxSourcePixel}
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

	// Cheap dimension check before decoding pixels — decompression-bomb guard.
	if n.MaxSourcePixel > 0 {
		cfg, _, err := image.DecodeConfig(bytes.NewReader(src))
		if err != nil {
			return Result{}, fmt.Errorf("transform: decode config: %w", err)
		}
		if px := int64(cfg.Width) * int64(cfg.Height); px > n.MaxSourcePixel {
			return Result{}, fmt.Errorf("transform: source %dx%d exceeds max source pixels %d",
				cfg.Width, cfg.Height, n.MaxSourcePixel)
		}
	}

	img, _, err := image.Decode(bytes.NewReader(src))
	if err != nil {
		return Result{}, fmt.Errorf("transform: decode: %w", err)
	}

	out := resizeCrop(img, p.Width, p.Height, p.Crop)
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
