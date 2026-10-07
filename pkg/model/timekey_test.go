package model

import "testing"

func TestTimeKey(t *testing.T) {
	for in, want := range map[string]string{
		"2026-07-29":                "2026-07-29", // a plain date is its own key (the fast path)
		"2026":                      "2026",
		"":                          "",
		"2026-07-29T02:00:00+05:00": "2026-07-28T21:00:00Z",
		"2026-07-29T01:00:00Z":      "2026-07-29T01:00:00Z",
		"not a date at all":         "not a date at all",
	} {
		if got := TimeKey(in); got != want {
			t.Errorf("TimeKey(%q) = %q, want %q", in, got, want)
		}
	}
	// The ordering it exists for: the later instant sorts later, and a date
	// sorts before every timestamp on its day.
	if TimeKey("2026-07-29T02:00:00+05:00") >= TimeKey("2026-07-29T01:00:00Z") || TimeKey("2026-07-29") >= TimeKey("2026-07-29T00:00:00Z") {
		t.Errorf("TimeKey does not order chronologically")
	}
}
