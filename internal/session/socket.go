package session

import (
	"context"
	"fmt"
	"net/url"

	"github.com/gorilla/websocket"
)

// Socket is a generic DWService app WebSocket carrying JSON text frames. Each
// app (shell, and future files/desktop) layers its own sub-protocol on top.
type Socket struct {
	conn *websocket.Conn
}

// OpenSocket opens a WebSocket for the given app module over this session
// (PROTOCOL.md §5.3). The relay host is the node prefixed with "s<slot>-".
func (s *Session) OpenSocket(ctx context.Context, module string) (*Socket, error) {
	const slot = 1

	u, err := url.Parse(s.commandURL)
	if err != nil {
		return nil, err
	}
	u.Scheme = "wss"
	u.Host = fmt.Sprintf("s%d-%s", slot, u.Host)

	key, err := s.signKey.NextSessionKey()
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("module", module)
	q.Set("request", "websocket")
	q.Set("simulate", "false")
	q.Set("slot", fmt.Sprintf("%d", slot))
	q.Set("_sk", key)
	u.RawQuery = q.Encode()

	dialer := *websocket.DefaultDialer
	dialer.Jar = s.client.Jar // carry the node's DWSID cookie into the handshake
	conn, resp, err := dialer.DialContext(ctx, u.String(), nil)
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("open %s socket: %w (http %d)", module, err, resp.StatusCode)
		}
		return nil, fmt.Errorf("open %s socket: %w", module, err)
	}
	return &Socket{conn: conn}, nil
}

// SendJSON writes one JSON text frame (the caller passes the marshaled bytes).
func (s *Socket) SendText(b []byte) error {
	return s.conn.WriteMessage(websocket.TextMessage, b)
}

// Read returns the next text frame payload.
func (s *Socket) Read() ([]byte, error) {
	for {
		mt, b, err := s.conn.ReadMessage()
		if err != nil {
			return nil, err
		}
		if mt == websocket.TextMessage || mt == websocket.BinaryMessage {
			return b, nil
		}
	}
}

// Close closes the socket.
func (s *Socket) Close() error { return s.conn.Close() }
