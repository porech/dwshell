package main

import (
	"encoding/base32"
	"testing"
	"time"
)

func TestTOTPRFC6238(t *testing.T) {
	// RFC 6238 SHA-1 test vector: ASCII key "12345678901234567890" at T=59s
	// yields 94287082; the 6-digit truncation is its last six digits.
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).
		EncodeToString([]byte("12345678901234567890"))

	got, err := totpCodeAt(secret, time.Unix(59, 0))
	if err != nil {
		t.Fatal(err)
	}
	if got != "287082" {
		t.Fatalf("totp = %q, want %q", got, "287082")
	}
}

func TestTOTPHandlesSpacesAndCase(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).
		EncodeToString([]byte("12345678901234567890"))
	// lower-case with spaces should still work
	spaced := secret[:4] + " " + secret[4:]
	got, err := totpCodeAt(spaced, time.Unix(59, 0))
	if err != nil || got != "287082" {
		t.Fatalf("got %q err=%v", got, err)
	}
}
