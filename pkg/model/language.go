package model

import (
	"regexp"
	"strings"
)

// languageTagRE is common.schema.json's #/$defs/language pattern, verbatim -
// TestLanguageTagPatternIsTheSchemas reads the embedded schema and fails if the
// two ever differ.
var languageTagRE = regexp.MustCompile(`^[a-z]{2,3}(-[a-z0-9]{2,8})*$`)

// ValidLanguageTag reports whether s is a language tag as the schema spells one
// (common.schema.json #/$defs/language): a lowercase primary subtag of two or
// three letters, then lowercase alphanumeric subtags. It does not fold case, so a
// caller normalizing user input lowercases first. It is the one copy every door
// that judges a typed tag reads, so a form, a filter and a record cannot disagree
// about what a tag is.
func ValidLanguageTag(s string) bool { return languageTagRE.MatchString(s) }

// PrimarySubtag is a BCP-47 tag's language alone, lower-cased and without its
// script or region ("en-GB" -> "en"). Two records are in one language iff their
// primary subtags agree, which is the comparison every language rule makes.
func PrimarySubtag(tag string) string {
	lang, _, _ := strings.Cut(strings.ToLower(tag), "-")
	return lang
}

// languageWords maps a language's English NAME, lowercased, to its ISO 639-1
// code - the one word-to-code table. The importer maps a source's language word
// through it (internal/importer's mapLanguage, where a word that is not here makes
// the book unknown and skipped, so a wrong entry is worse than a missing one), and
// internal/titlerule reads an own-language edition decoration ("(Finnish
// Edition)") through it. Every entry is a language whose 639-1 code is
// unambiguous; counts are books in the full libex dump.
//
// The second block was added after seed wave 5 refused 241 rows purely for want
// of a mapping. The schema accepts any two-letter code, so nothing else had to
// change.
//
// Deliberately NOT mapped, though measured and available:
//
//   - "mandarin_chinese" (440), "simplified_chinese" (4), "traditional_chinese"
//     (3). All three would land on the "zh" that "chinese" already has, and the
//     last two are SCRIPT distinctions rather than languages. Collapsing four
//     source spellings onto one code is a call for a maintainer, not a mapping
//     table entry.
//   - "luo" (1). It has no ISO 639-1 code at all, only 639-3.
//   - "unknown" (643) and the empty value (22,428). Neither is a language.
//   - "ukranian" (6). A misspelling of "ukrainian", and every one of the six
//     rows is outside the importable universe, so the alias would be dead code.
//   - the remaining long tail (tamil, korean, catalan, indonesian, urdu, ...).
//     Each is unambiguous and each is a one-line addition when a wave needs it;
//     they are left out because nothing has asked for them and an unexercised
//     mapping is an untested one.
var languageWords = map[string]string{
	"english":    "en",
	"turkish":    "tr",
	"german":     "de",
	"french":     "fr",
	"spanish":    "es",
	"italian":    "it",
	"japanese":   "ja",
	"portuguese": "pt",
	"dutch":      "nl",
	"polish":     "pl",
	"russian":    "ru",
	"chinese":    "zh",

	"danish":    "da", // 7,342
	"swedish":   "sv", // 4,864
	"arabic":    "ar", // 4,869
	"hindi":     "hi", // 2,305
	"hebrew":    "he", // 968
	"czech":     "cs", // 495
	"hungarian": "hu", // 252
	"finnish":   "fi", // 171
	"norwegian": "no", // 154
	"greek":     "el", // 153

	// The third block, added after the seed's create phase: the three languages
	// the waves refused most rows for (~239 of them, recoverable by a later
	// backfill import of exactly those rows). Each 639-1 code is unambiguous,
	// and the dump spells each language with the one word listed.
	"marathi":   "mr", // 2,186
	"romanian":  "ro", // 591
	"malayalam": "ml", // 456
}

// LanguageWords returns a copy of the word-to-code table (languageWords): each
// language's English name, lowercased, mapped to its ISO 639-1 code.
func LanguageWords() map[string]string {
	out := make(map[string]string, len(languageWords))
	for w, c := range languageWords {
		out[w] = c
	}
	return out
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
// link corrections, internal/audit's series index and internal/build, which
// writes it into the artifact's series.language column for every reader of the
// artifact.
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
