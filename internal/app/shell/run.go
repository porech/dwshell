package shell

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/porech/dwshell/internal/remote"
	"github.com/porech/dwshell/internal/session"
)

var (
	reBegin = regexp.MustCompile(`__DWSH_BEGIN__\r?\n`)
	reRC    = regexp.MustCompile(`__DWSH_RC_(\d+)_END__`)
)

// RunResult is the outcome of a non-interactive command.
type RunResult struct {
	Output        []byte // command output between the markers, CRLF normalized to LF
	ExitCode      int
	Authenticated bool // whether the agent required (and we performed) a login
}

// wrapCommand builds an OS-specific command that brackets the real command with
// a BEGIN marker and a RC sentinel so output and exit code can be extracted from
// the raw PTY stream (which also echoes the input).
func wrapCommand(cmd string, os remote.OS) string {
	if os == remote.OSWindows {
		// Run in a child cmd so an `exit` in the command does not close the
		// session; %errorlevel% then reflects the child's exit code.
		return fmt.Sprintf("echo __DWSH_BEGIN__& cmd /c %s & echo __DWSH_RC_%%errorlevel%%_END__\r", cmd)
	}
	// Run in a subshell so an `exit N` in the command only leaves the subshell
	// and $? still carries the code for the RC sentinel.
	return fmt.Sprintf("echo __DWSH_BEGIN__; ( %s ); echo __DWSH_RC_$?_END__\r", cmd)
}

// Run executes a single command non-interactively and returns its output and
// exit code. It opens a fresh shell, sends the wrapped command, and reads until
// the RC sentinel (or ctx/timeout fires).
func Run(ctx context.Context, sess *session.Session, os remote.OS, command string, timeout time.Duration, username string, getPassword PasswordFunc) (*RunResult, error) {
	sh, err := Open(ctx, sess, 200, 50)
	if err != nil {
		return nil, err
	}
	defer sh.Close()

	// Handle agent-side login if required (single attempt; non-interactive).
	_, authed, err := sh.Authenticate(ctx, username, getPassword, 1)
	if err != nil {
		return nil, err
	}

	if err := sh.Input(wrapCommand(command, os)); err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, fmt.Errorf("timed out waiting for command to finish")
		case chunk, ok := <-sh.Output():
			if !ok {
				if sh.Err() != nil {
					return nil, sh.Err()
				}
				return nil, fmt.Errorf("shell closed before command finished")
			}
			buf.Write(chunk)
			if m := reRC.FindSubmatchIndex(buf.Bytes()); m != nil {
				res, err := extract(buf.Bytes(), m)
				if res != nil {
					res.Authenticated = authed
				}
				return res, err
			}
		}
	}
}

func extract(b []byte, rcMatch []int) (*RunResult, error) {
	code, _ := strconv.Atoi(string(b[rcMatch[2]:rcMatch[3]]))
	rcStart := rcMatch[0]

	// Output starts after the last real BEGIN marker line before the RC marker.
	var outStart int
	for _, loc := range reBegin.FindAllIndex(b[:rcStart], -1) {
		outStart = loc[1]
	}
	out := b[outStart:rcStart]
	out = stripOSC(out) // remove OSC title sequences (e.g. cmd.exe window title)
	// Trim a trailing newline left before the RC echo.
	out = bytes.TrimRight(out, "\r\n")
	out = bytes.ReplaceAll(out, []byte("\r\n"), []byte("\n"))
	return &RunResult{Output: out, ExitCode: code}, nil
}

// stripOSC removes OSC sequences (ESC ] ... BEL, or ESC ] ... ESC \). These
// carry the terminal/window title and are never meaningful captured output.
// SGR color sequences (ESC [ ... m) are left untouched.
func stripOSC(b []byte) []byte {
	var out []byte
	for i := 0; i < len(b); {
		if b[i] == 0x1b && i+1 < len(b) && b[i+1] == ']' {
			j := i + 2
			for j < len(b) {
				if b[j] == 0x07 { // BEL terminator
					j++
					break
				}
				if b[j] == 0x1b && j+1 < len(b) && b[j+1] == '\\' { // ST terminator
					j += 2
					break
				}
				j++
			}
			i = j
			continue
		}
		out = append(out, b[i])
		i++
	}
	return out
}
