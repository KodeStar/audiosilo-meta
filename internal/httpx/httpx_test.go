package httpx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestFailLogsUnlessTheErrorIsTheRequestsCancellation pins which 500s reach the
// log. Only the request context's OWN error is silent - the abandoned keystroke
// search - and a real failure is logged even when the client has gone, which is
// what a read on a context of its own (the card batches) depends on. Every case
// answers the same fixed body.
func TestFailLogsUnlessTheErrorIsTheRequestsCancellation(t *testing.T) {
	dbErr := errors.New("database disk image is malformed")
	cancelled := func() context.Context {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return ctx
	}
	expired := func() context.Context {
		ctx, cancel := context.WithDeadline(context.Background(), time.Unix(0, 0))
		t.Cleanup(cancel)
		return ctx
	}
	cases := []struct {
		name   string
		ctx    context.Context
		err    error
		logged bool
	}{
		{"live request, real error", context.Background(), dbErr, true},
		{"live request, a context error of the handler's own", context.Background(), context.Canceled, true},
		{"client gone, the request's cancellation", cancelled(), context.Canceled, false},
		{"client gone, the cancellation wrapped", cancelled(), fmt.Errorf("cards: %w", context.Canceled), false},
		{"request deadline, its own deadline error", expired(), context.DeadlineExceeded, false},
		{"client gone, a real database error", cancelled(), dbErr, true},
		{"client gone, the handler's own budget expired", cancelled(), context.DeadlineExceeded, true},
	}
	for _, tc := range cases {
		var buf bytes.Buffer
		r := httptest.NewRequestWithContext(tc.ctx, http.MethodGet, "/api/v1/works/x", nil)
		w := httptest.NewRecorder()
		Fail(w, r, log.New(&buf, "", 0), "query", tc.err)
		if got := buf.Len() > 0; got != tc.logged {
			t.Errorf("%s: logged = %v (%q), want %v", tc.name, got, buf.String(), tc.logged)
		}
		if tc.logged && !strings.Contains(buf.String(), tc.err.Error()) {
			t.Errorf("%s: log %q does not carry the error", tc.name, buf.String())
		}
		if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), InternalErrMsg) {
			t.Errorf("%s: answered %d %q, want the fixed 500", tc.name, w.Code, w.Body.String())
		}
	}
}

// TestMatchesETag covers the If-None-Match forms RFC 9110 allows, since a wrong
// answer either serves a 304 for a document the client does not have or never
// serves one at all. "*" is NOT one of them: it names no validator, so whether
// it matches depends on the resource having a representation - see
// AnyValidator, and the entity handler that answers that question.
func TestMatchesETag(t *testing.T) {
	const tag = `"abc"`
	cases := []struct {
		header string
		want   bool
	}{
		{"", false},
		{tag, true},
		{`W/"abc"`, true},
		{`"other", "abc"`, true},
		{`"other"`, false},
		{"*", false},
		{`"ab"`, false},
	}
	for _, tc := range cases {
		if got := MatchesETag(tc.header, tag); got != tc.want {
			t.Errorf("MatchesETag(%q, %q) = %v, want %v", tc.header, tag, got, tc.want)
		}
	}
}

// TestAnyValidator pins the wildcard's own reading, in every list position it
// can arrive in - a header that carries it means "304 if this resource has any
// current representation", which is a question its callers answer.
func TestAnyValidator(t *testing.T) {
	cases := map[string]bool{
		"":              false,
		"*":             true,
		" * ":           true,
		`"abc", *`:      true,
		`*, "abc"`:      true,
		`"abc"`:         false,
		`W/"*"`:         false, // a validator whose opaque value is an asterisk
		`"a*b"`:         false,
		`"abc", W/"de"`: false,
	}
	for header, want := range cases {
		if got := AnyValidator(header); got != want {
			t.Errorf("AnyValidator(%q) = %v, want %v", header, got, want)
		}
	}
}

func TestNoArtifactIsTemporaryAndNeverStored(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteNoArtifact(rec, 1500*time.Millisecond)
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "2" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status %d headers %v", rec.Code, rec.Header())
	}
	if !strings.Contains(rec.Body.String(), noArtifactMsg) {
		t.Error("missing shared error message")
	}
}
