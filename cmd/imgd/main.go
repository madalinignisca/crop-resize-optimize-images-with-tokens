// Command imgd is the signed image resizing service.
//
// It exposes a public HTTP endpoint that only accepts opaque, HMAC-signed
// tokens (no raw width/height/quality/crop in the URL), and an optional
// internal endpoint for minting those tokens.
//
// Build the default (pure-Go) binary for development/CI:
//
//	go build ./cmd/imgd
//
// Build the production binary with libvips (webp/avif, best performance):
//
//	CGO_ENABLED=1 go build -tags vips ./cmd/imgd
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/madalinignisca/crop-resize-optimize-images-with-tokens/internal/config"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	srv, err := buildServer(cfg, log)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	public := &http.Server{
		Addr:              cfg.PublicAddr,
		Handler:           srv.PublicHandler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 2)
	go func() {
		log.Info("public listener starting", "addr", cfg.PublicAddr)
		if err := public.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	var internal *http.Server
	if cfg.InternalAddr != "" {
		internal = &http.Server{
			Addr:              cfg.InternalAddr,
			Handler:           srv.InternalHandler(),
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      15 * time.Second,
		}
		go func() {
			log.Info("internal listener starting", "addr", cfg.InternalAddr)
			if err := internal.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- err
			}
		}()
	} else {
		log.Info("internal signing endpoint disabled (IMG_INTERNAL_ADDR unset)")
	}

	select {
	case <-ctx.Done():
		log.Info("shutdown signal received")
	case err := <-errCh:
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = public.Shutdown(shutdownCtx)
	if internal != nil {
		_ = internal.Shutdown(shutdownCtx)
	}
	return nil
}
