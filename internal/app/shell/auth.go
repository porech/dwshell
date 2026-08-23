package shell

import (
	"bytes"
	"context"
	"fmt"
	"time"
)

// Agent-side shell authentication (when `shell.enable_authentication` is set on
// the agent) is an in-terminal login prompt rendered over the normal data
// channel: the agent clears the screen and prints "User: ", reads the username,
// prints "Password: ", reads the password (echoed as '*'), validates it, and on
// failure prints "Login incorrect" and repeats. dwshell drives this prompt
// automatically for an SSH-like experience.
const (
	promptUser  = "User: "
	promptPass  = "Password: "
	loginFailed = "Login incorrect"
	clearScreen = "\x1b[2J"
)

// PasswordFunc supplies a password on demand (e.g. a TTY prompt). retry is true
// when a previous attempt was rejected.
type PasswordFunc func(user string, retry bool) (string, error)

// authTimeouts (overridable in tests).
var (
	authFirstWait = 5 * time.Second
	authStepWait  = 8 * time.Second
)

// readNext returns the next output chunk, or open=false if the stream closed, or
// timedOut=true if nothing arrived within d.
func (s *Shell) readNext(d time.Duration) (b []byte, open bool, timedOut bool) {
	select {
	case chunk, ok := <-s.out:
		return chunk, ok, false
	case <-time.After(d):
		return nil, true, true
	}
}

// Authenticate handles the agent's in-terminal login prompt if present.
//
// When no authentication is required it returns (initial output, false, nil) —
// the bytes are the start of the session and must be displayed by the caller.
// When authentication succeeds it returns (nil, true, nil). username is sent
// automatically (SSH-style default); getPassword is called only when the agent
// actually asks for a password.
func (s *Shell) Authenticate(ctx context.Context, username string, getPassword PasswordFunc, maxAttempts int) (preamble []byte, authenticated bool, err error) {
	// Wait for the first output and decide whether a login prompt is present.
	var buf bytes.Buffer
	deadline := time.Now().Add(authFirstWait)
	for {
		chunk, open, timedOut := s.readNext(time.Until(deadline))
		if !open {
			return nil, false, s.errOrClosed()
		}
		if timedOut {
			// No login prompt appeared; treat as no-auth. Nothing buffered.
			return buf.Bytes(), false, nil
		}
		buf.Write(chunk)
		if bytes.Contains(buf.Bytes(), []byte(promptUser)) {
			break // login required
		}
		// If we already have a normal prompt (bracketed paste / OSC), no auth.
		if bytes.Contains(buf.Bytes(), []byte("\x1b[?2004h")) || bytes.Contains(buf.Bytes(), []byte("\x1b]0;")) {
			return buf.Bytes(), false, nil
		}
	}

	// Login loop.
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := s.Input(username + "\r"); err != nil {
			return nil, false, err
		}
		if err := s.waitFor(promptPass); err != nil {
			return nil, false, fmt.Errorf("waiting for password prompt: %w", err)
		}
		pw, perr := getPassword(username, attempt > 0)
		if perr != nil {
			return nil, false, perr
		}
		if err := s.Input(pw + "\r"); err != nil {
			return nil, false, err
		}
		ok, err := s.awaitLoginResult()
		if err != nil {
			return nil, false, err
		}
		if ok {
			return nil, true, nil // authenticated; screen cleared, session follows
		}
		// Rejected: the agent will redisplay "User: "; wait for it, then retry.
		if err := s.waitFor(promptUser); err != nil {
			return nil, false, fmt.Errorf("authentication failed")
		}
	}
	return nil, false, fmt.Errorf("authentication failed after %d attempts", maxAttempts)
}

// waitFor reads output until the marker appears or the step times out.
func (s *Shell) waitFor(marker string) error {
	var buf bytes.Buffer
	deadline := time.Now().Add(authStepWait)
	for {
		chunk, open, timedOut := s.readNext(time.Until(deadline))
		if !open {
			return s.errOrClosed()
		}
		if timedOut {
			return fmt.Errorf("timed out waiting for %q", marker)
		}
		buf.Write(chunk)
		if bytes.Contains(buf.Bytes(), []byte(marker)) {
			return nil
		}
	}
}

// awaitLoginResult reads until it can tell whether the password was accepted
// (screen clear) or rejected ("Login incorrect").
func (s *Shell) awaitLoginResult() (ok bool, err error) {
	var buf bytes.Buffer
	deadline := time.Now().Add(authStepWait)
	for {
		chunk, open, timedOut := s.readNext(time.Until(deadline))
		if !open {
			return false, s.errOrClosed()
		}
		if timedOut {
			return false, fmt.Errorf("timed out waiting for login result")
		}
		buf.Write(chunk)
		if bytes.Contains(buf.Bytes(), []byte(loginFailed)) {
			return false, nil
		}
		if bytes.Contains(buf.Bytes(), []byte(clearScreen)) {
			return true, nil
		}
	}
}

func (s *Shell) errOrClosed() error {
	if s.readErr != nil {
		return s.readErr
	}
	return fmt.Errorf("shell closed during authentication")
}
