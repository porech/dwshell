package session

import "testing"

func TestParseCommandResponse(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    string
		wantErr bool
	}{
		{"null payload", "K:K1:6:K:null", "null", false},
		{"json payload", `K:K1:9:K:{"a":1}`, `{"a":1}`, false},
		{"empty success", "K:K1:2:K:", "", false},
		{"command error", "K:K1:12:E:some error", "", true},
		{"server error", "E:boom", "", true},
		{"disconnected", "D:#SessionExpired", "", true},
		{"retry node", "B:elsewhere", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseCommandResponse(tc.body, "K1")
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

func TestParseCommandResponseSelectsID(t *testing.T) {
	// Two frames (K:no = 4 bytes, K:yes = 5 bytes); we asked for K2.
	body := `K:K1:4:K:noK2:5:K:yes`
	got, err := parseCommandResponse(body, "K2")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "yes" {
		t.Fatalf("got %q, want %q", got, "yes")
	}
}
