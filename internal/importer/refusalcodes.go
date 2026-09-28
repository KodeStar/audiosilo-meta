package importer

import "strings"

// refusalcodes.go is the ONE table of the rules a libex row can be refused by:
// each rule's machine-readable CODE and the wording the libex-select report
// counts it under. The codes are what `libex-select --refusals` and
// `metaimport libex --skipped` write, one {"asin":"<ASIN>","reason":"<code>"}
// line per refused row.
//
// THE CODES ARE A CONTRACT with audiosilo-meta-sync, the series-completion bot,
// which reads those files to decide what a refused row means for its queue. A
// code is never renamed and never reused for a different rule; a new rule gets a
// new code, added here in the same change (and the pin in audiosilo-meta-sync
// bumped). TestRefusalCodesAreStable pins the exact list. The report wording is
// kept whole in the bot's pull request body, so it is not reworded casually
// either.
//
// A rule is a refusal VALUE, never a bare string: every function that refuses a
// row returns one, so a refusal without a code cannot be written.

// refusal is one rule: its contract code and its report wording. The zero value
// is "not refused".
type refusal struct{ code, report string }

// The contract codes.
const (
	RefusalMalformedASIN           = "malformed-asin"
	RefusalASINInCatalogue         = "asin-in-catalogue"
	RefusalDuplicateASIN           = "duplicate-asin"
	RefusalNoCatalogueSeries       = "no-catalogue-series"
	RefusalSeriesOtherAuthors      = "series-other-authors"
	RefusalExtraSeriesUncatalogued = "extra-series-uncatalogued"
	RefusalPositionUnparseable     = "position-unparseable"
	RefusalUnmappedLanguage        = "unmapped-language"
	RefusalUnmappedRegion          = "unmapped-region"
	RefusalAINarrator              = "ai-narrator"
	RefusalCreditPlatformAccount   = "credit-platform-account"
	RefusalCreditListOfPeople      = "credit-list-of-people"
	RefusalCreditCastPlaceholder   = "credit-cast-placeholder"
	RefusalCreditNotAPerson        = "credit-not-a-person"
	RefusalPositionClaimed         = "position-claimed"
	RefusalOverSeriesCap           = "over-series-cap"
	// The IMPORT-side codes: rows `metaimport libex` itself skips, which only
	// its --skipped worklist carries (libex-select never refuses for these).
	RefusalIdentityDuplicate = "identity-duplicate"
	RefusalMissingAuthor     = "missing-author"
	RefusalMissingTitle      = "missing-title"
	RefusalMissingNarrator   = "missing-narrator"
)

// The rules, in the order selectLibexRow applies them. A row is counted under
// the FIRST rule it fails, so the exclusion counts partition the rows read.
var (
	reasonNoASIN        = refusal{RefusalMalformedASIN, "malformed or missing ASIN"}
	reasonAlreadyASIN   = refusal{RefusalASINInCatalogue, "ASIN already in the catalogue"}
	reasonDuplicateASIN = refusal{RefusalDuplicateASIN, "duplicate ASIN within the export"}
	reasonNoSeries      = refusal{RefusalNoCatalogueSeries, "no catalogue series"}
	reasonSeriesAuthors = refusal{RefusalSeriesOtherAuthors, "catalogue series belongs to other authors"}
	// reasonOtherSeriesUncatalogued is the second half of "never a new series":
	// the row DOES complete a catalogued series, but another of its claims would
	// mint one (see selectLibexRow).
	reasonOtherSeriesUncatalogued = refusal{RefusalExtraSeriesUncatalogued, "another claimed series is not in the catalogue"}
	reasonNoPosition              = refusal{RefusalPositionUnparseable, "series position missing or unparseable"}
	reasonLanguage                = refusal{RefusalUnmappedLanguage, "unmapped language"}
	reasonRegion                  = refusal{RefusalUnmappedRegion, "unmapped region"}
	reasonAINarrator              = refusal{RefusalAINarrator, "narrated by an AI voice"}
	reasonJunkCredit              = refusal{RefusalCreditPlatformAccount, "a credited name is a platform account"}
	reasonListCredit              = refusal{RefusalCreditListOfPeople, "a credited name is a list of people"}
	reasonPlaceholder             = refusal{RefusalCreditCastPlaceholder, "a credited name is a cast placeholder"}
	reasonUnnamedCredit           = refusal{RefusalCreditNotAPerson, "a credited name does not identify a person"}
	reasonPositionTaken           = refusal{RefusalPositionClaimed, "series position already claimed"}
	reasonSeriesCap               = refusal{RefusalOverSeriesCap, "over the per-series cap"}

	// Import-only rules (Summary.Skips): the create path's duplicate-identity
	// guard (dupidentity.go) and the admission tests a row with no author, no
	// title or no narrator fails (addBook, admitRecordingFacts).
	reasonIdentityDuplicate = refusal{RefusalIdentityDuplicate, "the catalogue holds the book under another title"}
	reasonMissingAuthor     = refusal{RefusalMissingAuthor, "no author"}
	reasonMissingTitle      = refusal{RefusalMissingTitle, "no title"}
	reasonMissingNarrator   = refusal{RefusalMissingNarrator, "no narrator"}
)

// refusals is every SELECTOR rule in report order (the order libex-select
// applies them): the libex-select report prints one line per entry, so an
// import-only rule is never added here.
var refusals = []refusal{
	reasonNoASIN, reasonAlreadyASIN, reasonDuplicateASIN,
	reasonNoSeries, reasonSeriesAuthors, reasonOtherSeriesUncatalogued,
	reasonNoPosition, reasonLanguage, reasonRegion,
	reasonAINarrator, reasonJunkCredit, reasonListCredit, reasonPlaceholder, reasonUnnamedCredit,
	reasonPositionTaken, reasonSeriesCap,
}

// importRefusals are the rules only the import applies (--skipped).
var importRefusals = []refusal{
	reasonIdentityDuplicate, reasonMissingAuthor, reasonMissingTitle, reasonMissingNarrator,
}

// RefusalCodes lists every code a worklist line can carry: the selector's rules
// in report order, then the import's own.
func RefusalCodes() []string {
	out := make([]string, 0, len(refusals)+len(importRefusals))
	for _, r := range append(append([]refusal{}, refusals...), importRefusals...) {
		out = append(out, r.code)
	}
	return out
}

// RowSkip is one refused row as a worklist states it: the row's ASIN and its
// rule's code. Its JSON form, in this field order, is one line of
// `libex-select --refusals` and of `metaimport libex --skipped` - one shape, a
// contract with the series-completion sync bot.
type RowSkip struct {
	ASIN   string `json:"asin"`
	Reason string `json:"reason"`
}

// appendSkip records one refused row under its rule, unless the row states no
// ASIN to name it by.
func appendSkip(skips []RowSkip, asin string, r refusal) []RowSkip {
	if asin == "" {
		return skips
	}
	return append(skips, RowSkip{ASIN: asin, Reason: r.code})
}

// dropRecorded is skips without every entry whose ASIN recorded reports: a
// refused row whose ASIN landed anyway (a sibling row of the same ASIN, or the
// other copy a selection kept) is no refusal worth memoizing.
func dropRecorded(skips []RowSkip, recorded func(asin string) bool) []RowSkip {
	out := skips[:0:0]
	for _, s := range skips {
		if !recorded(s.ASIN) {
			out = append(out, s)
		}
	}
	return out
}

// refusedRowASIN names a refused row by the ASIN it states: normalized when it
// is one, verbatim (trimmed) when it is malformed, so a worklist line still
// points at the row it came from; "" when it states none.
func refusedRowASIN(stated string) string {
	if asin := NormalizeASIN(stated); asin != "" {
		return asin
	}
	return strings.TrimSpace(stated)
}
