package token

import (
	"strings"
	"testing"
	"time"
)

func mustSigner(t *testing.T, sigBytes int, primary string, prev ...string) *Signer {
	t.Helper()
	s, err := NewSigner(sigBytes, primary, prev...)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	return s
}

func TestSignVerifyRoundTrip(t *testing.T) {
	s := mustSigner(t, FullSigBytes, "super-secret")
	in := Params{Src: "products/123.jpg", W: 800, H: 600, Q: 82, Crop: "cover", Fmt: "webp"}
	tok, err := s.Sign(in)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	out, err := s.Verify(tok, time.Unix(1000, 0))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if out != in {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", out, in)
	}
}

func TestVerifyRejectsTamperedPayload(t *testing.T) {
	s := mustSigner(t, FullSigBytes, "secret")
	tok, _ := s.Sign(Params{Src: "a.jpg", W: 100})
	payload, sig, _ := strings.Cut(tok, ".")
	// Flip a byte in the payload; signature no longer matches.
	tampered := payload[:len(payload)-1] + flip(payload[len(payload)-1:]) + "." + sig
	if _, err := s.Verify(tampered, time.Now()); err != ErrBadSignature {
		t.Fatalf("want ErrBadSignature, got %v", err)
	}
}

func TestVerifyRejectsTamperedSignature(t *testing.T) {
	s := mustSigner(t, FullSigBytes, "secret")
	tok, _ := s.Sign(Params{Src: "a.jpg"})
	if _, err := s.Verify(tok+"x", time.Now()); err == nil {
		t.Fatal("expected error for tampered signature")
	}
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	a := mustSigner(t, FullSigBytes, "secret-a")
	b := mustSigner(t, FullSigBytes, "secret-b")
	tok, _ := a.Sign(Params{Src: "a.jpg"})
	if _, err := b.Verify(tok, time.Now()); err != ErrBadSignature {
		t.Fatalf("want ErrBadSignature, got %v", err)
	}
}

func TestVerifyMalformed(t *testing.T) {
	s := mustSigner(t, FullSigBytes, "secret")
	cases := []string{"", "nodot", ".", "a.", ".b", "not#base64.sig", strings.Repeat("a", 10)}
	for _, c := range cases {
		if _, err := s.Verify(c, time.Now()); err == nil {
			t.Fatalf("expected error for %q", c)
		}
	}
}

func TestVerifyExpiry(t *testing.T) {
	s := mustSigner(t, FullSigBytes, "secret")
	tok, _ := s.Sign(Params{Src: "a.jpg", Exp: 500})
	if _, err := s.Verify(tok, time.Unix(499, 0)); err != nil {
		t.Fatalf("should be valid before exp: %v", err)
	}
	if _, err := s.Verify(tok, time.Unix(501, 0)); err != ErrExpired {
		t.Fatalf("want ErrExpired, got %v", err)
	}
	// exp==500 at exactly 500 is still valid (only strictly after expires).
	if _, err := s.Verify(tok, time.Unix(500, 0)); err != nil {
		t.Fatalf("exactly at exp should be valid: %v", err)
	}
}

func TestRotation(t *testing.T) {
	old := mustSigner(t, FullSigBytes, "old-secret")
	tok, _ := old.Sign(Params{Src: "a.jpg", W: 200})

	// New deployment signs with "new-secret" but still accepts "old-secret".
	rotated := mustSigner(t, FullSigBytes, "new-secret", "old-secret")
	if _, err := rotated.Verify(tok, time.Now()); err != nil {
		t.Fatalf("rotated signer should accept old token: %v", err)
	}
	// Tokens minted after rotation use the new secret.
	newTok, _ := rotated.Sign(Params{Src: "a.jpg", W: 200})
	if _, err := old.Verify(newTok, time.Now()); err != ErrBadSignature {
		t.Fatalf("old signer must not accept new token, got %v", err)
	}
}

func TestTruncatedSignature(t *testing.T) {
	s := mustSigner(t, 16, "secret")
	tok, _ := s.Sign(Params{Src: "a.jpg"})
	_, sig, _ := strings.Cut(tok, ".")
	// 16 bytes -> RawURLEncoding length is ceil(16*8/6)=22 chars.
	if len(sig) != 22 {
		t.Fatalf("expected 22-char truncated sig, got %d (%q)", len(sig), sig)
	}
	if _, err := s.Verify(tok, time.Now()); err != nil {
		t.Fatalf("truncated verify: %v", err)
	}
}

func TestNewSignerValidation(t *testing.T) {
	if _, err := NewSigner(FullSigBytes, ""); err == nil {
		t.Fatal("empty primary should error")
	}
	if _, err := NewSigner(0, "s"); err == nil {
		t.Fatal("sigBytes 0 should error")
	}
	if _, err := NewSigner(33, "s"); err == nil {
		t.Fatal("sigBytes 33 should error")
	}
}

func TestVerifyRejectsUnknownFields(t *testing.T) {
	s := mustSigner(t, FullSigBytes, "secret")
	// Hand-craft a payload with an extra field, signed correctly.
	payload := b64.EncodeToString([]byte(`{"src":"a.jpg","evil":true}`))
	sig := b64.EncodeToString(s.mac(s.secrets[0], payload))
	tok := payload + "." + sig
	if _, err := s.Verify(tok, time.Now()); err != ErrMalformed {
		t.Fatalf("want ErrMalformed for unknown field, got %v", err)
	}
}

func flip(s string) string {
	if s == "a" {
		return "b"
	}
	return "a"
}
