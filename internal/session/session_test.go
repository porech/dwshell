package session

import (
	"strconv"
	"testing"
)

// resultFor decodes the frame for id from body (test helper mirroring Execute).
func resultFor(t *testing.T, body, id string) ([]byte, error) {
	t.Helper()
	frames, err := parseFrames(body)
	if err != nil {
		return nil, err
	}
	f, ok := frames[id]
	return f.result(ok)
}

func TestParseFramesSingle(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    string
		wantErr bool
	}{
		{"null payload", "K:K0:6:K:null", "null", false},
		{"json payload", `K:K0:9:K:{"a":1}`, `{"a":1}`, false},
		{"empty success", "K:K0:2:K:", "", false},
		{"command error", "K:K0:12:E:some error", "", true},
		{"server error", "E:boom", "", true},
		{"disconnected", "D:#SessionExpired", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resultFor(t, tc.body, "K0")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseFramesBatch(t *testing.T) {
	// Two commands in one response (K:no = 4 bytes, K:yes = 5 bytes).
	body := `K:K0:4:K:noK1:5:K:yes`
	frames, err := parseFrames(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 2 {
		t.Fatalf("got %d frames", len(frames))
	}
	if d, err := frames["K0"].result(true); err != nil || string(d) != "no" {
		t.Fatalf("K0 = %q err=%v", d, err)
	}
	if d, err := frames["K1"].result(true); err != nil || string(d) != "yes" {
		t.Fatalf("K1 = %q err=%v", d, err)
	}
}

func TestFrameMissing(t *testing.T) {
	if _, err := (frame{}).result(false); err == nil {
		t.Fatal("missing frame should error")
	}
}

// The framed length counts characters, not bytes: the service is counting the
// way JavaScript and Java do. Slicing the body by bytes truncates any payload
// containing non-ASCII — a localized error message, an accented file name —
// and leaves the caller with a payload cut mid-character.
func TestParseFramesLengthCountsCharactersNotBytes(t *testing.T) {
	payload := `K:{"message":"L'agente 'x' già esiste.","status":"error"}`
	body := "K:K1:" + strconv.Itoa(len([]rune(payload))) + ":" + payload

	frames, err := parseFrames(body)
	if err != nil {
		t.Fatalf("parseFrames: %v", err)
	}
	f, ok := frames["K1"]
	if !ok {
		t.Fatal("frame K1 missing")
	}
	data, err := f.result(true)
	if err != nil {
		t.Fatalf("result: %v", err)
	}
	want := `{"message":"L'agente 'x' già esiste.","status":"error"}`
	if string(data) != want {
		t.Fatalf("payload truncated:\n got %q\nwant %q", data, want)
	}
}

// Beyond the basic plane a character is two UTF-16 units, which is what the
// service counts — counting Go runes there would trade one off-by-one for
// another.
func TestParseFramesCountsUTF16Units(t *testing.T) {
	payload := `K:{"n":"😀"}` // the emoji is one rune but two UTF-16 units
	body := "K:K1:" + strconv.Itoa(utf16Len(payload)) + ":" + payload

	frames, err := parseFrames(body)
	if err != nil {
		t.Fatalf("parseFrames: %v", err)
	}
	data, err := frames["K1"].result(true)
	if err != nil {
		t.Fatalf("result: %v", err)
	}
	if string(data) != `{"n":"😀"}` {
		t.Fatalf("got %q", data)
	}
}
