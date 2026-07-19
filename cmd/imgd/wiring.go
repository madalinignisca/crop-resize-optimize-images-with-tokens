package main

import (
	"log/slog"

	"github.com/madalinignisca/crop-resize-optimize-images-with-tokens/internal/cache"
	"github.com/madalinignisca/crop-resize-optimize-images-with-tokens/internal/config"
	"github.com/madalinignisca/crop-resize-optimize-images-with-tokens/internal/server"
	"github.com/madalinignisca/crop-resize-optimize-images-with-tokens/internal/source"
	"github.com/madalinignisca/crop-resize-optimize-images-with-tokens/internal/token"
	"github.com/madalinignisca/crop-resize-optimize-images-with-tokens/internal/transform"
)

// buildServer assembles all dependencies from config. The concrete image
// backend is selected at compile time by build tag (see backend_native.go /
// backend_vips.go).
func buildServer(cfg config.Config, log *slog.Logger) (*server.Server, error) {
	sg, err := token.NewSigner(cfg.SignatureBytes, cfg.PrimarySecret, cfg.PreviousSecrets...)
	if err != nil {
		return nil, err
	}
	src, err := source.NewDir(cfg.SourceDir, cfg.SourceAllow, cfg.MaxSourceByte)
	if err != nil {
		return nil, err
	}
	c, err := cache.NewDisk(cfg.CacheDir)
	if err != nil {
		return nil, err
	}
	tr := newTransformer(cfg.MaxSourcePixel)
	log.Info("image backend selected", "backend", tr.Name())

	return server.New(server.Options{
		Signer:      sg,
		Source:      src,
		Cache:       c,
		Transformer: tr,
		Bounds: transform.Bounds{
			MaxWidth:       cfg.MaxWidth,
			MaxHeight:      cfg.MaxHeight,
			MaxOutputPixel: cfg.MaxOutputPixel,
			DefaultQuality: cfg.DefaultQuality,
		},
		CacheMaxAge:   cfg.CacheMaxAge,
		InternalToken: cfg.InternalToken,
		PublicBaseURL: cfg.PublicBaseURL,
		Logger:        log,
	})
}
