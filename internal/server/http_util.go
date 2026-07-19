package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"log/slog"
	"net"
	"net/http"
	"strings"
)

// recoverer turns a panic in a handler into a 500 instead of crashing the
// process, and logs it.
func recoverer(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Error("panic in handler", "panic", rec, "path", r.URL.Path)
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// clientIP extracts a best-effort client IP for logging. Trust of proxy headers
// is a deployment concern (HAProxy sits in front); we log the direct peer and
// the forwarded-for hint without making auth decisions on either.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// etagMatch reports whether the If-None-Match header value matches etag. It
// handles a comma-separated list and the "*" wildcard, comparing strong
// validators.
func etagMatch(ifNoneMatch, etag string) bool {
	ifNoneMatch = strings.TrimSpace(ifNoneMatch)
	if ifNoneMatch == "*" {
		return true
	}
	for _, part := range strings.Split(ifNoneMatch, ",") {
		part = strings.TrimSpace(part)
		part = strings.TrimPrefix(part, "W/") // weak validator prefix
		if part == etag {
			return true
		}
	}
	return false
}

// acceptsType reports whether an Accept header value accepts the given MIME
// type, honouring "type/*" and "*/*" wildcards. An empty Accept means the
// client expressed no preference, so it is treated as not explicitly accepting
// (the caller falls back to a default format).
func acceptsType(accept, mime string) bool {
	if accept == "" {
		return false
	}
	group := mime
	if i := strings.IndexByte(mime, '/'); i >= 0 {
		group = mime[:i] + "/*"
	}
	for _, part := range strings.Split(accept, ",") {
		token := strings.TrimSpace(part)
		if i := strings.IndexByte(token, ';'); i >= 0 { // drop q= and other params
			token = strings.TrimSpace(token[:i])
		}
		if token == mime || token == group || token == "*/*" {
			return true
		}
	}
	return false
}

// constantTimeHeader compares a header value to an expected secret without
// leaking length or content via timing. Both sides are hashed to a fixed size
// first so ConstantTimeCompare never short-circuits on a length mismatch (which
// would otherwise leak the secret's length).
func constantTimeHeader(got, want string) bool {
	g := sha256.Sum256([]byte(got))
	w := sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(g[:], w[:]) == 1
}
