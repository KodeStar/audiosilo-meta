package importer

// refusalcodes.go is the MACHINE-READABLE twin of libex-select's exclusion
// reasons: the `--refusals <path>` worklist names every refused row's rule by one
// of these codes, one NDJSON line per row ({"asin":"<ASIN>","reason":"<code>"}).
//
// THE CODES ARE A CONTRACT with audiosilo-meta-sync, the series-completion bot,
// which reads that file to decide what a refused row means for its queue. A code
// is never renamed and never reused for a different rule; a new rule gets a new
// code, added here and to RefusalCodes in the same change (and the pin in
// audiosilo-meta-sync bumped). TestRefusalCodesAreStable pins the exact list.
//
// The human-readable reasons (the reason* constants in libexselect.go) are the
// REPORT's wording, which the bot keeps whole for its pull request body; the two
// vocabularies are one table apart (refusalCodeOf) so the report can be reworded
// without touching the contract, and the contract without touching the report.
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
)

// RefusalCodes lists every code a `--refusals` line can carry, in the order the
// selector applies the rules (the report's order).
func RefusalCodes() []string {
	return []string{
		RefusalMalformedASIN, RefusalASINInCatalogue, RefusalDuplicateASIN,
		RefusalNoCatalogueSeries, RefusalSeriesOtherAuthors, RefusalExtraSeriesUncatalogued,
		RefusalPositionUnparseable, RefusalUnmappedLanguage, RefusalUnmappedRegion,
		RefusalAINarrator, RefusalCreditPlatformAccount, RefusalCreditListOfPeople,
		RefusalCreditCastPlaceholder, RefusalCreditNotAPerson,
		RefusalPositionClaimed, RefusalOverSeriesCap,
	}
}

// refusalCodeOf maps each report reason onto its contract code. It is total over
// reasonOrder, one to one (TestEveryReasonHasARefusalCode).
var refusalCodeOf = map[string]string{
	reasonNoASIN:                  RefusalMalformedASIN,
	reasonAlreadyASIN:             RefusalASINInCatalogue,
	reasonDuplicateASIN:           RefusalDuplicateASIN,
	reasonNoSeries:                RefusalNoCatalogueSeries,
	reasonSeriesAuthors:           RefusalSeriesOtherAuthors,
	reasonOtherSeriesUncatalogued: RefusalExtraSeriesUncatalogued,
	reasonNoPosition:              RefusalPositionUnparseable,
	reasonLanguage:                RefusalUnmappedLanguage,
	reasonRegion:                  RefusalUnmappedRegion,
	reasonAINarrator:              RefusalAINarrator,
	reasonJunkCredit:              RefusalCreditPlatformAccount,
	reasonListCredit:              RefusalCreditListOfPeople,
	reasonPlaceholder:             RefusalCreditCastPlaceholder,
	reasonUnnamedCredit:           RefusalCreditNotAPerson,
	reasonPositionTaken:           RefusalPositionClaimed,
	reasonSeriesCap:               RefusalOverSeriesCap,
}
