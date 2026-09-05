package shell

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func init() { chunkPause = 0 } // the split itself is what these tests check

// drain collects every message the fake connection received.
func drain(f *fakeConn) []string {
	var out []string
	for {
		select {
		case s := <-f.sent:
			out = append(out, s)
		default:
			return out
		}
	}
}

// inputData decodes an input message and returns its data field.
func inputData(t *testing.T, msg string) string {
	t.Helper()
	var m struct {
		ID   int    `json:"id"`
		Type int    `json:"type"`
		Data string `json:"data"`
	}
	if err := json.Unmarshal([]byte(msg), &m); err != nil {
		t.Fatalf("unmarshal %q: %v", msg, err)
	}
	if m.Type != reqInput {
		t.Fatalf("expected an input message, got type %d", m.Type)
	}
	return m.Data
}

func TestInputSmallSendsOneMessage(t *testing.T) {
	sh, f := newTestShell()
	f.sent = make(chan string, 64)
	sh.sock = f

	if err := sh.Input("ls -l\r"); err != nil {
		t.Fatalf("Input: %v", err)
	}
	msgs := drain(f)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if got := inputData(t, msgs[0]); got != "ls -l\r" {
		t.Fatalf("data = %q", got)
	}
}

// The DWService relay drops the connection when a message arrives split across
// WebSocket continuation frames, which gorilla produces for any message larger
// than its write buffer. Long input must therefore be split into several input
// messages, each small enough to travel as a single frame.
func TestInputLongIsSplitIntoBoundedMessages(t *testing.T) {
	sh, f := newTestShell()
	f.sent = make(chan string, 1024)
	sh.sock = f

	data := strings.Repeat("x", 50_000) + "\r"
	if err := sh.Input(data); err != nil {
		t.Fatalf("Input: %v", err)
	}

	msgs := drain(f)
	if len(msgs) < 2 {
		t.Fatalf("expected the input to be split, got %d message(s)", len(msgs))
	}
	var got strings.Builder
	for _, m := range msgs {
		if len(m) > maxMessageBytes {
			t.Errorf("message of %d bytes exceeds the %d-byte limit", len(m), maxMessageBytes)
		}
		got.WriteString(inputData(t, m))
	}
	if got.String() != data {
		t.Fatalf("reassembled input differs: got %d bytes, want %d", got.Len(), len(data))
	}
}

// Escaping can inflate a rune up to sixfold in JSON, so the split has to bound
// the encoded message, not the raw data.
func TestInputLongEscapedStaysWithinLimit(t *testing.T) {
	sh, f := newTestShell()
	f.sent = make(chan string, 4096)
	sh.sock = f

	// Control characters and <, >, & are all escaped as \u00XX by encoding/json.
	data := strings.Repeat("\x01<>&", 5000)
	if err := sh.Input(data); err != nil {
		t.Fatalf("Input: %v", err)
	}

	msgs := drain(f)
	var got strings.Builder
	for _, m := range msgs {
		if len(m) > maxMessageBytes {
			t.Errorf("message of %d bytes exceeds the %d-byte limit", len(m), maxMessageBytes)
		}
		got.WriteString(inputData(t, m))
	}
	if got.String() != data {
		t.Fatal("reassembled input differs from what was sent")
	}
}

func TestInputNeverSplitsARune(t *testing.T) {
	sh, f := newTestShell()
	f.sent = make(chan string, 4096)
	sh.sock = f

	data := strings.Repeat("è€😀", 4000)
	if err := sh.Input(data); err != nil {
		t.Fatalf("Input: %v", err)
	}

	msgs := drain(f)
	var got strings.Builder
	for _, m := range msgs {
		d := inputData(t, m)
		if d == "" {
			t.Fatal("empty chunk")
		}
		if !utf8.ValidString(d) {
			t.Fatalf("chunk %q is not valid UTF-8 — a rune was split", d)
		}
		got.WriteString(d)
	}
	if got.String() != data {
		t.Fatal("reassembled input differs from what was sent")
	}
}
