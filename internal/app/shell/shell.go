// Package shell implements the DWService "shell" app over an agent session: it
// loads the app, opens the WebSocket, and speaks the terminal JSON sub-protocol
// (PROTOCOL.md §5). It exposes a small transport used by both the interactive
// TTY bridge and the non-interactive command runner.
package shell

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/porech/dwshell/internal/session"
)

// message type codes (client → server).
const (
	reqOpen         = 0
	reqClose        = 1
	reqInput        = 2
	reqChangeSize   = 3
	keepAlivePeriod = 30 * time.Second
)

// frameConn is the subset of a session socket the shell uses (satisfied by
// *session.Socket; a fake is used in tests).
type frameConn interface {
	SendText([]byte) error
	Read() ([]byte, error)
	Close() error
}

// Shell is a single remote terminal over one agent session.
type Shell struct {
	sock   frameConn
	termID int

	out  chan []byte
	done chan struct{}

	writeMu sync.Mutex
	once    sync.Once
	readErr error
}

// outFrame is a server → client message. Both a bare {id,data} and a typed
// {type:"data",id,data} form are accepted; "info"/terminate are handled too.
type outFrame struct {
	Type      string `json:"type"`
	ID        int    `json:"id"`
	Data      string `json:"data"`
	Terminate bool   `json:"terminate"`
	Version   int    `json:"version"`
	IDs       []int  `json:"ids"`
}

// Open loads the shell app, opens the socket, and starts terminal 1 with the
// given size. Output frames for that terminal are delivered on Output().
func Open(ctx context.Context, sess *session.Session, cols, rows int) (*Shell, error) {
	if err := loadShellApp(ctx, sess); err != nil {
		return nil, err
	}
	sock, err := sess.OpenSocket(ctx, "shell")
	if err != nil {
		return nil, err
	}
	s := &Shell{
		sock:   sock,
		termID: 1,
		out:    make(chan []byte, 64),
		done:   make(chan struct{}),
	}
	// Open the terminal, then announce.
	if err := s.send(map[string]any{"id": s.termID, "type": reqOpen, "cols": cols, "rows": rows}); err != nil {
		sock.Close()
		return nil, err
	}
	if err := s.send(map[string]any{"type": "init"}); err != nil {
		sock.Close()
		return nil, err
	}
	go s.readLoop()
	go s.keepAliveLoop()
	return s, nil
}

// loadShellApp loads the shell app on the agent. The agent may lazily download
// the app on first use (the first probe after an agent comes online can fail with
// "command response missing"), so a failed first load is retried once — mirroring
// files.Open.
func loadShellApp(ctx context.Context, sess *session.Session) error {
	if _, err := sess.Execute(ctx, "core", "load_app", map[string]string{"name": "shell"}); err == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(1500 * time.Millisecond):
	}
	if _, err := sess.Execute(ctx, "core", "load_app", map[string]string{"name": "shell"}); err != nil {
		return fmt.Errorf("load shell app: %w", err)
	}
	return nil
}

// Output delivers raw terminal output bytes; it is closed when the terminal ends
// or the connection drops (check Err afterward).
func (s *Shell) Output() <-chan []byte { return s.out }

// Err returns the terminating read error, if any.
func (s *Shell) Err() error { return s.readErr }

func (s *Shell) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.sock.SendText(b)
}

// Input sends raw keystrokes to the terminal immediately (no batching).
func (s *Shell) Input(data string) error {
	return s.send(map[string]any{"id": s.termID, "type": reqInput, "data": data})
}

// Resize notifies the remote PTY of a new size (triggers SIGWINCH).
func (s *Shell) Resize(cols, rows int) error {
	return s.send(map[string]any{"id": s.termID, "type": reqChangeSize, "rows": rows, "cols": cols})
}

// Close terminates the terminal and the shell app, then closes the socket.
func (s *Shell) Close() error {
	s.once.Do(func() {
		close(s.done)
		_ = s.send(map[string]any{"id": s.termID, "type": reqClose})
		_ = s.send(map[string]any{"type": "term"})
		s.sock.Close()
	})
	return nil
}

func (s *Shell) readLoop() {
	defer close(s.out)
	for {
		b, err := s.sock.Read()
		if err != nil {
			select {
			case <-s.done: // expected during Close
			default:
				s.readErr = err
			}
			return
		}
		var f outFrame
		if json.Unmarshal(b, &f) != nil {
			continue
		}
		switch {
		case f.Type == "info":
			// terminal id liveness; nothing to do for a single terminal.
		case f.Terminate && f.ID == s.termID:
			return
		case f.ID == s.termID && f.Data != "":
			select {
			case s.out <- []byte(f.Data):
			case <-s.done:
				return
			}
		}
	}
}

func (s *Shell) keepAliveLoop() {
	t := time.NewTicker(keepAlivePeriod)
	defer t.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-t.C:
			if err := s.send(map[string]any{"type": "keepalive"}); err != nil {
				return
			}
		}
	}
}
