//go:build vips

package main

import "github.com/madalinignisca/crop-resize-optimize-images-with-tokens/internal/transform"

// newTransformer returns the libvips backend (jpeg/png/gif/webp/avif). Built
// only with the "vips" tag; requires cgo and a system libvips.
func newTransformer(b transform.Bounds) transform.Transformer {
	return transform.NewVips(b)
}
