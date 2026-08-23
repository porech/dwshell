package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash"
	"math/big"
	"sync"
	"time"
)

// SignKey is a per-session ECDSA signing key. DWService generates one at login
// and one per agent/share connection, then authenticates each subsequent
// request by signing a monotonic counter with it (see PROTOCOL.md §1.3).
//
// The default algorithm is SIGN_ECDSA_512 (curve P-521, hash SHA-512), which is
// what the browser client selects in practice.
type SignKey struct {
	Name  string // e.g. "SIGN_ECDSA_512"
	priv  *ecdsa.PrivateKey
	curve elliptic.Curve
	hash  func() hash.Hash
	size  int // coordinate byte length for P1363 encoding

	initN   int64  // random negative seed from initValue
	initVal string // cached signed seed; sent at registration and reused as _sk

	mu      sync.Mutex
	counter int64 // monotonic session-key counter (epoch millis, strictly increasing)
	reconn  int64 // decreasing reconnect counter, starts at initN
}

// NewSignKey generates a fresh SIGN_ECDSA_512 signing key.
func NewSignKey() (*SignKey, error) { return newSignKeyCurve("SIGN_ECDSA_512") }

func newSignKeyCurve(name string) (*SignKey, error) {
	var curve elliptic.Curve
	var h func() hash.Hash
	switch name {
	case "SIGN_ECDSA_512":
		curve, h = elliptic.P521(), sha512.New
	case "SIGN_ECDSA_384":
		curve, h = elliptic.P384(), sha512.New384
	case "SIGN_ECDSA_256":
		curve, h = elliptic.P256(), sha256.New
	default:
		return nil, fmt.Errorf("unsupported sign algorithm %q", name)
	}
	priv, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		return nil, err
	}
	sk := &SignKey{
		Name:  name,
		priv:  priv,
		curve: curve,
		hash:  h,
		size:  (curve.Params().BitSize + 7) / 8,
	}
	// Random negative seed, matching dwsInitSessionMakeValue:
	//   -Math.floor(1e7 * Math.random()) - 1  ∈ [-1e7, -1]
	nBig, err := rand.Int(rand.Reader, big.NewInt(10_000_000))
	if err != nil {
		return nil, err
	}
	sk.initN = -(nBig.Int64() + 1)
	sk.reconn = sk.initN
	return sk, nil
}

// signP1363 hashes msg and signs it, returning the signature as fixed-length
// r||s (IEEE P1363), matching WebCrypto's ECDSA output.
func (k *SignKey) signP1363(msg []byte) ([]byte, error) {
	hsh := k.hash()
	hsh.Write(msg)
	digest := hsh.Sum(nil)
	r, s, err := ecdsa.Sign(rand.Reader, k.priv, digest)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 2*k.size)
	r.FillBytes(out[:k.size])
	s.FillBytes(out[k.size:])
	return out, nil
}

// signedValue returns base64( ascii(counter) ':' rawSig(ascii(counter)) ),
// the encoding used for initValue and for per-request keys.
func (k *SignKey) signedValue(counter int64) (string, error) {
	msg := []byte(fmt.Sprintf("%d", counter))
	sig, err := k.signP1363(msg)
	if err != nil {
		return "", err
	}
	buf := make([]byte, 0, len(msg)+1+len(sig))
	buf = append(buf, msg...)
	buf = append(buf, ':')
	buf = append(buf, sig...)
	return base64.StdEncoding.EncodeToString(buf), nil
}

// initValue returns the signed seed sent to the server at key registration.
// It is computed once and cached so the value registered with the server is the
// exact same string later reused as the _sk of the first (activation) request.
func (k *SignKey) initValue() (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.initVal != "" {
		return k.initVal, nil
	}
	v, err := k.signedValue(k.initN)
	if err != nil {
		return "", err
	}
	k.initVal = v
	return v, nil
}

// InitValue returns the cached signed seed (see initValue).
func (k *SignKey) InitValue() (string, error) { return k.initValue() }

// NextSessionKey returns the per-request auth string for command POSTs and the
// WebSocket handshake. The counter is epoch millis, forced strictly increasing
// (matching getNewSessionKey).
func (k *SignKey) NextSessionKey() (string, error) {
	k.mu.Lock()
	now := time.Now().UnixMilli()
	if k.counter == 0 || now > k.counter {
		k.counter = now
	} else {
		k.counter++
	}
	c := k.counter
	k.mu.Unlock()
	return k.signedValue(c)
}

// NextReconnectKey returns the per-request auth string for ?request=initialize.
// The counter starts at the seed and decrements (matching getNewReconnectKey).
func (k *SignKey) NextReconnectKey() (string, error) {
	k.mu.Lock()
	k.reconn--
	c := k.reconn
	k.mu.Unlock()
	return k.signedValue(c)
}

// --- JWK ---

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (k *SignKey) crvName() string {
	switch k.curve {
	case elliptic.P521():
		return "P-521"
	case elliptic.P384():
		return "P-384"
	default:
		return "P-256"
	}
}

// jwkPublic returns the public key JWK (WebCrypto exportKey("jwk") shape).
func (k *SignKey) jwkPublic() map[string]any {
	x := make([]byte, k.size)
	y := make([]byte, k.size)
	k.priv.X.FillBytes(x)
	k.priv.Y.FillBytes(y)
	return map[string]any{
		"crv": k.crvName(), "kty": "EC", "ext": true,
		"key_ops": []string{"verify"},
		"x":       b64url(x), "y": b64url(y),
	}
}

// jwkPrivate returns the private key JWK (includes "d").
func (k *SignKey) jwkPrivate() map[string]any {
	m := k.jwkPublic()
	m["key_ops"] = []string{"sign"}
	d := make([]byte, k.size)
	k.priv.D.FillBytes(d)
	m["d"] = b64url(d)
	return m
}

// --- persistence (JSON) ---

type signKeyJSON struct {
	Name string         `json:"name"`
	Priv map[string]any `json:"priv"` // private JWK
}

// MarshalJSON serializes the key as {name, private JWK} for config storage.
func (k *SignKey) MarshalJSON() ([]byte, error) {
	return json.Marshal(signKeyJSON{Name: k.Name, Priv: k.jwkPrivate()})
}

// UnmarshalJSON restores a key previously produced by MarshalJSON.
func (k *SignKey) UnmarshalJSON(b []byte) error {
	var s signKeyJSON
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	restored, err := signKeyFromJWK(s.Name, s.Priv)
	if err != nil {
		return err
	}
	// Copy fields individually; never copy the embedded mutex.
	k.Name = restored.Name
	k.priv = restored.priv
	k.curve = restored.curve
	k.hash = restored.hash
	k.size = restored.size
	k.initN = restored.initN
	return nil
}

func jwkBig(m map[string]any, field string) (*big.Int, error) {
	v, _ := m[field].(string)
	if v == "" {
		return nil, fmt.Errorf("jwk missing %q", field)
	}
	raw, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil {
		return nil, fmt.Errorf("jwk %q: %w", field, err)
	}
	return new(big.Int).SetBytes(raw), nil
}

func signKeyFromJWK(name string, jwk map[string]any) (*SignKey, error) {
	var curve elliptic.Curve
	var h func() hash.Hash
	switch name {
	case "SIGN_ECDSA_512":
		curve, h = elliptic.P521(), sha512.New
	case "SIGN_ECDSA_384":
		curve, h = elliptic.P384(), sha512.New384
	case "SIGN_ECDSA_256":
		curve, h = elliptic.P256(), sha256.New
	default:
		return nil, fmt.Errorf("unsupported sign algorithm %q", name)
	}
	d, err := jwkBig(jwk, "d")
	if err != nil {
		return nil, err
	}
	x, err := jwkBig(jwk, "x")
	if err != nil {
		return nil, err
	}
	y, err := jwkBig(jwk, "y")
	if err != nil {
		return nil, err
	}
	priv := &ecdsa.PrivateKey{
		PublicKey: ecdsa.PublicKey{Curve: curve, X: x, Y: y},
		D:         d,
	}
	return &SignKey{
		Name:  name,
		priv:  priv,
		curve: curve,
		hash:  h,
		size:  (curve.Params().BitSize + 7) / 8,
	}, nil
}

// sessionKeyForLogin builds the sessionKey object sent in the login password
// step: only the public verify key plus initValue (no private material).
func (k *SignKey) sessionKeyForLogin() (map[string]any, error) {
	iv, err := k.initValue()
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"name":      k.Name,
		"verify":    map[string]any{"key": k.jwkPublic()},
		"initValue": iv,
	}, nil
}

// ConnectionParam returns the JSON sessionKey parameter for an agent/share
// connection (full generated key). The same SignKey then signs that session.
func (k *SignKey) ConnectionParam() (string, error) { return k.sessionKeyForConnection() }

// sessionKeyForConnection builds the sessionKey parameter sent when connecting
// to an agent/share: the full generated key (sign+verify+initValue), matching
// the browser client which sends the private JWK over the (TLS) command channel.
func (k *SignKey) sessionKeyForConnection() (string, error) {
	iv, err := k.initValue()
	if err != nil {
		return "", err
	}
	m := map[string]any{
		"generate":  true,
		"name":      k.Name,
		"sign":      map[string]any{"key": k.jwkPrivate()},
		"verify":    map[string]any{"key": k.jwkPublic()},
		"initValue": iv,
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
