# Production image: libvips-backed (webp/avif) build via the "vips" tag.
#
# The default `go build` (no tag) produces a pure-Go binary with jpeg/png/gif
# only and is what CI/tests use; this Dockerfile builds the real production
# backend and therefore needs libvips + cgo.

# ---- build stage ----
FROM golang:1.24-bookworm AS build

# libvips dev headers for cgo/bimg. heif/avif support depends on the distro's
# libvips build; bookworm's libvips is compiled with libheif.
RUN apt-get update \
 && apt-get install -y --no-install-recommends libvips-dev \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ENV CGO_ENABLED=1
RUN go build -tags vips -ldflags "-s -w" -o /out/imgd ./cmd/imgd

# ---- runtime stage ----
FROM debian:bookworm-slim

RUN apt-get update \
 && apt-get install -y --no-install-recommends libvips42 ca-certificates \
 && rm -rf /var/lib/apt/lists/* \
 && useradd --system --uid 10001 imgd \
 && mkdir -p /srv/images /var/cache/imgd \
 && chown imgd /var/cache/imgd

COPY --from=build /out/imgd /usr/local/bin/imgd

USER imgd
ENV IMG_SOURCE_DIR=/srv/images \
    IMG_CACHE_DIR=/var/cache/imgd \
    IMG_PUBLIC_ADDR=:8080

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/imgd"]
