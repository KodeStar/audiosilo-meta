package model

import "strings"

// PrimarySubtag is a BCP-47 tag's language alone, lower-cased and without its
// script or region ("en-GB" -> "en"). Two records are in one language iff their
// primary subtags agree, which is the comparison every language rule makes.
func PrimarySubtag(tag string) string {
	lang, _, _ := strings.Cut(strings.ToLower(tag), "-")
	return lang
}

// SameLanguage reports whether two language tags are KNOWN to be one language:
// both state one and their primary subtags agree. An unknown side ("" - a
// series tie, a work stating nothing) is never the same language as anything,
// which is the one spelling of "a language rule stands down on an unknown".
func SameLanguage(a, b string) bool {
	pa := PrimarySubtag(a)
	return pa != "" && pa == PrimarySubtag(b)
}

// SeriesLanguage is a series' language, DERIVED from its member list: the
// STRICT majority of the members' primary subtags - the one language more
// members state than state any other (a strict plurality: 2 en, 1 fr and 1 de
// is "en", though not over half) - or "" when no member's language is known or
// the leading languages tie (a 1-1 split has no majority, and a guess would be a
// fact nobody stated). A series has no language field
// of its own, so this is the one definition every reader shares - pkg/check's
// link rules, internal/repair's plan-time link judgement, internal/issueform's
// link corrections and internal/audit's series index.
//
// langOf answers a work id's language tag, "" when the work is unknown or
// states none. Every membership counts, so a work listed at two positions
// counts twice - the series' own statement of what it holds.
func SeriesLanguage(works []SeriesWork, langOf func(workID string) string) string {
	counts := map[string]int{}
	for _, sw := range works {
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

// SeriesLanguageOf is SeriesLanguage over a catalogue held as an id map, the
// shape every whole-catalogue reader has: a member the map does not hold is
// unknown.
func SeriesLanguageOf(works []SeriesWork, byID map[string]*Work) string {
	return SeriesLanguage(works, func(id string) string {
		if w := byID[id]; w != nil {
			return w.Language
		}
		return ""
	})
}
