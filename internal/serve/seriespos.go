package serve

import (
	"sort"
	"strings"
)

// Series-position search: reading "jack reacher 2" as "volume 2 of the Jack
// Reacher series" and surfacing that work ahead of the ordinary FTS hits.
//
// WHY THIS IS QUERY-SIDE (in the server) rather than index-side (extra text in
// the work's FTS row in internal/build):
//
//   - Ranking. The requirement is that the positioned volume comes FIRST. bm25
//     over a widened FTS row cannot be steered: the series record, a sibling
//     volume, or any work whose name column happens to carry the same digits all
//     compete on term frequency. Resolving the position explicitly and prepending
//     it is the only deterministic answer.
//   - The FTS tokenizer would blur the positions. unicode61 splits "2.5" into
//     the tokens "2" and "5" and "1-3.5" into "1", "3", "5", so indexing the
//     position strings would make "jack reacher 2" match the 2.5 novella and the
//     1-3.5 omnibus as strongly as the actual volume 2. Comparing the stated
//     number through parsePositionRange keeps "2", "2.5" and "1-3.5" the distinct
//     values the data model says they are.
//   - No artifact change at all. No new table (so no SchemaVersion question - it
//     versions what a reader may SELECT), no new FTS column (which would silently
//     widen every existing binary's unqualified MATCH), no rebuild: the feature
//     ships with the binary and works against every already-published release,
//     old or new. It reads series_works, which has existed since schema_version
//     1, so there is nothing to version-gate and nothing to degrade.
//   - No dilution of ordinary search. Nothing about a title query changes; the
//     boost only fires when the query ENDS in a number, a series actually
//     resolves from the rest of it, and that series actually holds a work at that
//     position. "1984", "Fahrenheit 451" and "Slow Horses 2" therefore keep
//     exactly the FTS results they had.
//
// WHAT KEEPS IT CHEAP. /api/v1/search is unauthenticated, CORS-open and hit on
// every keystroke of the site's search box, so the probe is bounded on both
// sides of the FTS call:
//
//   - The residual is matched AS WRITTEN, with no trailing prefix-star. This is a
//     name lookup, not type-ahead: "jack reach 2" simply gets no boost, and the
//     FTS page still serves that user. A prefix-star on a short residual made the
//     MATCH walk a large fraction of the catalogue - measured at 237ms for
//     "s 1" against 7ms before the feature - because kind='series' is an
//     UNINDEXED post-filter and the bm25 ORDER BY then sorts the whole match set
//     in a temp b-tree.
//   - A residual made only of junk (articles, one-letter tokens, bare volume
//     words) is not probed at all - see worthProbing. "the 1" and "the 100" would
//     otherwise match tens of thousands of series names on the token "the", pay
//     for that sort on every keystroke, and prepend three effectively random
//     position-1 works to the page.
//
// What is left is at most two probes (one FTS query each) plus ONE batched
// membership read per probe, and only for a query whose last token is a number.

// seriesProbeLimit is how many series a residual may resolve to. A user naming a
// series and a number means one series; the small fan-out only exists so a
// sub-name ("expanse 3" against "The Expanse") that ranks second still resolves.
const seriesProbeLimit = 3

// maxPositionToken bounds the token that may be read as a position. Series
// positions are small ("2", "2.5", "1-3.5"); the bound is what keeps an
// identifier a user pasted (an ISBN, an ASIN-ish digit run) from being read as
// one. A handful of real range positions are longer (Perry Rhodan's
// "2500-2599" spans) and are simply not reachable through the boost; those works
// stay findable by title on the ordinary FTS page.
const maxPositionToken = 8

// seriesMatchSQL resolves a series-name query through the same FTS index the
// main search uses, restricted to series rows. It selects the NAME as well as
// the id (a series row's FTS title column IS its name), so preferWholeName costs
// no extra query. The kind literal is composed from the searchKind constant at
// COMPILE time, so the value vocabulary has one home and this is still a
// constant string.
const seriesMatchSQL = `SELECT id, title FROM search_fts WHERE search_fts MATCH ? AND kind='` +
	string(kindSeries) + `' ORDER BY bm25(search_fts) LIMIT ?`

// seriesMembersSQL reads the membership of every probed series in ONE query
// rather than one per candidate. It joins works, so a membership row pointing at
// a work the artifact does not carry can never be boosted.
func seriesMembersSQL(ph string) string {
	return `SELECT sw.series_id, sw.work_id, sw.position FROM series_works sw JOIN works w ON w.id = sw.work_id ` +
		`WHERE sw.series_id IN (` + ph + `)`
}

// volumeKeywords are the words that can sit between a series name and its
// number ("jack reacher book 2", "jack reacher band 2"). Stripping one is only
// ever the SECOND guess: parseSeriesPositionQuery tries the whole residual
// first, so "the jungle book 2" resolves against the series actually named "The
// Jungle Book" rather than against a series called "The Jungle". A word is
// stripped only when leaving it in resolves nothing.
//
// The vocabulary is EVIDENCE-BASED, like its two siblings - pkg/scan/derive.go's
// trailingVol/leadingVol (book|vol|volume|part, plus a leading "#") and
// internal/importer/recordings.go's volumeMarkerRE (book|vol|volume). It is
// those four words plus the three that the libex dump attests in the hundreds as
// a trailing "<word> <number>" title marker: teil (354), tome (94) and band
// (93) - band being the form internal/importer already handles by name
// (stripTitleNarratorQualifier's "..., Band 11"). The forms the dump barely
// knows were dropped rather than guessed at: bk (1), num (0), livre (6), deel
// (9), tomo (11), libro (12), nr (20) and buch (52, where a German query would
// say "Band"), plus the two ambiguous English abbreviations pt (86) and no
// (142), where reading a user's "no" as a volume word costs more than a marker
// that rare is worth. Widen it only with the same kind of evidence.
var volumeKeywords = map[string]bool{
	"book": true, "vol": true, "volume": true, "part": true,
	"band": true, "teil": true, "tome": true,
}

// seriesPositionQuery is a query read as "<series name> [volume word] <number>".
// residuals are the series-name candidates to try, most likely first.
type seriesPositionQuery struct {
	position  string
	residuals []string
}

// seriesCandidate is one probed series: its id and the name the probe matched.
type seriesCandidate struct{ id, name string }

// parseSeriesPositionQuery reads a trailing number off the query. It reports
// false unless the query has at least two tokens AND the last one is
// position-shaped, so a bare number ("1984") and an ordinary title query are
// never treated as a position lookup.
func parseSeriesPositionQuery(q string) (seriesPositionQuery, bool) {
	tokens := strings.Fields(q)
	if len(tokens) < 2 {
		return seriesPositionQuery{}, false
	}
	pos, ok := positionToken(tokens[len(tokens)-1])
	if !ok {
		return seriesPositionQuery{}, false
	}
	rest := tokens[:len(tokens)-1]

	out := seriesPositionQuery{position: pos, residuals: []string{strings.Join(rest, " ")}}
	if len(rest) > 1 && volumeKeywords[keywordKey(rest[len(rest)-1])] {
		out.residuals = append(out.residuals, strings.Join(rest[:len(rest)-1], " "))
	}
	return out, true
}

// positionToken reports whether tok can be read as a series position, returning
// it cleaned of a leading "#" and a trailing "." or ",". It must start with a
// digit, be short enough to be a position rather than an identifier, and parse
// through the same grammar a series listing is ordered by.
func positionToken(tok string) (string, bool) {
	tok = strings.TrimPrefix(tok, "#")
	tok = strings.TrimRight(tok, ".,")
	if tok == "" || len(tok) > maxPositionToken {
		return "", false
	}
	if tok[0] < '0' || tok[0] > '9' {
		return "", false
	}
	if _, _, ok := parsePositionRange(tok); !ok {
		return "", false
	}
	return tok, true
}

// positionsEqual compares a stated position against a stored one through
// parsePositionRange, this package's single copy of the position grammar (the
// same call snapshot.series orders a series by). Two positions are the same when
// both parse and their spans are equal, so "02" finds "2" and "2.50" finds
// "2.5", while "2", "2.5" and "1-3.5" stay three different volumes.
func positionsEqual(a, b string) bool {
	alo, ahi, aok := parsePositionRange(a)
	blo, bhi, bok := parsePositionRange(b)
	return aok && bok && alo == blo && ahi == bhi
}

// worthProbing reports whether a residual identifies a series well enough to
// spend an FTS query on. It needs one token that is not an article, not a bare
// volume word, and at least two runes long - so "oz 5" and "it 2" are probed
// while "the 1", "s 1" and "book 2" are not.
func worthProbing(residual string) bool {
	return hasIdentifyingToken(residual, probeStopwords, volumeKeywords)
}

// preferWholeName drops the partial matches when the residual names a series
// OUTRIGHT: "jack reacher 2" is volume 2 of "Jack Reacher", not of every series
// with those words in its name. When nothing matches whole, the ranked
// candidates are kept as they are - "expanse 3" legitimately resolves against
// "The Expanse" and its regional twin.
func preferWholeName(cands []seriesCandidate, residual string) []seriesCandidate {
	want := nameKey(residual)
	var exact []seriesCandidate
	for _, c := range cands {
		if nameKey(c.name) == want {
			exact = append(exact, c)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return cands
}

// seriesPositionHits resolves "<series> <n>" to the ids of the works sitting at
// that position, best-matching series first, or nil when the query is not a
// series-position query or nothing resolves. Callers prepend the ids to their
// ranked results; the works are known to exist (seriesMembersSQL joins works),
// so a hit is never a dangling membership row. Repeats are left to the caller's
// merge, which owns dedupe.
func (s *snapshot) seriesPositionHits(q string) ([]string, error) {
	pq, ok := parseSeriesPositionQuery(q)
	if !ok {
		return nil, nil
	}
	for _, residual := range pq.residuals {
		if !worthProbing(residual) {
			continue
		}
		cands, err := s.seriesMatching(residual, seriesProbeLimit)
		if err != nil {
			return nil, err
		}
		if len(cands) == 0 {
			continue
		}
		cands = preferWholeName(cands, residual)
		ids := make([]string, len(cands))
		for i, c := range cands {
			ids[i] = c.id
		}
		hits, err := s.worksAtPosition(ids, pq.position)
		if err != nil {
			return nil, err
		}
		if len(hits) > 0 {
			return hits, nil
		}
	}
	return nil, nil
}

// worksAtPosition returns the works sitting at pos in the given series, in the
// order the series were ranked (and by work id within a series, so the answer
// never depends on row order). One query covers every candidate.
func (s *snapshot) worksAtPosition(seriesIDs []string, pos string) ([]string, error) {
	bySeries := map[string][]string{}
	err := eachChunk(seriesIDs, func(ph string, args []any) error {
		rows, err := s.db.Query(seriesMembersSQL(ph), args...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var sid, workID, position string
			if err := rows.Scan(&sid, &workID, &position); err != nil {
				return err
			}
			if positionsEqual(position, pos) {
				bySeries[sid] = append(bySeries[sid], workID)
			}
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, sid := range seriesIDs {
		members := bySeries[sid]
		sort.Strings(members)
		out = append(out, members...)
	}
	return out, nil
}

// probeMatch builds the MATCH expression for a residual. Stopwords are left OUT
// of it: "the" identifies no series but intersecting its posting list is the
// single most expensive term in the index ("the expanse 2" measured 3x the
// residual's cost with it, 1.1x without). Dropping them only widens the match -
// "The Expanse" still matches "expanse" - and the widening is absorbed by
// preferWholeName, which compares against the residual as the user wrote it.
// worthProbing has already established that at least one token survives; the
// fallback keeps the function total rather than relying on that.
func probeMatch(residual string) string {
	kept := make([]string, 0, 4)
	for _, tok := range strings.Fields(residual) {
		if probeStopwords[keywordKey(tok)] {
			continue
		}
		kept = append(kept, tok)
	}
	if len(kept) == 0 {
		return ftsPhrase(residual)
	}
	return ftsPhrase(strings.Join(kept, " "))
}

// seriesMatching returns the series whose names match the query, best first. It
// goes through ftsPhrase, so no user input can break the MATCH - and
// deliberately not through ftsQuery: see the prefix-star note in the file
// header.
func (s *snapshot) seriesMatching(query string, limit int) ([]seriesCandidate, error) {
	rows, err := s.db.Query(seriesMatchSQL, probeMatch(query), limit)
	if err != nil {
		return nil, err
	}
	return scanPairs(rows, func(id, name string) seriesCandidate {
		return seriesCandidate{id: id, name: name}
	})
}
