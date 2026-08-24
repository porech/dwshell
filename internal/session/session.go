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
	"sync/atomic"

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

	// xfer numbers transfers so each carries a unique key (the agent rejects a
	// reused transfer key), mirroring the client's incrementing transfer key.
	xfer atomic.Uint64
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

// Command is one request in a batched Execute.
type Command struct {
	Module  string
	Command string
	Params  map[string]string
}

// Result is the per-command outcome from a batched Execute.
type Result struct {
	Data []byte
	Err  error
}

// Execute runs a single command and returns the raw payload bytes (usually JSON;
// empty for a no-data success).
func (s *Session) Execute(ctx context.Context, module, command string, params map[string]string) ([]byte, error) {
	res, err := s.ExecuteBatch(ctx, []Command{{Module: module, Command: command, Params: params}})
	if err != nil {
		return nil, err
	}
	return res[0].Data, res[0].Err
}

// ExecuteBatch runs several commands in one request (`count=N`), matching the
// official client, and returns a per-command Result. A returned error is a
// transport/session-level failure; per-command errors are in each Result.
func (s *Session) ExecuteBatch(ctx context.Context, cmds []Command) ([]Result, error) {
	if len(cmds) == 0 {
		return nil, nil
	}
	s.cmdMu.Lock()
	defer s.cmdMu.Unlock()

	form := url.Values{}
	form.Set("count", strconv.Itoa(len(cmds)))
	ids := make([]string, len(cmds))
	for i, c := range cmds {
		id := "K" + strconv.Itoa(i)
		ids[i] = id
		form.Set("id_"+strconv.Itoa(i), id)
		form.Set("module_"+strconv.Itoa(i), c.Module)
		form.Set("command_"+strconv.Itoa(i), c.Command)
		for k, v := range c.Params {
			form.Set(fmt.Sprintf("parameter_%d_%s", i, k), v)
		}
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
	if req.URL, err = url.Parse(signedURL); err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("command request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	frames, err := parseFrames(string(body))
	if err != nil {
		return nil, err
	}
	results := make([]Result, len(cmds))
	for i, id := range ids {
		f, ok := frames[id]
		results[i].Data, results[i].Err = f.result(ok)
	}
	return results, nil
}

// frame is one decoded command response entry.
type frame struct {
	status byte
	data   string
}

func (f frame) result(found bool) ([]byte, error) {
	if !found {
		return nil, fmt.Errorf("command response missing")
	}
	switch f.status {
	case 'K':
		return []byte(f.data), nil
	case 'E':
		return nil, fmt.Errorf("command error: %s", f.data)
	case 'P', 'W':
		return nil, fmt.Errorf("command deferred (password/wait required)")
	default:
		return nil, fmt.Errorf("unexpected command status %q", f.status)
	}
}

// parseFrames decodes a framed command response (PROTOCOL.md §3) into a map of
// command id → frame. A leading E/D/B status is a session-level error.
func parseFrames(body string) (map[string]frame, error) {
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

	frames := map[string]frame{}
	i := 2 // skip "<S>:"
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
		if len(payload) < 1 {
			continue
		}
		fr := frame{status: payload[0]}
		if len(payload) >= 2 {
			fr.data = payload[2:] // skip "<inner>:"
		}
		frames[id] = fr
	}
	return frames, nil
}

func trimStatus(body string) string {
	if len(body) > 2 {
		return body[2:]
	}
	return ""
}
