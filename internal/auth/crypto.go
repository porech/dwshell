package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// encryptedToken is the wire shape of an encrypted login/reset payload, matching
// the browser client's dwsInitEncrypt output.
type encryptedToken struct {
	Encrypt   bool   `json:"encrypt"`
	Value     string `json:"value"`     // base64(ciphertext||tag)
	PublicKey string `json:"publicKey"` // base64(client SPKI)
	IV        []int  `json:"iv"`        // 16 bytes as ints
}

// encryptToken encrypts an arbitrary payload for authentication.dw, using an
// ephemeral ECDH P-256 key against the server's SPKI public key and AES-GCM-256
// with a random 16-byte IV. It returns the JSON token string and the SCF value.
//
// serverSPKIBase64 is dwsConfig.cryptAlgorithmAccept[0].importKey.keyData.
func encryptToken(payload any, serverSPKIBase64 string) (tokenJSON string, scfValue string, err error) {
	plaintext, err := json.Marshal(payload)
	if err != nil {
		return "", "", fmt.Errorf("marshal payload: %w", err)
	}

	spki, err := base64.StdEncoding.DecodeString(serverSPKIBase64)
	if err != nil {
		return "", "", fmt.Errorf("decode server key: %w", err)
	}
	pub, err := x509.ParsePKIXPublicKey(spki)
	if err != nil {
		return "", "", fmt.Errorf("parse server key: %w", err)
	}
	serverECDH, err := toECDHPublic(pub)
	if err != nil {
		return "", "", err
	}

	clientPriv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("gen ephemeral key: %w", err)
	}

	shared, err := clientPriv.ECDH(serverECDH)
	if err != nil {
		return "", "", fmt.Errorf("ecdh: %w", err)
	}
	// WebCrypto ECDH deriveKey(AES-GCM,256) uses the shared secret X coordinate
	// (32 bytes for P-256) directly as the AES-256 key. No KDF.
	block, err := aes.NewCipher(shared)
	if err != nil {
		return "", "", fmt.Errorf("aes: %w", err)
	}
	// WebCrypto AES-GCM used a 16-byte IV (non-standard nonce size).
	gcm, err := cipher.NewGCMWithNonceSize(block, 16)
	if err != nil {
		return "", "", fmt.Errorf("gcm: %w", err)
	}
	iv := make([]byte, 16)
	if _, err := rand.Read(iv); err != nil {
		return "", "", fmt.Errorf("iv: %w", err)
	}
	ct := gcm.Seal(nil, iv, plaintext, nil) // ciphertext||tag

	// Export client public key as SPKI (matches WebCrypto exportKey("spki")).
	clientSPKI, err := x509.MarshalPKIXPublicKey(clientPriv.PublicKey())
	if err != nil {
		return "", "", fmt.Errorf("marshal client key: %w", err)
	}

	ivInts := make([]int, len(iv))
	for i, b := range iv {
		ivInts[i] = int(b)
	}
	tok := encryptedToken{
		Encrypt:   true,
		Value:     base64.StdEncoding.EncodeToString(ct),
		PublicKey: base64.StdEncoding.EncodeToString(clientSPKI),
		IV:        ivInts,
	}
	tb, err := json.Marshal(tok)
	if err != nil {
		return "", "", fmt.Errorf("marshal token: %w", err)
	}
	return string(tb), scf(string(tb)), nil
}

// toECDHPublic converts a parsed PKIX public key to an ecdh.PublicKey.
func toECDHPublic(pub any) (*ecdh.PublicKey, error) {
	type ecdhable interface {
		ECDH() (*ecdh.PublicKey, error)
	}
	if e, ok := pub.(ecdhable); ok {
		return e.ECDH()
	}
	if e, ok := pub.(*ecdh.PublicKey); ok {
		return e, nil
	}
	return nil, fmt.Errorf("server key is not an ECDH public key (%T)", pub)
}
