package transform

import (
	"image"
	"math"
)

// resizeGeometry computes, for a source of sw x sh, the source sub-rectangle to
// sample and the destination dimensions to render, given the requested w/h and
// crop mode. The returned rectangle is expressed in a 0,0-origin coordinate
// space (i.e. relative to the source content, not its image.Bounds().Min).
//
// The crop semantics match the constants documented in transform.go and are
// implemented identically here and, conceptually, in the vips backend.
func resizeGeometry(sw, sh, w, h int, crop string) (srcRect image.Rectangle, dstW, dstH int) {
	full := image.Rect(0, 0, sw, sh)
	if sw <= 0 || sh <= 0 {
		return full, maxInt(sw, 1), maxInt(sh, 1)
	}

	switch {
	case w <= 0 && h <= 0:
		// No resize; re-encode at original size.
		return full, sw, sh

	case w > 0 && h <= 0:
		// Proportional by width.
		return full, w, round(float64(sh) * float64(w) / float64(sw))

	case w <= 0 && h > 0:
		// Proportional by height.
		return full, round(float64(sw) * float64(h) / float64(sh)), h
	}

	// Both dimensions specified.
	switch crop {
	case CropFill:
		return full, w, h

	case CropCover:
		// Scale so the source covers the box, then centre-crop in source space.
		scale := math.Max(float64(w)/float64(sw), float64(h)/float64(sh))
		cropW := clampInt(round(float64(w)/scale), 1, sw)
		cropH := clampInt(round(float64(h)/scale), 1, sh)
		x0 := (sw - cropW) / 2
		y0 := (sh - cropH) / 2
		return image.Rect(x0, y0, x0+cropW, y0+cropH), w, h

	default: // CropContain
		scale := math.Min(float64(w)/float64(sw), float64(h)/float64(sh))
		return full, maxInt(1, round(float64(sw)*scale)), maxInt(1, round(float64(sh)*scale))
	}
}

func round(f float64) int { return int(math.Round(f)) }

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
