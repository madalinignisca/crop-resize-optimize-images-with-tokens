//go:build !vips

package transform

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

// makePNG builds a solid-colour PNG of the given size for use as source bytes.
func makePNG(t *testing.T, w, h int, c color.Color) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func decode(t *testing.T, b []byte) image.Image {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("decode result: %v", err)
	}
	return img
}

func TestNativeResizeContain(t *testing.T) {
	src := makePNG(t, 800, 600, color.NRGBA{R: 10, G: 20, B: 30, A: 255})
	n := NewNative(0)
	res, err := n.Transform(src, Params{Width: 400, Height: 400, Quality: 90, Crop: CropContain, Format: FormatPNG})
	if err != nil {
		t.Fatal(err)
	}
	if res.Width != 400 || res.Height != 300 {
		t.Fatalf("contain gave %dx%d, want 400x300", res.Width, res.Height)
	}
	if res.ContentType != "image/png" {
		t.Fatalf("content type = %q", res.ContentType)
	}
	b := decode(t, res.Bytes).Bounds()
	if b.Dx() != 400 || b.Dy() != 300 {
		t.Fatalf("decoded %dx%d, want 400x300", b.Dx(), b.Dy())
	}
}

func TestNativeResizeCover(t *testing.T) {
	src := makePNG(t, 800, 600, color.NRGBA{R: 100, G: 100, B: 100, A: 255})
	n := NewNative(0)
	res, err := n.Transform(src, Params{Width: 300, Height: 300, Quality: 90, Crop: CropCover, Format: FormatJPEG})
	if err != nil {
		t.Fatal(err)
	}
	if res.Width != 300 || res.Height != 300 {
		t.Fatalf("cover gave %dx%d, want 300x300", res.Width, res.Height)
	}
}

func TestNativeResizeFill(t *testing.T) {
	src := makePNG(t, 800, 600, color.NRGBA{R: 0, G: 0, B: 255, A: 255})
	n := NewNative(0)
	res, err := n.Transform(src, Params{Width: 200, Height: 500, Quality: 90, Crop: CropFill, Format: FormatPNG})
	if err != nil {
		t.Fatal(err)
	}
	if res.Width != 200 || res.Height != 500 {
		t.Fatalf("fill gave %dx%d, want 200x500", res.Width, res.Height)
	}
}

func TestNativePreservesColor(t *testing.T) {
	want := color.NRGBA{R: 200, G: 50, B: 25, A: 255}
	src := makePNG(t, 100, 100, want)
	n := NewNative(0)
	res, err := n.Transform(src, Params{Width: 40, Height: 40, Crop: CropFill, Format: FormatPNG})
	if err != nil {
		t.Fatal(err)
	}
	img := decode(t, res.Bytes)
	r, g, b, _ := img.At(20, 20).RGBA()
	// Allow a small tolerance for resampling; solid colour should be near-exact.
	if abs(int(r>>8)-200) > 4 || abs(int(g>>8)-50) > 4 || abs(int(b>>8)-25) > 4 {
		t.Fatalf("colour drifted: got (%d,%d,%d)", r>>8, g>>8, b>>8)
	}
}

func TestNativeUnsupportedFormat(t *testing.T) {
	src := makePNG(t, 10, 10, color.White)
	n := NewNative(0)
	if _, err := n.Transform(src, Params{Format: FormatWEBP}); err == nil {
		t.Fatal("native should not support webp")
	}
	if n.Supports(FormatWEBP) || n.Supports(FormatAVIF) {
		t.Fatal("native must report webp/avif unsupported")
	}
	if !n.Supports(FormatJPEG) || !n.Supports(FormatPNG) || !n.Supports(FormatGIF) {
		t.Fatal("native must support jpeg/png/gif")
	}
}

func TestNativeSourcePixelGuard(t *testing.T) {
	src := makePNG(t, 200, 200, color.White) // 40,000 px
	n := NewNative(10_000)                   // budget below source
	if _, err := n.Transform(src, Params{Width: 50, Height: 50, Format: FormatPNG}); err == nil {
		t.Fatal("expected source pixel guard to reject oversized source")
	}
}

func TestNativeNoResizeReencode(t *testing.T) {
	// PNG source, JPEG output, no dimensions -> same size, re-encoded.
	src := makePNG(t, 64, 48, color.NRGBA{R: 12, G: 34, B: 56, A: 255})
	n := NewNative(0)
	res, err := n.Transform(src, Params{Quality: 80, Format: FormatJPEG})
	if err != nil {
		t.Fatal(err)
	}
	if res.Width != 64 || res.Height != 48 {
		t.Fatalf("got %dx%d, want 64x48", res.Width, res.Height)
	}
	if _, err := jpeg.Decode(bytes.NewReader(res.Bytes)); err != nil {
		t.Fatalf("output not valid jpeg: %v", err)
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
