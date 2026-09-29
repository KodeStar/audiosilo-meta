package titlerule

import (
	"strings"
	"testing"
)

// SameSeriesName is the writers' one test for "these two names are one series":
// case is never identity, and neither is the bracket style or the spacing around a
// bracketed decoration - but the decoration's words, and everything outside the
// groups, still are.
func TestSameSeriesName(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		same bool
	}{
		// The long-standing rule: case only.
		{a: "Warhammer 40,000", b: "warhammer 40,000", same: true},
		{a: "Mémoires", b: "mémoires", same: true},
		{a: "Warhammer 40,000", b: "Horus Heresy"},
		{a: "Warhammer 40,000", b: "Warhammer 40K"},
		// A name with no addressable slug is no series at all.
		{a: "。。。", b: "。。。"},

		// The respells Audible makes under an unchanged series ASIN (#2441).
		{a: "NOMADS Legacy [German Edition]", b: "NOMADS Legacy (German Edition)", same: true},
		{a: "Throne of Glass[French Edition]", b: "Throne of Glass [French Edition]", same: true},
		{a: "Throne of Glass (French Edition)", b: "throne of glass [FRENCH EDITION]", same: true},
		{a: "X ( German Edition )", b: "X [German Edition]", same: true},
		{a: "X (A) (B)", b: "X [A][B]", same: true},
		{a: "Saga (Book 1) Collection", b: "Saga [Book 1]Collection", same: true},

		// A different decoration is a different series name.
		{a: "NOMADS Legacy [German Edition]", b: "NOMADS Legacy [French Edition]"},
		{a: "Vorkosigan Saga (Published Order)", b: "Vorkosigan Saga"},
		{a: "Vorkosigan Saga (Published Order)", b: "Vorkosigan Saga (Chronological Order)"},
		{a: "X (A) (B)", b: "X (B) (A)"},
		{a: "X (Books 1-2)", b: "X (Books 12)"},
		// Only spacing that TOUCHES a bracket is forgiven; punctuation outside the
		// groups stays identity even where the slug agrees.
		{a: "Mr. X (A)", b: "Mr X (A)"},
		{a: "X - (A)", b: "X (A)"},
		{a: "Big  Name (A)", b: "Big Name (A)"},
		// An undecorated pair that slugs alike but spells differently stays apart.
		{a: "Mr. X", b: "Mr X"},

		// A decoration DecorationKey cannot read whole never widens anything: only an
		// exact case-insensitive match agrees.
		{a: "Series [Книга 1]", b: "Series (Книга 1)"},
		{a: "Series [Книга 1]", b: "series [книга 1]", same: true},
		{a: "Series (Книга 1)", b: "Series (Том 1)"},
		// A name that is nothing but its groups keeps the exact rule.
		{a: "(Foo)", b: "[Foo]"},
		// An unclosed group is no group.
		{a: "X (German Edition", b: "X [German Edition"},
	} {
		if got := SameSeriesName(tc.a, tc.b); got != tc.same {
			t.Errorf("SameSeriesName(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.same)
		}
		if got := SameSeriesName(tc.b, tc.a); got != tc.same {
			t.Errorf("SameSeriesName(%q, %q) = %v, want %v (symmetry)", tc.b, tc.a, got, tc.same)
		}
	}
}

// The key is exactly as wide as strings.EqualFold on names that carry no readable
// decoration, so grouping by it and walking the chain with SameSeriesName can never
// disagree, and no pair EqualFold called equal is ever split.
func TestSeriesNameKeyContainsEqualFold(t *testing.T) {
	names := []string{
		"Warhammer", "WARHAMMER", "warhammer", "Kelvin", "Kelvin", "kelvin",
		"Straſe", "STRASE", "strase", "Ωmega", "ωMEGA", "İstanbul", "istanbul",
		"Series (Book 1)", "series (BOOK 1)", "Series [Книга 1]", "SERIES [КНИГА 1]",
		"Émile", "émile", "ÉMILE",
	}
	for _, a := range names {
		for _, b := range names {
			if strings.EqualFold(a, b) && SeriesNameKey(a) != SeriesNameKey(b) {
				t.Errorf("EqualFold(%q, %q) but keys %q / %q differ", a, b, SeriesNameKey(a), SeriesNameKey(b))
			}
			if !strings.ContainsAny(a+b, "([") && (SeriesNameKey(a) == SeriesNameKey(b)) != strings.EqualFold(a, b) {
				t.Errorf("undecorated %q / %q: key equality %v, EqualFold %v",
					a, b, SeriesNameKey(a) == SeriesNameKey(b), strings.EqualFold(a, b))
			}
		}
	}
}
