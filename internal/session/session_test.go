package session

import "testing"

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
