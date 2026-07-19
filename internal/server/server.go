// Package server wires the signed-token image service together: a public
// endpoint that verifies tokens and serves cached/rendered images, and an
// optional internal endpoint that mints tokens.
//
// The two are deliberately separate http.Handlers so they can be bound to
// different listeners/ports. The public handler must never accept raw
// width/height/quality/crop parameters — only the opaque token carries them.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/madalinignisca/crop-resize-optimize-images-with-tokens/internal/cache"
	"github.com/madalinignisca/crop-resize-optimize-images-with-tokens/internal/source"
	"github.com/madalinignisca/crop-resize-optimize-images-with-tokens/internal/token"
	"github.com/madalinignisca/crop-resize-optimize-images-with-tokens/internal/transform"
)

// Options configures a Server.
type Options struct {
	Signer        *token.Signer
	Source        source.Source
	Cache         cache.Cache
	Transformer   transform.Transformer
	Bounds        transform.Bounds
	CacheMaxAge   int    // seconds for Cache-Control on images
	InternalToken string // shared secret guarding /internal/sign; "" = no header check
	PublicBaseURL string // absolute base for sign URLs; "" = relative paths
	Logger        *slog.Logger
	Now           func() time.Time // injectable clock; defaults to time.Now
}

// Server holds the resolved dependencies and serves HTTP.
type Server struct {
	opt Options
	now func() time.Time
	log *slog.Logger
}

// New validates opt and returns a Server.
func New(opt Options) (*Server, error) {
	if opt.Signer == nil || opt.Source == nil || opt.Cache == nil || opt.Transformer == nil {
		return nil, errors.New("server: signer, source, cache and transformer are required")
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if opt.Logger == nil {
		opt.Logger = slog.Default()
	}
	if opt.CacheMaxAge <= 0 {
		opt.CacheMaxAge = 31_536_000
	}
	return &Server{opt: opt, now: opt.Now, log: opt.Logger}, nil
}

// PublicHandler returns the public-facing router: token-gated image delivery
// plus a health check. No transform parameters are accepted here except via the
// signed token.
func (s *Server) PublicHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /img/{token}/{filename...}", s.serveImage)
	mux.HandleFunc("GET /img/{token}", s.serveImage)
	mux.HandleFunc("GET /healthz", s.health)
	return recoverer(s.log, mux)
}

// InternalHandler returns the internal signing router. Mount this only on a
// non-public listener, or protect it with InternalToken.
func (s *Server) InternalHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /internal/sign", s.sign)
	mux.HandleFunc("GET /healthz", s.health)
	return recoverer(s.log, mux)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"status":"ok","backend":%q}`, s.opt.Transformer.Name())
}

func (s *Server) serveImage(w http.ResponseWriter, r *http.Request) {
	tok := r.PathValue("token")

	// 1-3. Verify signature/expiry BEFORE any image work. Every failure is a
	// 403 with no detail leaked to the client.
	p, err := s.opt.Signer.Verify(tok, s.now())
	if err != nil {
		s.log.Info("token rejected", "err", err, "remote", clientIP(r))
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	// 4-5. Resolve output format, translate + clamp parameters.
	tp := transform.Params{
		Width:   p.W,
		Height:  p.H,
		Quality: p.Q,
		Crop:    transform.NormalizeCrop(p.Crop),
		Format:  s.resolveFormat(p.Fmt, r.Header.Get("Accept")),
	}
	tp = transform.Clamp(tp, s.opt.Bounds)

	key := cache.Key(p.Src, tp.String())
	etag := `"` + key + `"`

	// Conditional GET: if the client already holds this exact render, skip work.
	if match := r.Header.Get("If-None-Match"); match != "" && etagMatch(match, etag) {
		s.setImageHeaders(w, "", etag, 0)
		w.WriteHeader(http.StatusNotModified)
		return
	}

	// 6. Cache lookup.
	if entry, err := s.opt.Cache.Get(key); err == nil {
		s.writeImage(w, r, entry, etag)
		return
	} else if !errors.Is(err, cache.ErrMiss) {
		s.log.Error("cache get failed", "err", err, "key", key)
		// Fall through and regenerate rather than failing the request.
	}

	// Cache miss: load source, render, store, serve.
	src, err := s.opt.Source.Open(p.Src)
	if err != nil {
		switch {
		case errors.Is(err, source.ErrNotFound):
			http.Error(w, "not found", http.StatusNotFound)
		case errors.Is(err, source.ErrForbidden):
			s.log.Warn("source path rejected", "src", p.Src, "err", err)
			http.Error(w, "forbidden", http.StatusForbidden)
		default:
			s.log.Error("source open failed", "src", p.Src, "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}

	res, err := s.opt.Transformer.Transform(src, tp)
	if err != nil {
		if errors.Is(err, transform.ErrUnsupportedFormat) {
			http.Error(w, "unsupported media type", http.StatusUnsupportedMediaType)
			return
		}
		s.log.Error("transform failed", "src", p.Src, "params", tp.String(), "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	entry := cache.Entry{Bytes: res.Bytes, ContentType: res.ContentType, ETag: key}
	if err := s.opt.Cache.Put(key, entry); err != nil {
		// A cache write failure must not fail the response — just log it.
		s.log.Error("cache put failed", "err", err, "key", key)
	}
	s.writeImage(w, r, entry, etag)
}

func (s *Server) writeImage(w http.ResponseWriter, r *http.Request, e cache.Entry, etag string) {
	s.setImageHeaders(w, e.ContentType, etag, len(e.Bytes))
	if r.Method == http.MethodHead {
		return
	}
	if _, err := w.Write(e.Bytes); err != nil {
		s.log.Debug("write body failed", "err", err)
	}
}

func (s *Server) setImageHeaders(w http.ResponseWriter, contentType, etag string, size int) {
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if size > 0 {
		w.Header().Set("Content-Length", strconv.Itoa(size))
	}
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", s.opt.CacheMaxAge))
	w.Header().Set("Vary", "Accept")
}

// resolveFormat decides the output format. An explicit, supported token format
// wins. Otherwise it negotiates from the Accept header (preferring avif, then
// webp), falling back to jpeg or any format the backend supports.
func (s *Server) resolveFormat(explicit, accept string) string {
	if f := transform.NormalizeFormat(explicit); f != "" && s.opt.Transformer.Supports(f) {
		return f
	}
	for _, f := range []string{transform.FormatAVIF, transform.FormatWEBP} {
		if s.opt.Transformer.Supports(f) && acceptsType(accept, transform.ContentTypeFor(f)) {
			return f
		}
	}
	if s.opt.Transformer.Supports(transform.FormatJPEG) {
		return transform.FormatJPEG
	}
	for _, f := range []string{transform.FormatPNG, transform.FormatWEBP, transform.FormatAVIF, transform.FormatGIF} {
		if s.opt.Transformer.Supports(f) {
			return f
		}
	}
	return transform.FormatJPEG
}

// --- signing endpoint ---

type signRequest struct {
	Src      string `json:"src"`
	W        int    `json:"w"`
	H        int    `json:"h"`
	Q        int    `json:"q"`
	Crop     string `json:"crop"`
	Fmt      string `json:"fmt"`
	Exp      int64  `json:"exp"`
	Filename string `json:"filename"` // optional extension hint for the public URL
}

type signResponse struct {
	Token string `json:"token"`
	URL   string `json:"url"`
}

func (s *Server) sign(w http.ResponseWriter, r *http.Request) {
	if s.opt.InternalToken != "" {
		if !constantTimeHeader(r.Header.Get("X-Internal-Auth"), s.opt.InternalToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}
	var req signRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Src) == "" {
		http.Error(w, "bad request: src is required", http.StatusBadRequest)
		return
	}

	tok, err := s.opt.Signer.Sign(token.Params{
		Src:  req.Src,
		W:    req.W,
		H:    req.H,
		Q:    req.Q,
		Crop: transform.NormalizeCrop(req.Crop),
		Fmt:  transform.NormalizeFormat(req.Fmt),
		Exp:  req.Exp,
	})
	if err != nil {
		s.log.Error("sign failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	resp := signResponse{Token: tok, URL: s.opt.PublicBaseURL + BuildPath(tok, req.Filename)}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// BuildPath builds the public URL path for a token. filename is an optional
// extension hint; if empty, "image" is used. The hint never affects the render
// (the token governs that) — it just gives the browser a sensible name.
func BuildPath(tok, filename string) string {
	name := strings.TrimSpace(filename)
	if name == "" {
		name = "image"
	}
	// Guard against a filename smuggling extra path segments.
	name = strings.ReplaceAll(name, "/", "_")
	return "/img/" + tok + "/" + name
}
