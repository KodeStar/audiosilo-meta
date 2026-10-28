package model

import (
	"strings"
	"testing"
)

// TestBoundedSlugTail pins the three regimes: the join fits, the base is cut at
// a hyphen boundary, the base is hard-cut because no boundary leaves room. The
// tail must come through intact in all of them.
func TestBoundedSlugTail(t *testing.T) {
	hyphenated := strings.Repeat("word-", 19) + "last" // 99 chars
	cases := []struct {
		name, base, tail, want string
	}{
		{"fits", "short-title", "-2020", "short-title-2020"},
		{"word boundary cut", hyphenated, "-2020", strings.Repeat("word-", 18) + "word-2020"},
		{"hard cut, no boundary", strings.Repeat("a", MaxSlugLen), "-2020", strings.Repeat("a", 95) + "-2020"},
		// Defensive regime: no caller composes a tail this long (the longest is an
		// author credit plus a numeric suffix), but the END of the tail - where the
		// distinguishing suffix lives - must still survive it.
		{"tail alone fills the cap", "a-title", "-" + strings.Repeat("bb-", 40) + "cast-7", "bb-" + strings.Repeat("bb-", 30) + "cast-7"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := BoundedSlugTail(c.base, c.tail)
			if got != c.want {
				t.Errorf("BoundedSlugTail(%q, %q) = %q, want %q", c.base, c.tail, got, c.want)
			}
			if !ValidSlug(got) {
				t.Errorf("result %q (%d chars) is not a valid slug", got, len(got))
			}
			if !strings.HasSuffix(got, "7") {
				return // only the long-tail case carries a distinguishing suffix
			}
			if !strings.HasSuffix(got, "cast-7") {
				t.Errorf("result %q dropped the tail's distinguishing suffix", got)
			}
		})
	}
}
