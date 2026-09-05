package shell

import (
	"strings"
	"testing"
)

// A short command line cannot have been truncated by any remote shell, so the
// timeout must not speculate about it.
func TestTimeoutErrStaysPlainForAShortLine(t *testing.T) {
	err := timeoutErr([]byte("__DWSH_BEGIN__\r\nsome output\r\n"), 120)
	if got := err.Error(); got != "timed out waiting for command to finish" {
		t.Fatalf("expected the plain timeout message, got %q", got)
	}
}

// Long enough to have been cut, and something did run: offer truncation as a
// possibility, with the way around it.
func TestTimeoutErrHintsTruncationForALongLine(t *testing.T) {
	err := timeoutErr([]byte("__DWSH_BEGIN__\r\nsome output\r\n"), 9000)
	got := err.Error()
	for _, want := range []string{"timed out", "9000", "truncate", "dwshell put"} {
		if !strings.Contains(got, want) {
			t.Errorf("message %q does not mention %q", got, want)
		}
	}
}

// Nothing ran at all — the marker never came back — so truncation is not the
// story and the hint would mislead.
func TestTimeoutErrStaysPlainWhenNothingRan(t *testing.T) {
	err := timeoutErr([]byte("no marker here"), 9000)
	if got := err.Error(); got != "timed out waiting for command to finish" {
		t.Fatalf("expected the plain timeout message, got %q", got)
	}
}
