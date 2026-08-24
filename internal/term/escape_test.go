package term

import "testing"

func TestEscapeScanner(t *testing.T) {
	// Each step feeds bytes and checks the forwarded output + disconnect flag,
	// carrying scanner state across steps.
	type step struct {
		in         string
		wantOut    string
		wantDiscon bool
	}
	tests := []struct {
		name  string
		steps []step
	}{
		{"plain command", []step{{"ls -l\r", "ls -l\r", false}}},
		{"disconnect at start", []step{{"~.", "", true}}},
		{"disconnect after newline", []step{{"echo hi\r", "echo hi\r", false}, {"~.", "", true}}},
		{"literal tilde ~~", []step{{"~~", "~", false}}},
		{"literal tilde then text", []step{{"~~ls\r", "~ls\r", false}}},
		{"tilde then other char", []step{{"~x", "~x", false}}},
		{"tilde not at line start", []step{{"a~.", "a~.", false}}},
		{"escape split across feeds", []step{{"~", "", false}, {".", "", true}}},
		{"tilde mid-line is literal", []step{{"foo~bar\r", "foo~bar\r", false}}},
		{"flush before disconnect", []step{{"abc\r~.", "abc\r", true}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newEscapeScanner()
			for i, s := range tc.steps {
				out, disc := e.feed([]byte(s.in))
				if string(out) != s.wantOut || disc != s.wantDiscon {
					t.Errorf("step %d feed(%q) = (%q,%v), want (%q,%v)", i, s.in, out, disc, s.wantOut, s.wantDiscon)
				}
			}
		})
	}
}
