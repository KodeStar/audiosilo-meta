package issueform

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// wireResult marshals r and decodes the two message lists back out; all is
// nil exactly when all_messages was omitted.
func wireResult(t *testing.T, r Result) (messages, all []string) {
	t.Helper()
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var w struct {
		Messages    []string `json:"messages"`
		AllMessages []string `json:"all_messages"`
	}
	if err := json.Unmarshal(data, &w); err != nil {
		t.Fatal(err)
	}
	return w.Messages, w.AllMessages
}

func numbered(n, size int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%04d", i) + strings.Repeat("x", size-4)
	}
	return out
}

// TestWireMessagesUnderTheBoundAreUntouched pins that an ordinary verdict - the
// overwhelming majority - reaches the wire exactly as composed, with no
// all_messages beside it. Exactly maxWireMessages is still under the bound.
func TestWireMessagesUnderTheBoundAreUntouched(t *testing.T) {
	msgs := numbered(maxWireMessages, 40)
	got, all := wireResult(t, Result{Status: StatusOK, Messages: msgs})
	if all != nil {
		t.Error("all_messages emitted for a list the bound did not touch")
	}
	if !slices.Equal(got, msgs) {
		t.Errorf("messages changed on the wire:\n got %v\nwant %v", got, msgs)
	}
}

// TestWireMessagesAreCappedByCount pins the count bound: the head in order, one
// overflow line with the exact remainder, and the complete list beside it.
func TestWireMessagesAreCappedByCount(t *testing.T) {
	msgs := numbered(120, 40)
	got, all := wireResult(t, Result{Status: StatusOK, Messages: msgs})
	if len(got) != maxWireMessages+1 {
		t.Fatalf("len(messages) = %d, want %d plus the overflow line", len(got), maxWireMessages)
	}
	if got[0] != msgs[0] || got[maxWireMessages-1] != msgs[maxWireMessages-1] {
		t.Errorf("the head is not the first %d messages in order: %v", maxWireMessages, got)
	}
	if want := "... and 70 more (full list in the run log)"; got[maxWireMessages] != want {
		t.Errorf("overflow line = %q, want %q", got[maxWireMessages], want)
	}
	if !slices.Equal(all, msgs) {
		t.Errorf("all_messages carries %d of %d messages", len(all), len(msgs))
	}
}

// TestWireMessagesAreCappedByBytes pins the size bound, which is what holds
// when the messages are few but long: the count alone would let fifty 2KB
// messages past a body ceiling of 64KB.
func TestWireMessagesAreCappedByBytes(t *testing.T) {
	msgs := numbered(30, 1900)
	got, all := wireResult(t, Result{Status: StatusOK, Messages: msgs})
	fit := maxWireMessageBytes / 1900
	if len(got) != fit+1 {
		t.Fatalf("len(messages) = %d, want %d plus the overflow line", len(got), fit)
	}
	if want := fmt.Sprintf("... and %d more (full list in the run log)", 30-fit); got[fit] != want {
		t.Errorf("overflow line = %q, want %q", got[fit], want)
	}
	if len(all) != len(msgs) {
		t.Error("all_messages missing although messages were dropped")
	}
}

// TestWireMessageIsCutAtARuneBoundary pins the per-message bound: a message
// echoing a long submission is cut, marked, and still valid UTF-8 - a cut
// through a multi-byte rune would hand GitHub an undecodable body.
func TestWireMessageIsCutAtARuneBoundary(t *testing.T) {
	long := "échec: " + strings.Repeat("é", maxOneMessageBytes)
	got, all := wireResult(t, Result{Status: StatusInvalid, Messages: []string{long, "short"}})
	if len(got) != 2 || got[1] != "short" {
		t.Fatalf("messages = %d, want the cut message and the short one", len(got))
	}
	if len(got[0]) > maxOneMessageBytes || !strings.HasSuffix(got[0], "...") || !utf8.ValidString(got[0]) {
		t.Errorf("cut message: %d bytes, valid UTF-8 %v, suffix %q",
			len(got[0]), utf8.ValidString(got[0]), got[0][len(got[0])-3:])
	}
	if len(all) != 2 || all[0] != long {
		t.Error("all_messages must carry the message uncut")
	}
}
