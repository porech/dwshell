// Package session implements the DWService per-session command channel and the
// generic WebSocket transport. It is app-agnostic: the account session, and each
// agent/share session, are all the same type. Apps (shell, and future files/
// desktop) are layered on top via Execute and OpenSocket.
package session

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/porech/dwshell/internal/auth"
)

// Session is one authenticated DWService session (account or agent). Requests
// are signed per PROTOCOL.md §1.3.
type Session struct {
	commandURL string
	signKey    *auth.SignKey
	client     *http.Client

	customHeaders    bool
	keepAliveSeconds int

	// cmdMu serializes command-channel requests: the official client keeps at
	// most one command request in flight per session, so we do too. Transfers
	// (Download/Upload) use a distinct request type and are not gated by this.
	cmdMu sync.Mutex
}

// New creates a session bound to a command URL and its signing key. Call
// Initialize to learn server capabilities before issuing commands.
func New(commandURL string, signKey *auth.SignKey, client *http.Client) *Session {
	if client == nil {
		client = http.DefaultClient
	}
	return &Session{commandURL: commandURL, signKey: signKey, client: client}
}

// Restore rebuilds a session from persisted state, skipping Initialize.
func Restore(commandURL string, signKey *auth.SignKey, customHeaders bool, client *http.Client) *Session {
	s := New(commandURL, signKey, client)
	s.customHeaders = customHeaders
	return s
}

// SignKey returns the session's signing key (for persistence).
func (s *Session) SignKey() *auth.SignKey { return s.signKey }

// Valid reports whether the session is still alive, via a keepalive probe.
func (s *Session) Valid(ctx context.Context) bool {
	s.cmdMu.Lock()
	defer s.cmdMu.Unlock()

	u := s.commandURL + "?request=keepalive"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return false
	}
	signedURL, err := s.sign(req, u)
	if err != nil {
		return false
	}
	if req.URL, err = url.Parse(signedURL); err != nil {
		return false
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	buf := make([]byte, 1)
	n, _ := resp.Body.Read(buf)
	return n == 1 && (buf[0] == 'K' || buf[0] == 'W' || buf[0] == 'P')
}

// CommandURL returns the session's command URL.
func (s *Session) CommandURL() string { return s.commandURL }

// KeepAliveSeconds returns the server-advertised keepalive interval (0 if unknown).
func (s *Session) KeepAliveSeconds() int { return s.keepAliveSeconds }

// CustomHeaders reports whether the node authenticates via the DWS-Sec-Key header.
func (s *Session) CustomHeaders() bool { return s.customHeaders }

// sign attaches the per-request auth to a request, either as the DWS-Sec-Key
// header (when the node supports custom headers) or the _sk query parameter.
func (s *Session) sign(req *http.Request, u string) (string, error) {
	key, err := s.signKey.NextSessionKey()
	if err != nil {
		return "", err
	}
	if s.customHeaders {
		req.Header.Set("DWS-Sec-Key", key)
		return u, nil
	}
	if strings.Contains(u, "?") {
		u += "&"
	} else {
		u += "?"
	}
	return u + "_sk=" + url.QueryEscape(key), nil
}

// Initialize activates the session and fetches its runtime config
// (customHeaders, keepalive). It mirrors the browser's loadSessionConfig: a POST
// to ?resptype=json whose _sk is the signing key's cached initValue (reused
// verbatim), with a DWS-Sec-Key: CHECK probe header.
func (s *Session) Initialize(ctx context.Context) error {
	s.cmdMu.Lock()
	defer s.cmdMu.Unlock()

	initVal, err := s.signKey.InitValue()
	if err != nil {
		return err
	}
	u := s.commandURL + "?resptype=json&_sk=" + url.QueryEscape(initVal)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("DWS-Sec-Key", "CHECK")
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	cfg := parseJSONObject(body)
	if v, ok := cfg["customHeaders"].(bool); ok {
		s.customHeaders = v
	}
	if v, ok := cfg["keepAliveInterval"].(float64); ok {
		s.keepAliveSeconds = int(v)
	}
	if v, ok := cfg["commandUrl"].(string); ok && v != "" {
		s.commandURL = v
	}
	return nil
}

// Execute runs a single command and returns the raw payload bytes (usually JSON;
// empty for a no-data success). params values are sent as parameter_0_<k>.
func (s *Session) Execute(ctx context.Context, module, command string, params map[string]string) ([]byte, error) {
	s.cmdMu.Lock()
	defer s.cmdMu.Unlock()

	form := url.Values{}
	form.Set("count", "1")
	form.Set("id_0", "K1")
	form.Set("module_0", module)
	form.Set("command_0", command)
	for k, v := range params {
		form.Set("parameter_0_"+k, v)
	}

	u := s.commandURL + "?request=command"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	signedURL, err := s.sign(req, u)
	if err != nil {
		return nil, err
	}
	req.URL, err = url.Parse(signedURL)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("command %s/%s: %w", module, command, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return parseCommandResponse(string(body), "K1")
}

// parseCommandResponse decodes the framed command response (PROTOCOL.md §3),
// returning the payload for the given command id.
func parseCommandResponse(body, wantID string) ([]byte, error) {
	if body == "" {
		return nil, fmt.Errorf("empty response")
	}
	switch body[0] {
	case 'K', 'W', 'P':
		// ok (possibly with wait/password flags) — entries follow after "<S>:".
	case 'E':
		return nil, fmt.Errorf("server error: %s", trimStatus(body))
	case 'D':
		return nil, fmt.Errorf("session disconnected: %s", trimStatus(body))
	case 'B':
		return nil, fmt.Errorf("retry another node: %s", trimStatus(body))
	default:
		return nil, fmt.Errorf("unexpected response status %q", body[0])
	}

	// Entries: <cmdId>:<len>:<payload> starting at index 2.
	i := 2
	for i < len(body) {
		c := strings.IndexByte(body[i:], ':')
		if c < 0 {
			break
		}
		id := body[i : i+c]
		i += c + 1
		l := strings.IndexByte(body[i:], ':')
		if l < 0 {
			return nil, fmt.Errorf("malformed frame (no length)")
		}
		n, err := strconv.Atoi(body[i : i+l])
		if err != nil {
			return nil, fmt.Errorf("malformed length: %w", err)
		}
		i += l + 1
		if i+n > len(body) {
			return nil, fmt.Errorf("truncated payload")
		}
		payload := body[i : i+n]
		i += n
		if id != wantID {
			continue
		}
		if len(payload) < 1 {
			return nil, fmt.Errorf("empty payload frame")
		}
		inner := payload[0]
		data := ""
		if len(payload) >= 2 {
			data = payload[2:] // skip "<inner>:"
		}
		switch inner {
		case 'K':
			return []byte(data), nil
		case 'E':
			return nil, fmt.Errorf("command error: %s", data)
		case 'P', 'W':
			return nil, fmt.Errorf("command deferred (password/wait required)")
		default:
			return nil, fmt.Errorf("unexpected command status %q", inner)
		}
	}
	return nil, fmt.Errorf("command id %q not found in response", wantID)
}

func trimStatus(body string) string {
	if len(body) > 2 {
		return body[2:]
	}
	return ""
}
