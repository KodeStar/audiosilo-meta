package serve

import (
	"strings"
	"unicode/utf8"
)

// The commons of the query-side boosts. Two probes resolve works a query names
// outright ahead of the FTS page - the exact-title probe (exacttitle.go) and
// the series-position probe (seriespos.go) - and both are built from the same
// substrate: one token normalization for the vocabulary tables, one stopword
// vocabulary, one cost gate, one whole-name comparison key. This file is that
// substrate, kept beside neither probe so a third boost extends the shared
// layer rather than reaching into a sibling's file. What is specific to one
// probe (the volume words, the position grammar, preferWholeName) stays in that
// probe's file.

// keywordKey normalizes a token for the vocabulary tables: lowercased, with a
// trailing "." dropped ("Vol." -> "vol").
func keywordKey(tok string) string {
	return strings.ToLower(strings.TrimSuffix(tok, "."))
}

// probeStopwords are the words that carry no identity on their own. A query (or
// residual) made only of these or of one-letter tokens is never probed: matching
// "the" against 30k series names costs a whole-match-set bm25 sort and returns
// an arbitrary three of them, and the exact-title probe reads the same table for
// the same reason at a larger scale - "the" is in 88,458 work titles.
var probeStopwords = map[string]bool{
	"the": true, "a": true, "an": true, "of": true, "and": true, "in": true, "to": true,
	"der": true, "die": true, "das": true, "den": true, "dem": true, "ein": true, "eine": true,
	"le": true, "la": true, "les": true, "un": true, "une": true, "du": true, "des": true,
	"el": true, "los": true, "las": true, "il": true, "lo": true, "i": true, "y": true, "e": true,
}

// hasIdentifyingToken is the shared cost gate behind worthProbing and
// worthTitleProbing: s must hold one term that is at least two runes long and
// in neither junk vocabulary. The two probes differ only in WHICH vocabularies
// disqualify a term - the series probe passes the volume words as junkB, the
// title probe passes nil (a nil map reads as empty) - so the rule itself is
// written once.
//
// It judges ftsTerms, not whitespace tokens, because those terms are what the
// MATCH will actually walk: "the-a" is one five-rune whitespace token but two
// stopword phrases, exactly the probe this gate exists to refuse.
func hasIdentifyingToken(s string, junkA, junkB map[string]bool) bool {
	for _, term := range ftsTerms(s) {
		k := keywordKey(term)
		if utf8.RuneCountInString(k) < 2 || junkA[k] || junkB[k] {
			continue
		}
		return true
	}
	return false
}

// nameKey normalizes a name (a series name, a residual, or - for the
// exact-title probe - a work title and the query itself) for whole-name
// comparison: the lowercased ftsTerms, space-joined. Sharing the retrieval
// side's rule is what makes the comparison symmetric with it - a query the
// index matched on its words is compared on the same words, so "Spider-Man" and
// "Spider Man", or "Halo:Primordium" and "Halo: Primordium", are one name.
func nameKey(s string) string {
	return strings.Join(ftsTerms(strings.ToLower(s)), " ")
}
