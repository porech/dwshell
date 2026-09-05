package shell

import (
	"testing"

	"github.com/porech/dwshell/internal/remote"
)

// The probe marker must be assembled by the remote shell, so that neither the
// PTY echoing the typed line back nor a command printing its own stdin can be
// mistaken for the marker.
func TestProbeMarkerCannotMatchTheTypedLine(t *testing.T) {
	for _, os := range []remote.OS{remote.OSWindows, remote.OSLinux} {
		typed := probeLine(os)
		if reTrunc.MatchString(typed) {
			t.Errorf("the typed probe line %q matches the marker regexp", typed)
		}
	}
}

func TestProbeMarkerMatchesWhatTheShellPrints(t *testing.T) {
	if !reTrunc.MatchString("__DWSH_TRUNC_0_END__\r\n") {
		t.Error("the printed probe marker is not recognised")
	}
}

// A probe line has to survive the very truncation it detects, so it must stay
// far below any line limit a remote shell might impose.
func TestProbeLineIsShort(t *testing.T) {
	for _, os := range []remote.OS{remote.OSWindows, remote.OSLinux} {
		if n := len(probeLine(os)); n > 100 {
			t.Errorf("probe line for %v is %d characters, too long to survive truncation", os, n)
		}
	}
}
