package server

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madalinignisca/crop-resize-optimize-images-with-tokens/internal/cache"
	"github.com/madalinignisca/crop-resize-optimize-images-with-tokens/internal/source"
	"github.com/madalinignisca/crop-resize-optimize-images-with-tokens/internal/token"
	"github.com/madalinignisca/crop-resize-optimize-images-with-tokens/internal/transform"
)

// countingTransformer wraps a Transformer and counts Transform calls so tests
// can assert cache behaviour.
type countingTransformer struct {
	inner transform.Transformer
	calls int
}

func (c *countingTransformer) Transform(src []byte, p transform.Params) (transform.Result, error) {
	c.calls++
	return c.inner.Transform(src, p)
}
func (c *countingTransformer) Supports(f string) bool { return c.inner.Supports(f) }
func (c *countingTransformer) Name() string           { return c.inner.Name() }

func writePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{R: 40, G: 80, B: 120, A: 255})
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

type testEnv struct {
	srv    *Server
	signer *token.Signer
	tr     *countingTransformer
	pub    http.Handler
	intr   http.Handler
}

func newTestEnv(t *testing.T) testEnv {
	t.Helper()
	srcDir := t.TempDir()
	cacheDir := t.TempDir()
	writePNG(t, filepath.Join(srcDir, "products", "1.png"), 800, 600)

	sg, err := token.NewSigner(token.FullSigBytes, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	src, err := source.NewDir(srcDir, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	c, err := cache.NewDisk(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	tr := &countingTransformer{inner: transform.NewNative(100_000_000)}

	srv, err := New(Options{
		Signer:      sg,
		Source:      src,
		Cache:       c,
		Transformer: tr,
		Bounds: transform.Bounds{
			MaxWidth: 4000, MaxHeight: 4000, MaxOutputPixel: 24_000_000, DefaultQuality: 82,
		},
		InternalToken: "internal-secret",
		Now:           func() time.Time { return time.Unix(1_000_000, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return testEnv{srv: srv, signer: sg, tr: tr, pub: srv.PublicHandler(), intr: srv.InternalHandler()}
}

func (e testEnv) get(t *testing.T, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.pub.ServeHTTP(rec, req)
	return rec
}

func TestValidTokenServesResizedImage(t *testing.T) {
	e := newTestEnv(t)
	tok, _ := e.signer.Sign(token.Params{Src: "products/1.png", W: 200, H: 200, Q: 90, Crop: "cover", Fmt: "png"})
	rec := e.get(t, BuildPath(tok, "photo.png"), nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("content type = %q", ct)
	}
	img, _, err := image.Decode(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 200 || b.Dy() != 200 {
		t.Fatalf("served %dx%d, want 200x200 (cover)", b.Dx(), b.Dy())
	}
	if rec.Header().Get("ETag") == "" || rec.Header().Get("Cache-Control") == "" {
		t.Fatal("missing caching headers")
	}
}

func TestInvalidTokenIsForbiddenWithoutProcessing(t *testing.T) {
	e := newTestEnv(t)
	rec := e.get(t, "/img/garbage.token/photo.png", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if e.tr.calls != 0 {
		t.Fatalf("transformer ran %d times on invalid token; must be 0", e.tr.calls)
	}
}

func TestTamperedTokenIsForbidden(t *testing.T) {
	e := newTestEnv(t)
	tok, _ := e.signer.Sign(token.Params{Src: "products/1.png", W: 100})
	// Flip a char in the payload segment.
	payload, sig, _ := strings.Cut(tok, ".")
	tampered := payload[:len(payload)-1] + altChar(payload[len(payload)-1]) + "." + sig
	rec := e.get(t, BuildPath(tampered, ""), nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if e.tr.calls != 0 {
		t.Fatal("no image work should happen for a tampered token")
	}
}

func TestExpiredTokenIsForbidden(t *testing.T) {
	e := newTestEnv(t)
	// Clock is fixed at 1_000_000; set exp in the past.
	tok, _ := e.signer.Sign(token.Params{Src: "products/1.png", W: 100, Exp: 999_999})
	rec := e.get(t, BuildPath(tok, ""), nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if e.tr.calls != 0 {
		t.Fatal("expired token must not be processed")
	}
}

func TestServerSideClampEnforced(t *testing.T) {
	e := newTestEnv(t)
	// Ask for an absurd width in a *validly signed* token.
	tok, _ := e.signer.Sign(token.Params{Src: "products/1.png", W: 999999, H: 10, Crop: "fill", Fmt: "png"})
	rec := e.get(t, BuildPath(tok, ""), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	img, _, err := image.Decode(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() > 4000 {
		t.Fatalf("clamp not enforced: width %d exceeds 4000", img.Bounds().Dx())
	}
}

func TestPathTraversalForbiddenEvenWithValidSignature(t *testing.T) {
	e := newTestEnv(t)
	tok, _ := e.signer.Sign(token.Params{Src: "../../../../etc/passwd", W: 100, Fmt: "png"})
	rec := e.get(t, BuildPath(tok, ""), nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for traversal", rec.Code)
	}
}

func TestMissingSourceReturns404(t *testing.T) {
	e := newTestEnv(t)
	tok, _ := e.signer.Sign(token.Params{Src: "products/missing.png", W: 100, Fmt: "png"})
	rec := e.get(t, BuildPath(tok, ""), nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestRepeatRequestServedFromCache(t *testing.T) {
	e := newTestEnv(t)
	tok, _ := e.signer.Sign(token.Params{Src: "products/1.png", W: 150, H: 150, Crop: "cover", Fmt: "png"})
	p := BuildPath(tok, "")

	if rec := e.get(t, p, nil); rec.Code != http.StatusOK {
		t.Fatalf("first request status %d", rec.Code)
	}
	if e.tr.calls != 1 {
		t.Fatalf("first request should render once, got %d", e.tr.calls)
	}
	if rec := e.get(t, p, nil); rec.Code != http.StatusOK {
		t.Fatalf("second request status %d", rec.Code)
	}
	if e.tr.calls != 1 {
		t.Fatalf("second request should hit cache, transformer called %d times", e.tr.calls)
	}
}

func TestConditionalNotModified(t *testing.T) {
	e := newTestEnv(t)
	tok, _ := e.signer.Sign(token.Params{Src: "products/1.png", W: 120, Fmt: "png"})
	p := BuildPath(tok, "")
	rec := e.get(t, p, nil)
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no etag")
	}
	rec2 := e.get(t, p, map[string]string{"If-None-Match": etag})
	if rec2.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", rec2.Code)
	}
	if rec2.Body.Len() != 0 {
		t.Fatal("304 must not have a body")
	}
}

func TestNoRawParamsAccepted(t *testing.T) {
	e := newTestEnv(t)
	// There is simply no route that accepts width/height/etc. Hitting a
	// query-param style URL must not produce an image.
	rec := e.get(t, "/img/resize?w=100&h=100&src=products/1.png", nil)
	if rec.Code == http.StatusOK {
		t.Fatal("raw-parameter style request must never succeed")
	}
	if e.tr.calls != 0 {
		t.Fatal("no rendering for raw-param request")
	}
}

func TestHeadRequestHasNoBody(t *testing.T) {
	e := newTestEnv(t)
	tok, _ := e.signer.Sign(token.Params{Src: "products/1.png", W: 100, Fmt: "png"})
	req := httptest.NewRequest(http.MethodHead, BuildPath(tok, ""), nil)
	rec := httptest.NewRecorder()
	e.pub.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("HEAD status = %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("HEAD response must have no body, got %d bytes", rec.Body.Len())
	}
	if rec.Header().Get("Content-Type") != "image/png" {
		t.Fatal("HEAD should still set content type")
	}
}

// --- internal signing endpoint ---

func TestSignEndpointRoundTrip(t *testing.T) {
	e := newTestEnv(t)
	body := `{"src":"products/1.png","w":100,"h":100,"crop":"cover","fmt":"png","filename":"p.png"}`
	req := httptest.NewRequest(http.MethodPost, "/internal/sign", strings.NewReader(body))
	req.Header.Set("X-Internal-Auth", "internal-secret")
	rec := httptest.NewRecorder()
	e.intr.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("sign status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp signResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(resp.URL, "/img/") {
		t.Fatalf("unexpected url %q", resp.URL)
	}
	// The minted token must actually serve an image through the public handler.
	pubReq := httptest.NewRequest(http.MethodGet, resp.URL, nil)
	pubRec := httptest.NewRecorder()
	e.pub.ServeHTTP(pubRec, pubReq)
	if pubRec.Code != http.StatusOK {
		t.Fatalf("minted token did not serve: status %d, body=%s", pubRec.Code, pubRec.Body.String())
	}
}

func TestSignEndpointRequiresAuth(t *testing.T) {
	e := newTestEnv(t)
	req := httptest.NewRequest(http.MethodPost, "/internal/sign", strings.NewReader(`{"src":"a.png"}`))
	// No X-Internal-Auth header.
	rec := httptest.NewRecorder()
	e.intr.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestSignEndpointRejectsEmptySrc(t *testing.T) {
	e := newTestEnv(t)
	req := httptest.NewRequest(http.MethodPost, "/internal/sign", strings.NewReader(`{"w":100}`))
	req.Header.Set("X-Internal-Auth", "internal-secret")
	rec := httptest.NewRecorder()
	e.intr.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHealth(t *testing.T) {
	e := newTestEnv(t)
	rec := e.get(t, "/healthz", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "ok") {
		t.Fatalf("unexpected health body %q", rec.Body.String())
	}
}

func altChar(b byte) string {
	if b == 'A' {
		return "B"
	}
	return "A"
}
