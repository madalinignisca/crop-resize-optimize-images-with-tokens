//go:build !vips

package main

import "github.com/madalinignisca/crop-resize-optimize-images-with-tokens/internal/transform"

// newTransformer returns the pure-Go backend (jpeg/png/gif). This is the
// default build, used where libvips is not available.
func newTransformer(b transform.Bounds) transform.Transformer {
	return transform.NewNative(b)
}
