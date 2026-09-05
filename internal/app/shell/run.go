package shell

import (
	"bytes"
	"context"
	"errors"
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
	reTrunc = regexp.MustCompile(`__DWSH_TRUNC_(\d+)_END__`)
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

// errTruncated reports a command line the remote shell cut short. How long a
// line a remote accepts is the remote shell's own business and differs between
// shells, so dwshell does not try to predict it — it reports the truncation
// when it happens instead.
var errTruncated = errors.New(
	"the remote shell truncated the command line, so it ran a partial command and the exit-code marker was lost; " +
		"send a shorter command, or upload it with `dwshell put` and run it by path")

// probeLine is typed as its own short line right after the command. A shell
// reads it only once it has read and run the command line, so its marker can
// never come back before that command's RC sentinel — unless the remote
// truncated the command line and took the sentinel with it. That makes a lost
// sentinel detectable on any remote, whatever its line limit happens to be.
//
// Like the RC sentinel, the marker is assembled by the remote (`$?` /
// %errorlevel%) so that the PTY echoing the typed line back, or a command that
// reads its stdin and prints it, cannot be mistaken for the marker itself.
func probeLine(os remote.OS) string {
	if os == remote.OSWindows {
		return "echo __DWSH_TRUNC_%errorlevel%_END__\r"
	}
	return "echo __DWSH_TRUNC_$?_END__\r"
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
	if err := sh.Input(probeLine(os)); err != nil {
		return nil, err
	}

	var buf bytes.Buffer

	// A non-positive timeout means no deadline (rely on ctx / Ctrl-C); a nil
	// channel blocks forever in the select.
	var timeoutCh <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		timeoutCh = t.C
	}

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timeoutCh:
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
			// The probe came back but the sentinel never did: the remote cut
			// the command line short and ran what was left of it.
			if reTrunc.Match(buf.Bytes()) {
				return nil, errTruncated
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
