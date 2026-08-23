package shell

import (
	"context"
	"testing"
	"time"
)

// fakeConn records SendText calls; Read/Close are unused (readLoop is not run;
// tests feed the out channel directly).
type fakeConn struct{ sent chan string }

func (f *fakeConn) SendText(b []byte) error { f.sent <- string(b); return nil }
func (f *fakeConn) Read() ([]byte, error)   { select {} }
func (f *fakeConn) Close() error            { return nil }

func newTestShell() (*Shell, *fakeConn) {
	f := &fakeConn{sent: make(chan string, 16)}
	s := &Shell{sock: f, termID: 1, out: make(chan []byte, 16), done: make(chan struct{})}
	return s, f
}

// waitInput returns the data field of the next SendText JSON (best-effort).
func waitInput(t *testing.T, f *fakeConn) string {
	t.Helper()
	select {
	case s := <-f.sent:
		return s
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for input")
		return ""
	}
}

func TestAuthenticateSuccess(t *testing.T) {
	sh, f := newTestShell()
	authFirstWait, authStepWait = time.Second, time.Second

	go func() {
		sh.out <- []byte("\x1b[2J\x1b[HUser: ")
		waitInput(t, f) // username
		sh.out <- []byte("dwtest\r\nPassword: ")
		waitInput(t, f) // password
		sh.out <- []byte("\x1b[2J\x1b[H")
	}()

	pre, authed, err := sh.Authenticate(context.Background(), "dwtest", func(u string, retry bool) (string, error) {
		return "secret", nil
	}, 1)
	if err != nil {
		t.Fatalf("auth failed: %v", err)
	}
	if pre != nil {
		t.Fatalf("expected nil preamble after auth, got %q", pre)
	}
	if !authed {
		t.Fatal("expected authenticated=true")
	}
}

func TestAuthenticateNoAuth(t *testing.T) {
	sh, _ := newTestShell()
	authFirstWait, authStepWait = time.Second, time.Second

	go func() {
		sh.out <- []byte("\x1b[?2004h\x1b]0;user@host\x07user@host:~$ ")
	}()

	pre, authed, err := sh.Authenticate(context.Background(), "dwtest", func(u string, retry bool) (string, error) {
		t.Fatal("password should not be requested when no auth")
		return "", nil
	}, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pre) == 0 {
		t.Fatal("expected preamble (initial prompt) to be returned")
	}
	if authed {
		t.Fatal("expected authenticated=false when no auth")
	}
}

func TestAuthenticateRetryThenSuccess(t *testing.T) {
	sh, f := newTestShell()
	authFirstWait, authStepWait = time.Second, time.Second
	var calls int

	go func() {
		sh.out <- []byte("\x1b[2J\x1b[HUser: ")
		waitInput(t, f) // user
		sh.out <- []byte("dwtest\r\nPassword: ")
		waitInput(t, f) // wrong pw
		sh.out <- []byte("\r\nLogin incorrect")
		sh.out <- []byte("\x1b[2J\x1b[HUser: ") // re-prompt
		waitInput(t, f)                         // user again
		sh.out <- []byte("dwtest\r\nPassword: ")
		waitInput(t, f) // right pw
		sh.out <- []byte("\x1b[2J\x1b[H")
	}()

	_, authed, err := sh.Authenticate(context.Background(), "dwtest", func(u string, retry bool) (string, error) {
		calls++
		if retry {
			return "right", nil
		}
		return "wrong", nil
	}, 3)
	if err != nil {
		t.Fatalf("auth should succeed on retry: %v", err)
	}
	if !authed {
		t.Fatal("expected authenticated=true after retry")
	}
	if calls != 2 {
		t.Fatalf("expected 2 password attempts, got %d", calls)
	}
}
