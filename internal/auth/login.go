package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Bootstrap is the result of a successful login: everything needed to open the
// account command channel. The raw "ok" response is preserved because the exact
// field set is server-defined and may carry either a single node URL or a node
// list.
type Bootstrap struct {
	Raw     map[string]any // full authentication.dw "ok" response
	SignKey *SignKey       // account-session signing key (private kept here)

	// TrustedDeviceID/Name are set when a trusted device was registered.
	TrustedDeviceID   string
	TrustedDeviceName string
}

// authError carries a server "error" status message (e.g. #PASSWORDRESET).
type authError struct{ msg string }

func (e *authError) Error() string { return e.msg }

// Message returns the raw server message code (e.g. "#PASSWORDRESET").
func (e *authError) Message() string { return e.msg }

// postAuth sends one authentication.dw step and returns the decoded JSON.
func postAuth(ctx context.Context, client *http.Client, cfg *LoginConfig, typ, step, tokenJSON, scfVal string, extra url.Values) (map[string]any, error) {
	form := url.Values{}
	form.Set("type", typ)
	form.Set("step", step)
	form.Set("token", tokenJSON)
	form.Set(scfFieldName, scfVal)
	for k, vs := range extra {
		for _, v := range vs {
			form.Add(k, v)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.AuthURL(), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode auth response: %w (body: %.200s)", err, body)
	}
	if s, _ := out["status"].(string); s == "error" {
		msg, _ := out["message"].(string)
		return out, &authError{msg: msg}
	}
	return out, nil
}

// LoginPassword performs the user+password login steps. On success it returns a
// Bootstrap. If trusted is non-nil, a trusted device is registered and its
// credentials are returned in the Bootstrap and via trusted (for persistence).
func LoginPassword(ctx context.Context, client *http.Client, cfg *LoginConfig, user, password string, trusted *TrustedDeviceRequest) (*Bootstrap, error) {
	// Step user.
	tok, scfVal, err := encryptToken(map[string]any{
		"type": "login", "step": "user", "username": user,
	}, cfg.ServerKeyB64)
	if err != nil {
		return nil, err
	}
	uresp, err := postAuth(ctx, client, cfg, "login", "user", tok, scfVal, nil)
	if err != nil {
		return nil, err
	}
	tempKey, _ := uresp["tempKey"].(string)
	status, _ := uresp["status"].(string)
	if status != "password" {
		return nil, fmt.Errorf("unexpected status after user step: %q", status)
	}
	if tempKey == "" {
		return nil, fmt.Errorf("no tempKey returned")
	}

	// Step password.
	signKey, err := NewSignKey()
	if err != nil {
		return nil, err
	}
	sessionKey, err := signKey.sessionKeyForLogin()
	if err != nil {
		return nil, err
	}
	payload := map[string]any{
		"type": "login", "step": "password",
		"username":   user,
		"password":   tempKey + ":" + password,
		"sessionKey": sessionKey,
	}
	var trustedKey *SignKey
	if trusted != nil {
		tk, td, err := trusted.build()
		if err != nil {
			return nil, err
		}
		trustedKey = tk
		payload["trustedDevice"] = td
	}
	tok, scfVal, err = encryptToken(payload, cfg.ServerKeyB64)
	if err != nil {
		return nil, err
	}
	presp, err := postAuth(ctx, client, cfg, "login", "password", tok, scfVal, nil)
	if err != nil {
		return nil, err
	}
	if s, _ := presp["status"].(string); s != "ok" {
		return nil, fmt.Errorf("login did not return ok (status=%q): %v", s, presp)
	}

	b := &Bootstrap{Raw: presp, SignKey: signKey}
	if id, ok := presp["trustedDeviceID"].(string); ok {
		b.TrustedDeviceID = id
		b.TrustedDeviceName, _ = presp["trustedDeviceUserName"].(string)
		if trusted != nil && trustedKey != nil {
			trusted.Result = &TrustedDevice{
				ID:      id,
				Name:    trusted.Name,
				AuthKey: trustedKey,
			}
		}
	}
	return b, nil
}
