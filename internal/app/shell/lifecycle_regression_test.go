package shell

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

// This isolated transport blocks writes until explicitly released or closed.
// No account, remote agent, network socket, or saved credentials are used.
type diagnosticBlockedConn struct {
	entered   chan struct{}
	release   chan struct{}
	closed    chan struct{}
	enterOnce sync.Once
	closeOnce sync.Once
}

func (f *diagnosticBlockedConn) SendText([]byte) error {
	f.enterOnce.Do(func() { close(f.entered) })
	select {
	case <-f.release:
		return nil
	case <-f.closed:
		return errors.New("test transport closed")
	}
}
func (f *diagnosticBlockedConn) Read() ([]byte, error) {
	<-f.closed
	return nil, errors.New("test transport closed")
}
func (f *diagnosticBlockedConn) Close() error {
	f.closeOnce.Do(func() { close(f.closed) })
	return nil
}

func TestCloseAbortsBlockedInput(t *testing.T) {
	f := &diagnosticBlockedConn{entered: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{})}
	s := &Shell{sock: f, termID: 1, out: make(chan []byte, 64), done: make(chan struct{})}
	inputDone, closeDone := make(chan struct{}), make(chan struct{})
	go func() { _ = s.Input("pwd\r"); close(inputDone) }()
	<-f.entered
	go func() { _ = s.Close(); close(closeDone) }()
	select {
	case <-f.closed:
		t.Log("Close interrupted the blocked input transport")
	case <-time.After(closeGracePeriod + time.Second):
		close(f.release)
		t.Fatal("Close did not interrupt the blocked input within its grace period")
	}
	close(f.release)
	select {
	case <-inputDone:
	case <-time.After(time.Second):
		t.Fatal("input cleanup failed")
	}
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("close cleanup failed")
	}
}

func TestCloseAbortsStalledClosingFrame(t *testing.T) {
	f := &diagnosticBlockedConn{entered: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{})}
	s := &Shell{sock: f, termID: 1, out: make(chan []byte, 64), done: make(chan struct{})}
	done := make(chan struct{})
	go func() { _ = s.Close(); close(done) }()
	<-f.entered
	select {
	case <-f.closed:
		t.Log("Close interrupted its own stalled closing frame")
	case <-time.After(closeGracePeriod + time.Second):
		close(f.release)
		t.Fatal("Close did not abort its stalled closing frame within its grace period")
	}
	close(f.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("close cleanup failed")
	}
}

func TestNormalCloseRequestsTerminalAndAppCleanup(t *testing.T) {
	s, f := newTestShell()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	frames := drain(f)
	if len(frames) != 2 {
		t.Fatalf("expected terminal close plus app termination, got %v", frames)
	}
	var terminal struct {
		ID   int `json:"id"`
		Type int `json:"type"`
	}
	if err := json.Unmarshal([]byte(frames[0]), &terminal); err != nil {
		t.Fatal(err)
	}
	if terminal.ID != s.termID || terminal.Type != reqClose {
		t.Fatalf("unexpected terminal close: %s", frames[0])
	}
	var app struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(frames[1]), &app); err != nil {
		t.Fatal(err)
	}
	if app.Type != "term" {
		t.Fatalf("unexpected app termination: %s", frames[1])
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if extra := drain(f); len(extra) != 0 {
		t.Fatalf("repeated Close sent extra frames: %v", extra)
	}
}
