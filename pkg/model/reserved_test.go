package model

import (
	"slices"
	"sort"
	"testing"
)

// TestIsReservedSlug pins the vocabulary itself: the two words are route
// literals in the works, people and series id namespaces, and nothing else is.
func TestIsReservedSlug(t *testing.T) {
	for _, word := range ReservedSlugs() {
		if !IsReservedSlug(word) {
			t.Errorf("IsReservedSlug(%q) = false, but it is in ReservedSlugs()", word)
		}
	}
	for _, ok := range []string{"", "searching", "search-2", "latest-releases", "the-search", "chapters", "recordings"} {
		if IsReservedSlug(ok) {
			t.Errorf("IsReservedSlug(%q) = true; only whole route literals are reserved", ok)
		}
	}
	if got := ReservedSlugs(); !sort.StringsAreSorted(got) {
		t.Errorf("ReservedSlugs() = %v, want sorted", got)
	}
}

// TestReservedSlugsAreValidSlugs keeps the vocabulary honest: a reserved word
// that is not a valid slug could never have collided with a record id in the
// first place, so it would be reserving nothing.
func TestReservedSlugsAreValidSlugs(t *testing.T) {
	for _, word := range ReservedSlugs() {
		if !ValidSlug(word) {
			t.Errorf("reserved word %q is not a valid slug, so no record could ever be stored under it", word)
		}
	}
}

// TestPersonSlugStepsOffAReservedWord is the person half of the reserved rule.
// A person really named "Search" has to satisfy two rules at once - their id IS
// the slug of their name, and no id is a route literal - and this is the single
// variant that satisfies both. Minting (internal/importer, internal/issueform)
// and verification (pkg/check's checkPersonSlug) both read it from here, so the
// answer cannot fork.
func TestPersonSlugStepsOffAReservedWord(t *testing.T) {
	cases := []struct {
		name     string
		want     string
		fellBack bool
	}{
		{"Search", "search-person", false},
		{"SEARCH", "search-person", false},
		{"Latest", "latest-person", false},
		// Only the WHOLE slug is reserved: a name that merely contains the word
		// is untouched.
		{"Search Party", "search-party", false},
		{"Searching", "searching", false},
		{"Ray Porter", "ray-porter", false},
		// The catch-all is unaffected, and is not itself a reserved word.
		{"김영하", UnslugPersonID, true},
	}
	for _, tc := range cases {
		got, fellBack := PersonSlug(tc.name)
		if got != tc.want || fellBack != tc.fellBack {
			t.Errorf("PersonSlug(%q) = (%q, %v), want (%q, %v)", tc.name, got, fellBack, tc.want, tc.fellBack)
		}
		if IsReservedSlug(got) {
			t.Errorf("PersonSlug(%q) = %q, which is a reserved slug", tc.name, got)
		}
		if !ValidSlug(got) {
			t.Errorf("PersonSlug(%q) = %q, which is not a valid slug", tc.name, got)
		}
	}
}

// TestReservedPersonSlugIsIdempotentlyLegal pins the property the one-shot
// application relies on: the stepped-off form is never itself reserved, so
// PersonSlug never has to step twice.
func TestReservedPersonSlugIsIdempotentlyLegal(t *testing.T) {
	for _, word := range ReservedSlugs() {
		stepped := ReservedPersonSlug(word)
		if IsReservedSlug(stepped) {
			t.Errorf("ReservedPersonSlug(%q) = %q, which is reserved too", word, stepped)
		}
		if slices.Contains(ReservedSlugs(), stepped) {
			t.Errorf("ReservedPersonSlug(%q) = %q collides with the vocabulary", word, stepped)
		}
		if len(stepped) > MaxSlugLen {
			t.Errorf("ReservedPersonSlug(%q) = %q is over MaxSlugLen", word, stepped)
		}
	}
}
