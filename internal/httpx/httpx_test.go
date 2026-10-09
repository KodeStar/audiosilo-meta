package httpx

import "testing"

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
