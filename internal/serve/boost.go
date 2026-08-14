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
// worthTitleProbing: s must hold one token that is at least two runes long and
// in neither junk vocabulary. The two probes differ only in WHICH vocabularies
// disqualify a token - the series probe passes the volume words as junkB, the
// title probe passes nil (a nil map reads as empty) - so the rule itself is
// written once.
//
// The length minimum is measured on the WHITESPACE token, not on its terms. An
// initialism is many one-rune terms ("N.E.R.D.S.", "Q&A", "A.D.", "3 a.m.") and
// judging terms refused every one of them - yet tokenPhrases turns such a token
// into a single highly selective phrase, and those probes measured at 0.9-2.4ms
// against the 19.7ms of "book", which this gate admits. There is no cost case for
// refusing them, and refusing them cost both boosts for the whole class.
//
// What the term rule buys is kept as a SECOND, cheap refusal: a token whose terms
// are all junk is junk however long the token is, so "the-a" is refused (both
// terms are stopwords) and so is a token with no term at all ("!!").
func hasIdentifyingToken(s string, junkA, junkB map[string]bool) bool {
	for _, tok := range strings.Fields(s) {
		k := keywordKey(tok)
		if utf8.RuneCountInString(k) < 2 || junkA[k] || junkB[k] {
			continue
		}
		if allJunkTerms(tok, junkA, junkB) {
			continue
		}
		return true
	}
	return false
}

// allJunkTerms reports whether every word inside tok is junk - which includes a
// token holding no word at all. It is what keeps a punctuated pile of stopwords
// ("the-a") out of a probe the token's own length would otherwise admit.
func allJunkTerms(tok string, junkA, junkB map[string]bool) bool {
	terms := ftsTerms(tok)
	for _, term := range terms {
		k := keywordKey(term)
		if !junkA[k] && !junkB[k] {
			return false
		}
	}
	return true
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
