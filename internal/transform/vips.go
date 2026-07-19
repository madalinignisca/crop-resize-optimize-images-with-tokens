//go:build vips

// This file is compiled only with the "vips" build tag and requires cgo plus a
// system libvips (via github.com/h2non/bimg). It is the intended production
// backend. Build with:
//
//	CGO_ENABLED=1 go build -tags vips ./cmd/imgd
//
// The pure-Go Native backend (native.go) is used for every other build so that
// tests and CI run without libvips installed.
package transform

import (
	"fmt"

	"github.com/h2non/bimg"
)

// Vips is the libvips-backed backend. It reuses the exact geometry rules from
// geometry.go so that "contain", "cover" and "fill" produce dimensions
// consistent with the native backend, delegating only the pixel work to
// libvips.
type Vips struct {
	MaxSourcePixel int64
}

// NewVips builds the libvips backend. maxSourcePixel of 0 disables the
// source-dimension guard.
func NewVips(maxSourcePixel int64) *Vips {
	return &Vips{MaxSourcePixel: maxSourcePixel}
}

func (v *Vips) Name() string { return fmt.Sprintf("vips (libvips %s)", bimg.VipsVersion) }

func (v *Vips) Supports(format string) bool {
	switch format {
	case FormatJPEG, FormatPNG, FormatGIF, FormatWEBP, FormatAVIF:
		return true
	default:
		return false
	}
}

func (v *Vips) Transform(src []byte, p Params) (Result, error) {
	imgType, ok := vipsType(p.Format)
	if !ok {
		return Result{}, fmt.Errorf("%w: %q", ErrUnsupportedFormat, p.Format)
	}

	img := bimg.NewImage(src)
	size, err := img.Size()
	if err != nil {
		return Result{}, fmt.Errorf("transform: read source size: %w", err)
	}
	if v.MaxSourcePixel > 0 {
		if px := int64(size.Width) * int64(size.Height); px > v.MaxSourcePixel {
			return Result{}, fmt.Errorf("transform: source %dx%d exceeds max source pixels %d",
				size.Width, size.Height, v.MaxSourcePixel)
		}
	}

	opts := bimg.Options{
		Quality:       p.Quality,
		Type:          imgType,
		Enlarge:       true,
		StripMetadata: true,
	}

	switch p.Crop {
	case CropCover:
		// Let libvips scale-to-cover and centre-crop to exactly WxH.
		opts.Width = p.Width
		opts.Height = p.Height
		opts.Crop = true
		opts.Gravity = bimg.GravityCentre
	default:
		// contain / fill / proportional / no-resize: compute the exact output
		// dimensions with the shared geometry, then force libvips to them. For
		// "fill" these are w,h; for the others they preserve aspect ratio, so
		// Force introduces no distortion.
		_, dstW, dstH := resizeGeometry(size.Width, size.Height, p.Width, p.Height, p.Crop)
		opts.Width = dstW
		opts.Height = dstH
		opts.Force = true
	}

	out, err := img.Process(opts)
	if err != nil {
		return Result{}, fmt.Errorf("transform: libvips process: %w", err)
	}

	res := Result{Bytes: out, ContentType: ContentTypeFor(p.Format)}
	if meta, err := bimg.NewImage(out).Size(); err == nil {
		res.Width = meta.Width
		res.Height = meta.Height
	}
	return res, nil
}

func vipsType(format string) (bimg.ImageType, bool) {
	switch format {
	case FormatJPEG:
		return bimg.JPEG, true
	case FormatPNG:
		return bimg.PNG, true
	case FormatGIF:
		return bimg.GIF, true
	case FormatWEBP:
		return bimg.WEBP, true
	case FormatAVIF:
		return bimg.AVIF, true
	default:
		return 0, false
	}
}
