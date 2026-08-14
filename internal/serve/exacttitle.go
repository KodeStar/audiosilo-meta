package serve

import "sort"

// Exact-title search: a query that IS a work's title returns that work FIRST,
// ahead of the ordinary FTS hits.
//
// THE BUG (issue #1838). bm25 scores a row, and a work's FTS row carries the
// names of its authors, narrators and series beside the title. "Spare" - Prince
// Harry's memoir, a heavily credited row - therefore scored BELOW "Spare Us!",
// "Spare Parts" and "Spare Room" and never reached the first page of a search
// for its own exact title; the second work titled "Spare" sat at rank 20. No
// column weighting fixes that: bm25 divides by the row's TOTAL token count, so
// for a short common title the top of the page is always the shortest row rather
// than the matching one.
//
// WHY THIS IS QUERY-SIDE (in the server), like the series-position boost next
// door in seriespos.go:
//
//   - Ranking. The requirement is that the work the user named comes FIRST.
//     Widening the index cannot deliver that, for the reason the bug exists:
//     relevance is a score over a document, and an exact title is an EQUALITY.
//     Resolving it explicitly and prepending it is the only deterministic answer.
//   - No artifact change at all. No new table, no new column, no new index (so
//     no SchemaVersion question - it versions what a reader may SELECT), no
//     rebuild: it reads search_fts and works, both of which have existed since
//     schema_version 1. The fix ships with the binary and works against every
//     already-published release.
//   - No dilution of ordinary search. A query that is nobody's title resolves
//     nothing, and the page is byte-for-byte the page it was.
//
// WHAT KEEPS IT CHEAP. /api/v1/search is unauthenticated, CORS-open and hit on
// every keystroke of the site's search box, and unlike the series-position probe
// this one is reached by EVERY query - so its cost is bounded on both sides of
// the FTS call:
//
//   - The query is matched AS WRITTEN, with no trailing prefix-star (ftsPhrase),
//     and COLUMN-FILTERED to the title. A half-typed title simply gets no boost;
//     the FTS page still serves that user. The filter is spelled over a
//     PARENTHESIZED expression, because an FTS5 column filter otherwise binds to
//     the first phrase only: an unparenthesized filter over the query "spare
//     harry" measured as "spare" in the TITLE and "harry" ANYWHERE, which matched
//     Prince Harry's Spare on a string that is not its title at all.
//   - A query made only of articles and one-letter tokens is not probed at all
//     (worthTitleProbing). Measured against the 279k-work artifact, per query
//     (warm cache), beside the plain search page the same query already pays:
//
//     query      probe    plain page      query      probe    plain page
//     "the"      426ms    196ms           "book"      21ms      24ms
//     "a"        119ms    367ms           "girl"       3ms       6ms
//     "s"        497ms    613ms           "it"         3.5ms     5.6ms
//     "of"        76ms    109ms           "spare"      0.2ms     0.3ms
//     "and"       37ms     55ms           "1984"       0.2ms     0.2ms
//
//     The junk queries are the ones whose match set is a large fraction of the
//     catalogue ("the" is in 88,458 titles) and they can boost nothing useful
//     anyway. Everything the guard admits costs at or below the FTS page it
//     enriches, worst measured case "book" at 21ms against that page's 24ms.
//
// WHERE IT APPLIES. The combined /search and the WORK scope, the same gating the
// series-position boost has (snapshot.search): the ids it resolves are always
// works, so prepending them on a people or series page would put a work where the
// endpoint promises neither. /abs/search is deliberately untouched - it goes
// through ftsHits directly, and an Audiobookshelf provider match is a per-book
// metadata lookup its caller ranks itself.
//
// It is deliberately NOT gated on the shape of the query the way the
// series-position probe is (last token numeric): any string can be a title.
// What replaces that gate is the guard above plus the match's own selectivity -
// a title query of two or more tokens is a hundreds-of-microseconds lookup.
//
// The single probe is one FTS query plus a primary-key read of the candidate
// titles; the exact comparison then happens in Go.

// exactTitleProbeLimit is how many candidate titles the probe reads back. The
// FTS match is ordered by title LENGTH, and a title containing the query is
// never shorter than a title that IS the query, so the exact matches are always
// at the front of that order: the window only has to be wide enough for the ties
// (five works are titled "1984"). A page is 20 results, so boosting more works
// than that could not be shown anyway.
const exactTitleProbeLimit = 20

// exactTitleSQL reads the shortest-titled works whose TITLE matches the query,
// then resolves each candidate's authoritative title from the works table.
//
// The title is re-read rather than taken from the FTS row because the indexed
// column is the work's title AND subtitle joined (internal/build), so comparing
// against it would refuse to boost a work whose subtitle the user did not type.
// The join costs one primary-key lookup per candidate and the LIMIT is inside
// the subquery, so it runs over the window (20 rows) rather than over the match
// set - measured indistinguishable from the FTS query alone, while the same join
// applied to the whole match set (ordering by the works table's own title) cost
// about 8x on a common word.
//
// The kind literal is composed from the searchKind constant at COMPILE time, so
// the value vocabulary has one home and this is still a constant string.
const exactTitleSQL = `SELECT w.id, w.title FROM works w JOIN (` +
	`SELECT id FROM search_fts WHERE search_fts MATCH ? AND kind='` + string(kindWork) + `' ` +
	`ORDER BY length(title), id LIMIT ?) f ON w.id = f.id`

// titleMatch builds the column-filtered MATCH expression for a title probe. It
// goes through ftsPhrase, so no user input can break the MATCH, and the phrases
// are parenthesized so the column filter covers EVERY one of them (see the file
// header).
func titleMatch(q string) string { return "title : (" + ftsPhrase(q) + ")" }

// worthTitleProbing reports whether a query can identify a title well enough to
// spend an FTS query on it. It shares the series probe's rule - one token that
// is not an article and is at least two runes long - but deliberately NOT its
// volume-word vocabulary: a bare "book" or "part" names no series, while a work
// really can be titled "Book" (measured at 21ms, in line with its own search
// page). The single-rune and article cases are the ones this costs real money
// on, and they are the ones a boost could not help: nothing worth surfacing is
// titled "s".
func worthTitleProbing(q string) bool { return hasIdentifyingToken(q, probeStopwords, nil) }

// titleCandidate is one probed work: its id and its authoritative title.
type titleCandidate struct{ id, title string }

// titleCandidates runs the probe's query for q and materializes the window: the
// shortest-titled works whose title matches, each with its authoritative title.
// It is the one place the probe's column contract is spelled, shared by
// exactTitleHits and the test that pins the column filter's scope.
func (s *snapshot) titleCandidates(q string) ([]titleCandidate, error) {
	rows, err := s.db.Query(exactTitleSQL, titleMatch(q), exactTitleProbeLimit)
	if err != nil {
		return nil, err
	}
	return scanPairs(rows, func(id, title string) titleCandidate {
		return titleCandidate{id: id, title: title}
	})
}

// exactTitleHits returns the ids of the works whose title IS the query, in id
// order, or nil when the query names no title. Callers prepend the ids to their
// ranked results; repeats are left to the caller's merge, which owns dedupe.
//
// The comparison is nameKey's - the lowercased ftsTerms, space-joined - the same
// normalization the series probe compares a name with, and the same words the
// FTS match was built from, so "the martian", "The Martian", "The Martian!" and
// "Halo:Primordium" all name one work. The FTS match only narrows the field;
// equality is decided here, in Go, which is what keeps "Spare" apart from
// "Spare Parts".
//
// Ordering by id makes the answer independent of the row order the probe
// happened to return, so two works sharing a title always boost the same way
// round.
func (s *snapshot) exactTitleHits(q string) ([]string, error) {
	if !worthTitleProbing(q) {
		return nil, nil
	}
	// The gate runs first: it is what the junk keystrokes hit, and it is cheaper
	// than the normalization. It now implies a non-empty want (both read the same
	// terms), so this is a guard keeping the function total rather than a live
	// path.
	want := nameKey(q)
	if want == "" {
		return nil, nil
	}
	cands, err := s.titleCandidates(q)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, c := range cands {
		if nameKey(c.title) == want {
			ids = append(ids, c.id)
		}
	}
	sort.Strings(ids)
	return ids, nil
}
