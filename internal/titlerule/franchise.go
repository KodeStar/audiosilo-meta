package titlerule

// franchise.go is the FRANCHISE key: a series name with the words a retailer hangs on
// a franchise's name peeled off too, so "The Jack Ryan Universe (Publication Order)" and
// "A Jack Ryan Novel (Publication Order)" meet on "jackryan" where SeriesKey keeps them
// apart ("jackryanuniverse" against "jackryan").
//
// It is deliberately LOOSER than SeriesKey and is read by one detector alone
// (internal/audit's ordering-twin), whose proposals are always advisory: "universe" and
// "saga" are part of a real name often enough ("Vorkosigan Saga", "The Expanse
// Universe" beside a different "Expanse") that a key folding them away can only ever
// nominate a pair for a human.

// franchiseSuffixes are SeriesKey's decoration suffixes (which already cover "series",
// "novel(s)" and "books") plus the closed list of trailing franchise words: " universe"
// and the one suffix SeriesKey holds back, sagaSuffix. Trailing only: a franchise word
// inside a name is part of the name.
var franchiseSuffixes = append(append(append([]string{}, seriesDecorSuffixes...), " universe"), sagaSuffix...)

// SeriesFranchiseKey is SeriesKey with the franchise words peeled as well:
// parentheticals removed, a leading article dropped, every trailing decoration or
// franchise word peeled, then folded.
func SeriesFranchiseKey(name string) string { return seriesKey(name, franchiseSuffixes) }
