// Package term bridges a local terminal to a remote DWService shell: raw mode,
// stdin→input, output→stdout, and size-change propagation. It is cross-platform
// (golang.org/x/term covers Unix termios and the Windows console).
package term

import (
	"context"
	"errors"
	"io"
	"os"
	"time"

	xterm "golang.org/x/term"
)

// Remote is the minimal shell interface the bridge drives.
type Remote interface {
	Input(string) error
	Resize(cols, rows int) error
	Output() <-chan []byte
}

// Size returns the local terminal size, defaulting to 80x24 when stdout is not
// a terminal.
func Size() (cols, rows int) {
	if c, r, err := xterm.GetSize(int(os.Stdout.Fd())); err == nil && c > 0 && r > 0 {
		return c, r
	}
	return 80, 24
}

// IsTTY reports whether stdin is an interactive terminal.
func IsTTY() bool { return xterm.IsTerminal(int(os.Stdin.Fd())) }

const resizePoll = 250 * time.Millisecond

// Bridge connects the local terminal to r until the remote output closes or
// stdin reaches EOF. When stdin is a TTY it is switched to raw mode and restored
// on return.
func Bridge(ctx context.Context, r Remote) error {
	inFd := int(os.Stdin.Fd())
	if xterm.IsTerminal(inFd) {
		old, err := xterm.MakeRaw(inFd)
		if err == nil {
			defer xterm.Restore(inFd, old)
		}
	}

	// stdin → remote input
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				if werr := r.Input(string(buf[:n])); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// local size changes → remote resize (polling; instant enough, no signals)
	go func() {
		lastC, lastR := Size()
		t := time.NewTicker(resizePoll)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				c, rw := Size()
				if c != lastC || rw != lastR {
					lastC, lastR = c, rw
					_ = r.Resize(c, rw)
				}
			}
		}
	}()

	// remote output → stdout (runs until the channel closes)
	out := r.Output()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case chunk, ok := <-out:
			if !ok {
				return nil
			}
			if _, err := os.Stdout.Write(chunk); err != nil {
				if errors.Is(err, io.ErrClosedPipe) {
					return nil
				}
				return err
			}
		}
	}
}
