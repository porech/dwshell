package main

import (
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

// twoFactorProvider returns a callback that supplies a second-factor code.
//
// For TOTP it uses, in order: DWSHELL_TOTP_CODE (a ready code), DWSHELL_TOTP_SECRET
// (generates the code locally), then an interactive prompt. For email it uses
// DWSHELL_2FA_CODE or an interactive prompt (the server sends the code first).
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
			if !retry {
				if code := os.Getenv("DWSHELL_2FA_CODE"); code != "" {
					return code, nil
				}
			}
			return promptCode("Enter the code sent to your email: ", retry)
		default:
			return promptCode(fmt.Sprintf("Two-factor code (%s): ", method), retry)
		}
	}
}

func promptCode(label string, retry bool) (string, error) {
	if !xterm.IsTerminal(int(os.Stdin.Fd())) {
		return "", fmt.Errorf("two-factor code required but no TTY; set DWSHELL_TOTP_SECRET or DWSHELL_TOTP_CODE (TOTP), or DWSHELL_2FA_CODE (email)")
	}
	if retry {
		fmt.Fprintln(os.Stderr, "Invalid code, try again.")
	}
	fmt.Fprint(os.Stderr, label)
	var code string
	fmt.Scanln(&code)
	return strings.TrimSpace(code), nil
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
