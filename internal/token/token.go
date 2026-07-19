// Package token implements the signed, self-contained transform tokens that
// gate the public image endpoint.
//
// A token is:
//
//	payload_b64 = base64url( JSON(Params) )   // canonical: struct field order
//	sig         = HMAC-SHA256(secret, payload_b64)
//	sig_b64     = base64url( sig[:sigBytes] )
//	token       = payload_b64 + "." + sig_b64
//
// Verification recomputes the HMAC over the payload segment and compares in
// constant time *before* the payload JSON is ever unmarshalled, so malformed or
// forged input is rejected without doing any image work.
package token

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Params is the transform description carried inside a token. Field order is
// significant: encoding/json marshals struct fields in declaration order, which
// gives us a canonical, deterministic payload without sorting a map.
type Params struct {
	Src  string `json:"src"`            // source image identifier, resolved within a fixed base dir
	W    int    `json:"w,omitempty"`    // target width, 0 = auto/original
	H    int    `json:"h,omitempty"`    // target height, 0 = auto/original
	Q    int    `json:"q,omitempty"`    // quality 1-100, 0 = server default
	Crop string `json:"crop,omitempty"` // "cover" | "contain" | "fill" | ""
	Fmt  string `json:"fmt,omitempty"`  // "webp" | "avif" | "jpeg" | "png" | "gif" | "" (auto)
	Exp  int64  `json:"exp,omitempty"`  // unix expiry, 0 = no expiry
}

// FullSigBytes is the length of an untruncated HMAC-SHA256 signature.
const FullSigBytes = sha256.Size // 32

// Errors returned by Verify. They are intentionally distinct so callers can log
// the reason, but every one of them maps to a 403 for the client — the public
// endpoint never reveals which check failed.
var (
	ErrMalformed    = errors.New("token: malformed")
	ErrBadSignature = errors.New("token: signature mismatch")
	ErrExpired      = errors.New("token: expired")
)

var b64 = base64.RawURLEncoding // URL-safe, no padding

// Signer signs and verifies tokens. It holds one or more secrets: the first is
// the primary used for signing, and every secret is accepted during
// verification. This is what makes zero-downtime secret rotation possible —
// deploy with [new, old], flip callers to sign with new, then drop old.
//
// A Signer is safe for concurrent use.
type Signer struct {
	secrets  [][]byte
	sigBytes int
}

// NewSigner builds a Signer. primary must be non-empty; previous secrets are
// optional and used for verification only. sigBytes controls how many bytes of
// the HMAC are kept in the token (1..32); pass FullSigBytes for the
// conservative full-length signature.
func NewSigner(sigBytes int, primary string, previous ...string) (*Signer, error) {
	if strings.TrimSpace(primary) == "" {
		return nil, errors.New("token: primary secret is empty")
	}
	if sigBytes < 1 || sigBytes > FullSigBytes {
		return nil, fmt.Errorf("token: sigBytes must be in 1..%d, got %d", FullSigBytes, sigBytes)
	}
	secrets := make([][]byte, 0, 1+len(previous))
	secrets = append(secrets, []byte(primary))
	for _, p := range previous {
		if strings.TrimSpace(p) == "" {
			continue
		}
		secrets = append(secrets, []byte(p))
	}
	return &Signer{secrets: secrets, sigBytes: sigBytes}, nil
}

// Sign encodes p and signs it with the primary secret, returning the token.
func (s *Signer) Sign(p Params) (string, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("token: marshal params: %w", err)
	}
	payload := b64.EncodeToString(raw)
	sig := s.mac(s.secrets[0], payload)
	return payload + "." + b64.EncodeToString(sig), nil
}

// Verify validates a token and returns its Params. It checks the signature
// against every configured secret in constant time before unmarshalling the
// payload, then enforces expiry using now. Any failure returns one of the
// sentinel errors above and a zero Params.
func (s *Signer) Verify(tok string, now time.Time) (Params, error) {
	payload, sigB64, ok := strings.Cut(tok, ".")
	if !ok || payload == "" || sigB64 == "" {
		return Params{}, ErrMalformed
	}
	gotSig, err := b64.DecodeString(sigB64)
	if err != nil || len(gotSig) != s.sigBytes {
		return Params{}, ErrMalformed
	}

	// Constant-time check against all accepted secrets. We compute every MAC
	// even after a match so verification time does not leak which secret hit.
	valid := false
	for _, secret := range s.secrets {
		want := s.mac(secret, payload)
		if hmac.Equal(want, gotSig) {
			valid = true
		}
	}
	if !valid {
		return Params{}, ErrBadSignature
	}

	raw, err := b64.DecodeString(payload)
	if err != nil {
		return Params{}, ErrMalformed
	}
	var p Params
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return Params{}, ErrMalformed
	}

	if p.Exp != 0 && now.Unix() > p.Exp {
		return Params{}, ErrExpired
	}
	return p, nil
}

// mac computes HMAC-SHA256 over payload and truncates to the configured length.
func (s *Signer) mac(secret []byte, payload string) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(payload))
	return m.Sum(nil)[:s.sigBytes]
}
