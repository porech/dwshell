package auth

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
)

// LoginPageURL is the entry point served by DWService.
const LoginPageURL = "https://access.dwservice.net/login.dw"

// LoginConfig holds the values dwshell needs from the login page. The server's
// ECDH public key rotates, so this must be fetched live before each login.
type LoginConfig struct {
	ServerKeyB64 string // cryptAlgorithmAccept[0].importKey.keyData (SPKI, P-256)
	BaseURL      string // e.g. https://access.dwservice.net/
	RequestURL   string // authentication endpoint base
}

// AuthURL returns the authentication.dw endpoint.
func (c *LoginConfig) AuthURL() string {
	base := c.RequestURL
	if base == "" {
		base = c.BaseURL
	}
	return base + "authentication.dw"
}

// hexBlobRe matches the hex-encoded config blob passed to dwsInitDecodeHex() in
// loadConfiguration(); it decodes to the JSON that seeds dwsConfig.
var hexBlobRe = regexp.MustCompile(`dwsInitDecodeHex\('([0-9a-f]{200,})'\)`)

type rawLoginConfig struct {
	BaseURL              string `json:"baseUrl"`
	RequestURL           string `json:"requestUrl"`
	CryptAlgorithmAccept []struct {
		ImportKey struct {
			KeyData string `json:"keyData"`
		} `json:"importKey"`
	} `json:"cryptAlgorithmAccept"`
}

// FetchLoginConfig loads the login page and extracts the crypto config.
func FetchLoginConfig(ctx context.Context, client *http.Client) (*LoginConfig, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, LoginPageURL+"?localeid=en", nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch login page: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return parseLoginConfig(body)
}

func parseLoginConfig(html []byte) (*LoginConfig, error) {
	m := hexBlobRe.FindSubmatch(html)
	if m == nil {
		return nil, fmt.Errorf("login config blob not found on page")
	}
	raw, err := hex.DecodeString(string(m[1]))
	if err != nil {
		return nil, fmt.Errorf("decode config hex: %w", err)
	}
	var rc rawLoginConfig
	if err := json.Unmarshal(raw, &rc); err != nil {
		return nil, fmt.Errorf("parse config json: %w", err)
	}
	if len(rc.CryptAlgorithmAccept) == 0 || rc.CryptAlgorithmAccept[0].ImportKey.KeyData == "" {
		return nil, fmt.Errorf("server crypto key not present in config")
	}
	return &LoginConfig{
		ServerKeyB64: rc.CryptAlgorithmAccept[0].ImportKey.KeyData,
		BaseURL:      rc.BaseURL,
		RequestURL:   rc.RequestURL,
	}, nil
}
