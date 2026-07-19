# crop-resize-optimize-images-with-tokens

A standalone HTTP image service (Go) that resizes / crops / re-encodes images on
request **without ever exposing width, height, quality or crop in the public
URL**. Every transform parameter travels inside a signed, opaque token. The
public endpoint only serves requests carrying a valid HMAC signature; an
internal endpoint (or an imported Go call) mints those tokens.

```
Client browser
     │  GET /img/{token}/{filename}
     ▼
Public handler ── verify HMAC ──►(invalid → 403, no image work)
     │  decode params → clamp to server max bounds → resolve format
     │  cache hit  → serve bytes
     │  cache miss → load source (path-safe) → render → cache → serve
     ▼
image bytes + Cache-Control + ETag   (Varnish/HAProxy do the repeat-request heavy lifting)
```

## Why tokens

Chosen design (Option 2, "signed self-contained token"): parameters are signed
into the URL so we get per-image custom dimensions **and** no extra stateful
dependency (no preset table, no KV lookup). A leaked or brute-forced URL still
can't request an arbitrary transform, and the public surface never accepts raw
`?w=&h=` parameters.

## Layout

| Package | Responsibility |
|---|---|
| `internal/token` | Token encode/decode, HMAC sign/verify (constant-time), expiry, secret rotation |
| `internal/source` | Resolve `src` → bytes, confined to a base dir (traversal + symlink-escape safe) |
| `internal/transform` | Transform params, server-side clamping, `Transformer` interface + backends |
| `internal/cache` | Disk cache keyed by a hash of source id + source version + effective transform |
| `internal/server` | Public image handler + internal signing handler |
| `internal/config` | Env / secret-file configuration |
| `cmd/imgd` | Service entrypoint (two listeners, graceful shutdown) |

## Image backends

Image processing sits behind a `transform.Transformer` interface with two
compile-time-selected implementations:

- **native** (default build) — pure Go, standard library only, **jpeg / png /
  gif**. Bilinear resampling. Requires no system libraries, so tests and CI run
  anywhere. This is what you get from a plain `go build`.
- **vips** (`-tags vips`) — [`bimg`](https://github.com/h2non/bimg) / libvips,
  adds **webp / avif** and libvips performance/memory characteristics. This is
  the intended **production** backend. Requires cgo + a system libvips.

Both share the exact same crop/resize geometry, so switching backends does not
change output dimensions.

```bash
go build ./cmd/imgd                          # native (dev/CI)
CGO_ENABLED=1 go build -tags vips ./cmd/imgd # production (needs libvips-dev)
```

The provided `Dockerfile` builds the `vips` production image.

## Running

```bash
cp .env.example .env      # edit IMG_SECRET, IMG_SOURCE_DIR, IMG_CACHE_DIR
make run                  # native binary against ./.env
# or
docker build -t imgd . && docker run --env-file .env -p 8080:8080 \
  -v /srv/images:/srv/images -v /var/cache/imgd:/var/cache/imgd imgd
```

Configuration is entirely environment-driven — see [`.env.example`](.env.example)
for every variable. The three required ones are `IMG_SECRET` (or
`IMG_SECRET_FILE`), `IMG_SOURCE_DIR`, and `IMG_CACHE_DIR`.

## Endpoints

### `GET /img/{token}/{filename}`

Public. Verifies the token, then serves the transformed image. The `{filename}`
segment is an optional extension hint for the browser and does **not** influence
the render — the token governs everything. Invalid / tampered / expired tokens
return `403` before any image work happens. Returns `ETag` + `Cache-Control` and
honours `If-None-Match` (`304`).

### `POST /internal/sign`

Internal. Disabled unless `IMG_INTERNAL_ADDR` is set; bind it to a private
interface and/or set `IMG_INTERNAL_TOKEN` (checked via the `X-Internal-Auth`
header). Accepts the real parameters and returns the token + public URL:

```bash
curl -X POST http://127.0.0.1:8081/internal/sign \
  -H "X-Internal-Auth: $IMG_INTERNAL_TOKEN" \
  -d '{"src":"products/123.jpg","w":800,"h":600,"crop":"cover","fmt":"webp","filename":"p.webp"}'
# {"token":"…","url":"/img/…/p.webp"}
```

If your URL-generating backend is the same deployment, you can skip the HTTP hop
and sign in-process instead:

```go
sg, _ := token.NewSigner(token.FullSigBytes, os.Getenv("IMG_SECRET"))
tok, _ := sg.Sign(token.Params{Src: "products/123.jpg", W: 800, H: 600, Crop: "cover", Fmt: "webp"})
url := server.BuildPath(tok, "p.webp") // "/img/<token>/p.webp"
```

## Token format

```
payload = {src, w, h, q, crop, fmt, exp}   // JSON, canonical struct-field order
token   = base64url(payload) + "." + base64url(HMAC-SHA256(secret, payload)[:N])
```

`N` is `IMG_SIGNATURE_BYTES` (default **32**, the full signature). Set `16` for a
128-bit truncated signature and shorter URLs — still computationally infeasible
to forge; see the decisions section.

Crop modes: `cover` (scale + centre-crop to exactly WxH), `contain`/`""` (fit
within WxH, aspect preserved), `fill` (stretch to exactly WxH). `w`/`h` of `0`
means "auto/original"; giving only one scales proportionally.

## Security properties

- **No signature, no processing.** Signature + expiry are checked (constant-time,
  against all configured secrets) before the payload is even unmarshalled.
- **Server-side clamping.** `IMG_MAX_WIDTH`/`HEIGHT`, an output-pixel budget, and
  a source-pixel guard are enforced on every request regardless of token
  contents — a compromised signer can't trigger a decompression-bomb render.
- **Path safety.** `src` is confined to `IMG_SOURCE_DIR`; `../` traversal,
  absolute paths, and symlinks escaping the base are rejected even with a valid
  signature. An optional `IMG_SOURCE_ALLOW` regexp tightens it further.
- **Secret management.** Secrets come from env or a mounted `*_FILE`, never
  source. Rotate with `IMG_SECRET_PREVIOUS` (verify against both, sign with the
  newest) for zero downtime.
- **Rate limiting** is expected at the HAProxy layer in front (this is a
  compute-heavy endpoint); it is intentionally out of scope for the service.

## Decisions taken (were open in the handover)

1. **Full 32-byte signature by default** (`IMG_SIGNATURE_BYTES=32`), the
   conservative choice; 16-byte truncation is available via config.
2. **`/internal/sign` is optional.** Off unless `IMG_INTERNAL_ADDR` is set;
   in-process signing via the `token` package + `server.BuildPath` is the
   simpler path when URL generation shares the deployment.
3. **No cache eviction in v1** (disk is cheap, volumes low). Entries are plain
   files; a time-based sweeper can be added later without an API change.
4. **Local disk source + cache in v1.** `source.Source` and `cache.Cache` are
   interfaces, so a MinIO-backed implementation drops in later unchanged.

## Development

```bash
make test        # go test ./...
make test-race   # with the race detector
make vet
make fmt-check
```

Licensed under AGPL-3.0 (see `LICENSE`).
