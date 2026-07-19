package transform

import "testing"

func TestClampCapsDimensions(t *testing.T) {
	b := Bounds{MaxWidth: 4000, MaxHeight: 4000, MaxOutputPixel: 16_000_000, DefaultQuality: 82}
	got := Clamp(Params{Width: 99999, Height: 50, Quality: 90, Format: FormatJPEG}, b)
	if got.Width != 4000 {
		t.Errorf("width not capped: %d", got.Width)
	}
	if got.Height != 50 {
		t.Errorf("height changed unexpectedly: %d", got.Height)
	}
}

func TestClampOutputPixelBudget(t *testing.T) {
	b := Bounds{MaxWidth: 10000, MaxHeight: 10000, MaxOutputPixel: 1_000_000, DefaultQuality: 82}
	// 2000x2000 = 4,000,000 px > 1,000,000; expect proportional downscale to ~1000x1000.
	got := Clamp(Params{Width: 2000, Height: 2000, Format: FormatJPEG}, b)
	if pixels := int64(got.Width) * int64(got.Height); pixels > b.MaxOutputPixel {
		t.Fatalf("clamp left %d pixels, over budget %d", pixels, b.MaxOutputPixel)
	}
	if got.Width != got.Height {
		t.Errorf("aspect ratio not preserved: %dx%d", got.Width, got.Height)
	}
}

func TestClampQualityDefaultsAndBounds(t *testing.T) {
	b := Bounds{DefaultQuality: 82}
	if got := Clamp(Params{Quality: 0, Format: FormatJPEG}, b); got.Quality != 82 {
		t.Errorf("quality 0 should default to 82, got %d", got.Quality)
	}
	if got := Clamp(Params{Quality: 250, Format: FormatJPEG}, b); got.Quality != 100 {
		t.Errorf("quality should cap at 100, got %d", got.Quality)
	}
}

func TestClampDefaultsFormat(t *testing.T) {
	if got := Clamp(Params{}, Bounds{DefaultQuality: 80}); got.Format != FormatJPEG {
		t.Errorf("empty format should default to jpeg, got %q", got.Format)
	}
}

func TestNormalizeCrop(t *testing.T) {
	cases := map[string]string{
		"cover": CropCover, "COVER": CropCover, "fill": CropFill,
		"contain": CropContain, "": CropContain, "garbage": CropContain,
	}
	for in, want := range cases {
		if got := NormalizeCrop(in); got != want {
			t.Errorf("NormalizeCrop(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeFormat(t *testing.T) {
	cases := map[string]string{
		"jpg": FormatJPEG, "jpeg": FormatJPEG, "png": FormatPNG,
		"webp": FormatWEBP, "avif": FormatAVIF, "gif": FormatGIF,
		"": "", "bmp": "",
	}
	for in, want := range cases {
		if got := NormalizeFormat(in); got != want {
			t.Errorf("NormalizeFormat(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClampDims(t *testing.T) {
	b := Bounds{MaxWidth: 4000, MaxHeight: 4000, MaxOutputPixel: 1_000_000}

	// Within all bounds: unchanged.
	if w, h := ClampDims(800, 600, b); w != 800 || h != 600 {
		t.Errorf("in-bounds changed: %dx%d", w, h)
	}

	// Exceeds a single dimension: scale down, aspect ratio preserved.
	if w, h := ClampDims(200, 8000, b); h > 4000 {
		t.Errorf("height not capped: %dx%d", w, h)
	}

	// Exceeds pixel budget: scaled to fit, aspect ratio preserved.
	w, h := ClampDims(2000, 2000, b) // 4,000,000 px > 1,000,000
	if int64(w)*int64(h) > b.MaxOutputPixel {
		t.Errorf("pixel budget exceeded: %d px", int64(w)*int64(h))
	}
	if w != h {
		t.Errorf("square aspect not preserved: %dx%d", w, h)
	}

	// A wildly proportional derived size is bounded on every axis.
	w, h = ClampDims(4000, 400_000_000, b)
	if w > 4000 || h > 4000 || int64(w)*int64(h) > b.MaxOutputPixel {
		t.Errorf("extreme dims not clamped: %dx%d", w, h)
	}

	// Disabled bounds (all zero): unchanged.
	if w, h := ClampDims(99999, 88888, Bounds{}); w != 99999 || h != 88888 {
		t.Errorf("zero bounds should not clamp: %dx%d", w, h)
	}
}

func TestResizeGeometry(t *testing.T) {
	tests := []struct {
		name         string
		sw, sh, w, h int
		crop         string
		wantW, wantH int
		wantCropW    int
		wantCropH    int
	}{
		{"no-resize", 800, 600, 0, 0, CropContain, 800, 600, 800, 600},
		{"by-width", 800, 600, 400, 0, CropContain, 400, 300, 800, 600},
		{"by-height", 800, 600, 0, 300, CropContain, 400, 300, 800, 600},
		{"contain-landscape-into-square", 800, 600, 400, 400, CropContain, 400, 300, 800, 600},
		{"fill-stretch", 800, 600, 400, 400, CropFill, 400, 400, 800, 600},
		{"cover-square", 800, 600, 300, 300, CropCover, 300, 300, 600, 600},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rect, dw, dh := resizeGeometry(tt.sw, tt.sh, tt.w, tt.h, tt.crop)
			if dw != tt.wantW || dh != tt.wantH {
				t.Errorf("dst = %dx%d, want %dx%d", dw, dh, tt.wantW, tt.wantH)
			}
			if rect.Dx() != tt.wantCropW || rect.Dy() != tt.wantCropH {
				t.Errorf("crop rect = %dx%d, want %dx%d", rect.Dx(), rect.Dy(), tt.wantCropW, tt.wantCropH)
			}
		})
	}
}
