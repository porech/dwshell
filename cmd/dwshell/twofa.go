package main

import (
	"bufio"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"time"

	xterm "golang.org/x/term"

	"github.com/porech/dwshell/internal/auth"
)

// stdinReader is shared so retries don't drop buffered input.
var stdinReader = bufio.NewReader(os.Stdin)

// twoFactorProvider returns a callback that supplies a second-factor code.
//
// For TOTP, non-interactively: DWSHELL_TOTP_SECRET lets dwshell generate the code
// itself (regenerated on each retry), or DWSHELL_TOTP_CODE provides a ready code.
// The email code cannot be known in advance (the server sends it on demand), so
// it is read from the prompt or stdin. Otherwise it prompts.
func twoFactorProvider() auth.TwoFactorFunc {
	return func(method string, retry bool) (string, error) {
		switch method {
		case "totp":
			if secret := os.Getenv("DWSHELL_TOTP_SECRET"); secret != "" {
				return totpCode(secret) // regenerates on retry (fresh time window)
			}
			if !retry {
				if code := os.Getenv("DWSHELL_TOTP_CODE"); code != "" {
					return code, nil
				}
			}
			return promptCode("Two-factor code (TOTP): ", retry)
		case "email":
			return promptCode("Enter the code sent to your email: ", retry)
		case "device":
			// No code: the auth layer polls until you approve on your device.
			fmt.Fprintln(os.Stderr, "Waiting for approval on your trusted device…")
			return "", nil
		default:
			return promptCode(fmt.Sprintf("Two-factor code (%s): ", method), retry)
		}
	}
}

// promptCode reads a code from stdin — a TTY prompt interactively, or a line
// from a pipe/FIFO for scripted use.
func promptCode(label string, retry bool) (string, error) {
	tty := xterm.IsTerminal(int(os.Stdin.Fd()))
	if tty {
		if retry {
			fmt.Fprintln(os.Stderr, "Invalid code, try again.")
		}
		fmt.Fprint(os.Stderr, label)
	}
	line, err := stdinReader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		if err != nil {
			return "", fmt.Errorf("two-factor code required; provide it on stdin, or (TOTP) via DWSHELL_TOTP_SECRET or DWSHELL_TOTP_CODE")
		}
	}
	return line, nil
}

// totpCode computes the current RFC 6238 TOTP (SHA-1, 6 digits, 30s step) for a
// base32 secret.
func totpCode(secret string) (string, error) { return totpCodeAt(secret, time.Now()) }

func totpCodeAt(secret string, t time.Time) (string, error) {
	secret = strings.ToUpper(strings.ReplaceAll(secret, " ", ""))
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		return "", fmt.Errorf("invalid TOTP secret: %w", err)
	}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(t.Unix())/30)
	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	val := (uint32(sum[off]&0x7f) << 24) |
		(uint32(sum[off+1]) << 16) |
		(uint32(sum[off+2]) << 8) |
		uint32(sum[off+3])
	return fmt.Sprintf("%06d", val%1_000_000), nil
}
