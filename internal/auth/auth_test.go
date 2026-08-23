package auth

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
)

func TestSCF(t *testing.T) {
	// Synthetic vector: token "0123456789" (len 10); value is token[idx%10] for
	// each fixed SCF index.
	got := scf("0123456789")
	want := "35111225678839694611"
	if got != want {
		t.Fatalf("scf = %q, want %q", got, want)
	}
	if len(scf("anytokenlongerthan86characters________________________________________________________________")) != len(scfIndexes) {
		t.Fatal("scf length must equal number of indexes")
	}
	if scf("") != "" {
		t.Fatal("scf of empty string must be empty")
	}
}

func TestEncryptTokenRoundTrip(t *testing.T) {
	// Act as the server: generate a P-256 key, hand its SPKI to encryptToken,
	// then decrypt what the client produced and confirm it matches.
	serverPriv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serverSPKI, err := x509.MarshalPKIXPublicKey(serverPriv.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	serverSPKIB64 := base64.StdEncoding.EncodeToString(serverSPKI)

	payload := map[string]any{"type": "login", "step": "user", "username": "someone@example.com"}
	tokenJSON, scfVal, err := encryptToken(payload, serverSPKIB64)
	if err != nil {
		t.Fatal(err)
	}
	if scfVal != scf(tokenJSON) {
		t.Fatal("returned scf does not match token")
	}

	var tok encryptedToken
	if err := json.Unmarshal([]byte(tokenJSON), &tok); err != nil {
		t.Fatal(err)
	}
	if !tok.Encrypt || len(tok.IV) != 16 {
		t.Fatalf("bad token shape: %+v", tok)
	}

	// Server side: import client SPKI, ECDH, AES-GCM open.
	cpkiBytes, err := base64.StdEncoding.DecodeString(tok.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	cpub, err := x509.ParsePKIXPublicKey(cpkiBytes)
	if err != nil {
		t.Fatal(err)
	}
	clientECDH, err := toECDHPublic(cpub)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := serverPriv.ECDH(clientECDH)
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(shared)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCMWithNonceSize(block, 16)
	if err != nil {
		t.Fatal(err)
	}
	iv := make([]byte, 16)
	for i, v := range tok.IV {
		iv[i] = byte(v)
	}
	ct, err := base64.StdEncoding.DecodeString(tok.Value)
	if err != nil {
		t.Fatal(err)
	}
	pt, err := gcm.Open(nil, iv, ct, nil)
	if err != nil {
		t.Fatalf("decrypt failed: %v", err)
	}
	want, _ := json.Marshal(payload)
	if !bytes.Equal(pt, want) {
		t.Fatalf("decrypted %q, want %q", pt, want)
	}
}

func TestSignKeyValueVerifies(t *testing.T) {
	k, err := NewSignKey()
	if err != nil {
		t.Fatal(err)
	}
	if k.Name != "SIGN_ECDSA_512" || k.size != 66 {
		t.Fatalf("unexpected key: name=%s size=%d", k.Name, k.size)
	}
	val, err := k.NextSessionKey()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(val)
	if err != nil {
		t.Fatal(err)
	}
	i := bytes.IndexByte(raw, ':')
	if i < 0 {
		t.Fatal("no ':' separator in signed value")
	}
	counterBytes, sig := raw[:i], raw[i+1:]
	if len(sig) != 2*k.size {
		t.Fatalf("sig length = %d, want %d (P1363 r||s)", len(sig), 2*k.size)
	}
	// Verify the ECDSA signature over SHA-512(counterBytes) with the public key.
	digest := sha512.Sum512(counterBytes)
	r := new(big.Int).SetBytes(sig[:k.size])
	s := new(big.Int).SetBytes(sig[k.size:])
	pub := &ecdsa.PublicKey{Curve: elliptic.P521(), X: k.priv.X, Y: k.priv.Y}
	if !ecdsa.Verify(pub, digest[:], r, s) {
		t.Fatal("signature does not verify")
	}
}

func TestSessionKeyMonotonic(t *testing.T) {
	k, err := NewSignKey()
	if err != nil {
		t.Fatal(err)
	}
	decodeCounter := func(v string) int64 {
		raw, _ := base64.StdEncoding.DecodeString(v)
		c := raw[:bytes.IndexByte(raw, ':')]
		var n int64
		for _, d := range c {
			n = n*10 + int64(d-'0')
		}
		return n
	}
	var prev int64
	for i := 0; i < 5; i++ {
		v, err := k.NextSessionKey()
		if err != nil {
			t.Fatal(err)
		}
		c := decodeCounter(v)
		if c <= prev {
			t.Fatalf("counter not strictly increasing: %d after %d", c, prev)
		}
		prev = c
	}
}

func TestJWKShapes(t *testing.T) {
	k, err := NewSignKey()
	if err != nil {
		t.Fatal(err)
	}
	pub := k.jwkPublic()
	if pub["crv"] != "P-521" || pub["kty"] != "EC" {
		t.Fatalf("bad public jwk: %+v", pub)
	}
	for _, f := range []string{"x", "y"} {
		b, err := base64.RawURLEncoding.DecodeString(pub[f].(string))
		if err != nil || len(b) != 66 {
			t.Fatalf("jwk %s: len=%d err=%v", f, len(b), err)
		}
	}
	priv := k.jwkPrivate()
	if _, ok := priv["d"]; !ok {
		t.Fatal("private jwk missing d")
	}
	// The connection sessionKey must be valid JSON carrying the private key.
	conn, err := k.sessionKeyForConnection()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(conn, `"generate":true`) || !strings.Contains(conn, `"d":`) {
		t.Fatalf("connection sessionKey missing fields: %s", conn)
	}
	login, err := k.sessionKeyForLogin()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := login["sign"]; ok {
		t.Fatal("login sessionKey must not include private sign key")
	}
}
