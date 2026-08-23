package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// deviceApprovalTimeout bounds how long we wait for a device-approval login.
var deviceApprovalTimeout = 2 * time.Minute

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

// TwoFactorFunc supplies a second-factor code on demand. method is the server's
// requested factor ("totp" or "email"); retry is true after a rejected code.
type TwoFactorFunc func(method string, retry bool) (string, error)

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

// encryptAndPost encrypts a payload and posts it as one login step.
func encryptAndPost(ctx context.Context, client *http.Client, cfg *LoginConfig, typ, step string, payload map[string]any) (map[string]any, error) {
	tok, scfVal, err := encryptToken(payload, cfg.ServerKeyB64)
	if err != nil {
		return nil, err
	}
	return postAuth(ctx, client, cfg, typ, step, tok, scfVal, nil)
}

// credentialStep sends a credential-bearing login step (password or a 2FA code)
// with a fresh session signing key (and a trusted-device registration when
// requested). It returns the response plus the signing key and trusted authKey
// generated for this step, so the caller can adopt them if the step succeeds.
func credentialStep(ctx context.Context, client *http.Client, cfg *LoginConfig, user, step, credential string, trusted *TrustedDeviceRequest) (resp map[string]any, signKey, trustedKey *SignKey, err error) {
	signKey, err = NewSignKey()
	if err != nil {
		return nil, nil, nil, err
	}
	sessionKey, err := signKey.sessionKeyForLogin()
	if err != nil {
		return nil, nil, nil, err
	}
	payload := map[string]any{
		"type":       "login",
		"step":       step,
		"username":   user,
		"password":   credential,
		"sessionKey": sessionKey,
	}
	if trusted != nil {
		tk, td, err := trusted.build()
		if err != nil {
			return nil, nil, nil, err
		}
		trustedKey = tk
		payload["trustedDevice"] = td
	}
	resp, err = encryptAndPost(ctx, client, cfg, "login", step, payload)
	return resp, signKey, trustedKey, err
}

// LoginPassword performs the user+password login, transparently handling a
// second factor (TOTP or email) via twoFA when the server requests one. On
// success it returns a Bootstrap; if trusted is non-nil a trusted device is
// registered on the successful step and returned via trusted for persistence.
func LoginPassword(ctx context.Context, client *http.Client, cfg *LoginConfig, user, password string, trusted *TrustedDeviceRequest, twoFA TwoFactorFunc) (*Bootstrap, error) {
	// Step user.
	uresp, err := encryptAndPost(ctx, client, cfg, "login", "user", map[string]any{
		"type": "login", "step": "user", "username": user,
	})
	if err != nil {
		return nil, err
	}
	if s, _ := uresp["status"].(string); s != "password" {
		return nil, fmt.Errorf("unexpected status after user step: %q", s)
	}
	tempKey, _ := uresp["tempKey"].(string)
	if tempKey == "" {
		return nil, fmt.Errorf("no tempKey returned")
	}

	// Step password.
	resp, signKey, trustedKey, err := credentialStep(ctx, client, cfg, user, "password", tempKey+":"+password, trusted)
	if err != nil {
		return nil, err
	}

	// Resolve status, running a 2FA exchange if required.
	for {
		status, _ := resp["status"].(string)
		switch status {
		case "ok":
			return finishLogin(resp, signKey, trusted, trustedKey), nil
		case "totp", "email":
			resp, signKey, trustedKey, err = handleTwoFactor(ctx, client, cfg, user, status, resp, trusted, twoFA)
			if err != nil {
				return nil, err
			}
			// loop again to inspect the new status
		case "device":
			resp, signKey, trustedKey, err = handleDeviceApproval(ctx, client, cfg, user, resp, trusted, twoFA)
			if err != nil {
				return nil, err
			}
			// loop again to inspect the new status
		default:
			return nil, fmt.Errorf("login did not return ok (status=%q)", status)
		}
	}
}

// handleTwoFactor performs the second-factor exchange for the given method and
// returns the resulting (hopefully "ok") response.
func handleTwoFactor(ctx context.Context, client *http.Client, cfg *LoginConfig, user, method string, prev map[string]any, trusted *TrustedDeviceRequest, twoFA TwoFactorFunc) (map[string]any, *SignKey, *SignKey, error) {
	if twoFA == nil {
		return nil, nil, nil, fmt.Errorf("two-factor authentication (%s) required but no code provider is configured", method)
	}
	tempKey := stringOr(prev["tempKey"], "")

	// For email, ask the server to send the code first.
	if method == "email" {
		eresp, err := encryptAndPost(ctx, client, cfg, "login", "email", map[string]any{
			"type": "login", "step": "email", "username": user, "password": tempKey + ":EMAIL",
		})
		if err != nil {
			return nil, nil, nil, err
		}
		if tk := stringOr(eresp["tempKey"], ""); tk != "" {
			tempKey = tk
		}
	}

	const maxAttempts = 3
	for attempt := 0; attempt < maxAttempts; attempt++ {
		code, err := twoFA(method, attempt > 0)
		if err != nil {
			return nil, nil, nil, err
		}
		code = strings.TrimSpace(code)
		resp, signKey, trustedKey, err := credentialStep(ctx, client, cfg, user, method, tempKey+":"+code, trusted)
		if err != nil {
			// A rejected code comes back as status==method (not an error status),
			// so a real error here is terminal.
			return nil, nil, nil, err
		}
		switch stringOr(resp["status"], "") {
		case "ok":
			return resp, signKey, trustedKey, nil
		case method:
			// Wrong/expired code: refresh tempKey if provided and retry.
			if tk := stringOr(resp["tempKey"], ""); tk != "" {
				tempKey = tk
			}
			continue
		default:
			// Any other status (e.g. another factor) is returned to the caller.
			return resp, signKey, trustedKey, nil
		}
	}
	return nil, nil, nil, fmt.Errorf("two-factor authentication failed after %d attempts", maxAttempts)
}

// handleDeviceApproval polls the server while the user approves the login on a
// trusted device. No code is entered; the status flips from "device"/"wait" to
// "ok" once approved. twoFA (if set) is called once with method "device" so the
// UI can tell the user to approve.
func handleDeviceApproval(ctx context.Context, client *http.Client, cfg *LoginConfig, user string, prev map[string]any, trusted *TrustedDeviceRequest, twoFA TwoFactorFunc) (map[string]any, *SignKey, *SignKey, error) {
	if twoFA != nil {
		_, _ = twoFA("device", false) // notification only; any returned value is ignored
	}
	tempKey := stringOr(prev["tempKey"], "")
	deadline := time.Now().Add(deviceApprovalTimeout)

	for {
		resp, signKey, trustedKey, err := credentialStep(ctx, client, cfg, user, "device", tempKey, trusted)
		if err != nil {
			return nil, nil, nil, err
		}
		switch stringOr(resp["status"], "") {
		case "ok":
			return resp, signKey, trustedKey, nil
		case "wait", "device":
			if tk := stringOr(resp["tempKey"], ""); tk != "" {
				tempKey = tk
			}
			if time.Now().After(deadline) {
				return nil, nil, nil, fmt.Errorf("device approval timed out")
			}
			select {
			case <-ctx.Done():
				return nil, nil, nil, ctx.Err()
			case <-time.After(2 * time.Second):
			}
		default:
			// Unexpected status: return it for the caller to inspect.
			return resp, signKey, trustedKey, nil
		}
	}
}

// finishLogin builds the Bootstrap and records a registered trusted device.
func finishLogin(resp map[string]any, signKey *SignKey, trusted *TrustedDeviceRequest, trustedKey *SignKey) *Bootstrap {
	b := &Bootstrap{Raw: resp, SignKey: signKey}
	if id, ok := resp["trustedDeviceID"].(string); ok && id != "" {
		b.TrustedDeviceID = id
		b.TrustedDeviceName, _ = resp["trustedDeviceUserName"].(string)
		if trusted != nil && trustedKey != nil {
			trusted.Result = &TrustedDevice{ID: id, Name: trusted.Name, AuthKey: trustedKey}
		}
	}
	return b
}

func stringOr(v any, def string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return def
}
