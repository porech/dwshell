package auth

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
)

// TrustedDevice is a persisted passwordless credential: a device id plus the
// private signing key registered with the account. It is the "permanent token".
type TrustedDevice struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	AuthKey *SignKey `json:"authKey"` // serialized via SignKey's JSON methods
}

// TrustedDeviceRequest asks LoginPassword to register a trusted device. After a
// successful login, Result holds the credential to persist.
type TrustedDeviceRequest struct {
	Name string
	Type string
	OS   string

	Result *TrustedDevice
}

// NewTrustedDeviceRequest builds a request with sensible local defaults.
func NewTrustedDeviceRequest(name string) *TrustedDeviceRequest {
	if name == "" {
		name = "dwshell"
	}
	return &TrustedDeviceRequest{Name: name, Type: "Desktop", OS: runtime.GOOS}
}

// build generates the authKey and the trustedDevice payload object.
func (r *TrustedDeviceRequest) build() (*SignKey, map[string]any, error) {
	k, err := NewSignKey()
	if err != nil {
		return nil, nil, err
	}
	iv, err := k.initValue()
	if err != nil {
		return nil, nil, err
	}
	td := map[string]any{
		"name": r.Name,
		"type": r.Type,
		"os":   r.OS,
		"authKey": map[string]any{
			"name":      k.Name,
			"verify":    map[string]any{"key": k.jwkPublic()},
			"initValue": iv,
		},
	}
	return k, td, nil
}

// RemoveTrustedDevice deregisters a trusted device on the account, freeing its
// slot (device registrations are capped). It mirrors the client's removeDevice
// flow: a signed device request with removeDevice=true.
func RemoveTrustedDevice(ctx context.Context, client *http.Client, cfg *LoginConfig, td *TrustedDevice) error {
	if td == nil || td.AuthKey == nil {
		return fmt.Errorf("no trusted device credential")
	}
	auth, err := td.AuthKey.NextSessionKey()
	if err != nil {
		return err
	}
	tok, scfVal, err := encryptToken(map[string]any{
		"type": "device", "step": "user",
		"id": td.ID, "auth": auth, "removeDevice": true,
	}, cfg.ServerKeyB64)
	if err != nil {
		return err
	}
	_, err = postAuth(ctx, client, cfg, "device", "user", tok, scfVal, nil)
	return err
}

// LoginTrustedDevice performs passwordless login using a stored trusted device.
func LoginTrustedDevice(ctx context.Context, client *http.Client, cfg *LoginConfig, td *TrustedDevice) (*Bootstrap, error) {
	if td == nil || td.AuthKey == nil {
		return nil, fmt.Errorf("no trusted device credential")
	}
	// auth = signed current time using the device authKey.
	auth, err := td.AuthKey.NextSessionKey()
	if err != nil {
		return nil, err
	}
	// The resulting session needs its own signing key, sent as sessionKey.
	signKey, err := NewSignKey()
	if err != nil {
		return nil, err
	}
	sessionKey, err := signKey.sessionKeyForLogin()
	if err != nil {
		return nil, err
	}
	tok, scfVal, err := encryptToken(map[string]any{
		"type": "device", "step": "user",
		"id": td.ID, "auth": auth,
		"sessionKey": sessionKey,
	}, cfg.ServerKeyB64)
	if err != nil {
		return nil, err
	}
	resp, err := postAuth(ctx, client, cfg, "device", "user", tok, scfVal, nil)
	if err != nil {
		return nil, err
	}
	if s, _ := resp["status"].(string); s != "ok" {
		return nil, fmt.Errorf("device login did not return ok (status=%q): %v", s, resp)
	}
	return &Bootstrap{Raw: resp, SignKey: signKey}, nil
}
