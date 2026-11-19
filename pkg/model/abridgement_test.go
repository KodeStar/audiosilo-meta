package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRecordingAbridgementRoundTrip(t *testing.T) {
	for _, tt := range []struct{ name, flag, want string }{
		{"absent", "", ""}, {"false", `,"abridged":false`, "unabridged"}, {"true", `,"abridged":true`, "abridged"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			input := `{"id":"narration","work":"book","language":"en","narrators":["reader"],"runtime_min":123` + tt.flag + `}`
			var recording Recording
			if err := json.Unmarshal([]byte(input), &recording); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if recording.Abridgement() != tt.want {
					t.Fatalf("abridgement = %q, want %q", recording.Abridgement(), tt.want)
				}
				b, err := json.Marshal(recording)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(b), `"abridged"`) != (tt.flag != "") {
					t.Fatalf("flag presence lost: %s", b)
				}
				if err := json.Unmarshal(b, &recording); err != nil {
					t.Fatal(err)
				}
				if recording.RuntimeMin != 123 || len(recording.Narrators) != 1 {
					t.Fatalf("other fields lost: %+v", recording)
				}
			}
		})
	}
	if got := (Recording{Abridged: true}).Abridgement(); got != "abridged" {
		t.Fatalf("legacy true = %q", got)
	}
	if got := (Recording{Abridged: false}).Abridgement(); got != "" {
		t.Fatalf("legacy false = %q", got)
	}
}
