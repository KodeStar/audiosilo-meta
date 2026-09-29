package model

import "strings"

// PrimarySubtag is a BCP-47 tag's language alone, lower-cased and without its
// script or region ("en-GB" -> "en"). Two records are in one language iff their
// primary subtags agree, which is the comparison every language rule makes.
func PrimarySubtag(tag string) string {
	lang, _, _ := strings.Cut(strings.ToLower(tag), "-")
	return lang
}

// SeriesLanguage is a series' language, DERIVED from its members: the STRICT
// majority of their primary subtags, or "" when no member's language is known
// or the leading languages tie (a 1-1 split has no majority, and a guess would
// be a fact nobody stated). A series has no language field of its own, so this
// is the one definition pkg/check, internal/build and internal/audit share.
//
// langOf answers a work id's language tag, "" when the work is unknown or
// states none. Every membership counts, so a work listed at two positions
// counts twice - the series' own statement of what it holds.
func SeriesLanguage(s *Series, langOf func(workID string) string) string {
	if s == nil {
		return ""
	}
	counts := map[string]int{}
	for _, sw := range s.Works {
		if lang := PrimarySubtag(langOf(sw.Work)); lang != "" {
			counts[lang]++
		}
	}
	// The answer does not depend on the map's iteration order: a later, higher
	// count clears a tie, and only a count EQUAL to the running best sets one.
	best, bestN, tied := "", 0, false
	for lang, n := range counts {
		switch {
		case n > bestN:
			best, bestN, tied = lang, n, false
		case n == bestN:
			tied = true
		}
	}
	if tied {
		return ""
	}
	return best
}
