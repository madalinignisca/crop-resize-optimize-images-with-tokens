//go:build !vips

package transform

import (
	"image"
	"image/draw"
)

// resizeCrop resamples src to the geometry implied by w/h/crop using bilinear
// interpolation. It always returns an *image.NRGBA with a 0,0 origin.
func resizeCrop(src image.Image, w, h int, crop string) image.Image {
	nr := toNRGBA(src)
	b := nr.Bounds() // origin 0,0
	region, dstW, dstH := resizeGeometry(b.Dx(), b.Dy(), w, h, crop)

	// Fast path: nothing to do.
	if dstW == b.Dx() && dstH == b.Dy() && region == b {
		return nr
	}

	dst := image.NewNRGBA(image.Rect(0, 0, dstW, dstH))
	rw, rh := region.Dx(), region.Dy()

	for y := 0; y < dstH; y++ {
		// Map destination row centre back into the source region.
		sy := float64(region.Min.Y) + (float64(y)+0.5)*float64(rh)/float64(dstH) - 0.5
		y0, y1, ty := neighbors(sy, region.Min.Y, region.Max.Y-1)
		for x := 0; x < dstW; x++ {
			sx := float64(region.Min.X) + (float64(x)+0.5)*float64(rw)/float64(dstW) - 0.5
			x0, x1, tx := neighbors(sx, region.Min.X, region.Max.X-1)

			c00 := pixel(nr, x0, y0)
			c10 := pixel(nr, x1, y0)
			c01 := pixel(nr, x0, y1)
			c11 := pixel(nr, x1, y1)

			o := dst.PixOffset(x, y)
			for i := 0; i < 4; i++ {
				top := lerp(c00[i], c10[i], tx)
				bot := lerp(c01[i], c11[i], tx)
				dst.Pix[o+i] = uint8(lerp(top, bot, ty) + 0.5)
			}
		}
	}
	return dst
}

// toNRGBA returns src as an *image.NRGBA anchored at 0,0, converting if needed.
func toNRGBA(src image.Image) *image.NRGBA {
	if n, ok := src.(*image.NRGBA); ok && n.Rect.Min == (image.Point{}) {
		return n
	}
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	return dst
}

// neighbors returns the two integer sample coordinates bracketing f (clamped to
// [lo,hi]) and the interpolation weight toward the second.
func neighbors(f float64, lo, hi int) (a, b int, t float64) {
	fl := int(floor(f))
	t = f - float64(fl)
	a = clampInt(fl, lo, hi)
	b = clampInt(fl+1, lo, hi)
	return a, b, t
}

func pixel(img *image.NRGBA, x, y int) [4]float64 {
	o := img.PixOffset(x, y)
	return [4]float64{
		float64(img.Pix[o+0]),
		float64(img.Pix[o+1]),
		float64(img.Pix[o+2]),
		float64(img.Pix[o+3]),
	}
}

func lerp(a, b, t float64) float64 { return a + (b-a)*t }

func floor(f float64) float64 {
	i := float64(int(f))
	if f < i {
		return i - 1
	}
	return i
}
