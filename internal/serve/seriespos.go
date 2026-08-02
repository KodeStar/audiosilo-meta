package serve

import "strings"

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
//     number in Go keeps "2", "02", "2.5" and "1-3.5" distinct values, which is
//     what the data model says they are.
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
// The cost is bounded: at most two series probes (one FTS query each) plus one
// membership read per probed series, and only for a query whose last token is a
// number.

// seriesProbeLimit is how many series a residual may resolve to. A user naming a
// series and a number means one series; the small fan-out only exists so a
// prefix ("stormlight 2") that ranks the intended series second still resolves.
const seriesProbeLimit = 3

// maxPositionToken bounds the token that may be read as a position. Series
// positions are small ("2", "2.5", "1-3.5"); the bound is what keeps an
// identifier a user pasted (an ISBN, an ASIN-ish digit run) from being read as
// one.
const maxPositionToken = 8

// seriesMatchSQL resolves a series-name query through the same FTS index the
// main search uses, restricted to series rows. Named so the index guard
// (TestServeLookupsAreIndexed) EXPLAINs the text the request path runs.
const seriesMatchSQL = `SELECT id FROM search_fts WHERE search_fts MATCH ? AND kind='series' ORDER BY bm25(search_fts) LIMIT ?`

// volumeKeywords are the words that can sit between a series name and its
// number ("jack reacher book 2", "jack reacher band 2"). Stripping one is only
// ever the SECOND guess: parseSeriesPositionQuery tries the whole residual
// first, so "the jungle book 2" resolves against the series actually named "The
// Jungle Book" rather than against a series called "The Jungle". A word is
// stripped only when leaving it in resolves nothing.
var volumeKeywords = map[string]bool{
	"book": true, "bk": true, "vol": true, "volume": true,
	"part": true, "pt": true, "no": true, "num": true, "nr": true,
	// German / Dutch / French / Spanish / Italian volume words, the forms that
	// ride in on European series names.
	"band": true, "teil": true, "deel": true, "tome": true, "tomo": true,
	"buch": true, "libro": true, "livre": true,
}

// seriesPositionQuery is a query read as "<series name> [volume word] <number>".
// residuals are the series-name candidates to try, most likely first.
type seriesPositionQuery struct {
	position  string
	residuals []string
}

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

// keywordKey normalizes a token for the volume-word table: lowercased, with a
// trailing "." dropped ("Vol." -> "vol").
func keywordKey(tok string) string {
	return strings.ToLower(strings.TrimSuffix(tok, "."))
}

// positionToken reports whether tok can be read as a series position, returning
// it cleaned of a leading "#" and a trailing "." or ",". A position is digits
// with optional "." and "-" separators ("2", "02", "2.5", "1-3.5") and must
// start with a digit.
func positionToken(tok string) (string, bool) {
	tok = strings.TrimPrefix(tok, "#")
	tok = strings.TrimRight(tok, ".,")
	if tok == "" || len(tok) > maxPositionToken {
		return "", false
	}
	if tok[0] < '0' || tok[0] > '9' {
		return "", false
	}
	for i := 0; i < len(tok); i++ {
		c := tok[i]
		if (c < '0' || c > '9') && c != '.' && c != '-' {
			return "", false
		}
	}
	return tok, true
}

// positionKey normalizes a position string for comparison: leading zeros are
// dropped from each numeric component, so a query for "02" finds the work stored
// at "2" and vice versa. Nothing else is normalized - "2.5" and "1-3.5" are
// compared as the exact strings the data states, which is all the schema
// promises about them.
func positionKey(pos string) string {
	pos = strings.TrimSpace(pos)
	var b strings.Builder
	b.Grow(len(pos))
	for i := 0; i < len(pos); {
		j := i
		for j < len(pos) && pos[j] != '.' && pos[j] != '-' {
			j++
		}
		b.WriteString(trimZeros(pos[i:j]))
		if j < len(pos) {
			b.WriteByte(pos[j])
		}
		i = j + 1
		if j == len(pos) {
			break
		}
	}
	return b.String()
}

// trimZeros drops leading zeros from an all-digit component, keeping one digit
// ("02" -> "2", "000" -> "0"). A component that is not all digits is returned
// unchanged, so nothing is silently rewritten.
func trimZeros(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return s
		}
	}
	i := 0
	for i < len(s)-1 && s[i] == '0' {
		i++
	}
	return s[i:]
}

// seriesPositionHits resolves "<series> <n>" to the ids of the works sitting at
// that position, best-matching series first, or nil when the query is not a
// series-position query or nothing resolves. Callers prepend the ids to their
// ranked results; the works are known to exist (seriesWorksSQL joins works), so
// a hit is never a dangling membership row.
func (s *snapshot) seriesPositionHits(q string) ([]string, error) {
	pq, ok := parseSeriesPositionQuery(q)
	if !ok {
		return nil, nil
	}
	want := positionKey(pq.position)

	for _, residual := range pq.residuals {
		seriesIDs, err := s.seriesMatching(residual, seriesProbeLimit)
		if err != nil {
			return nil, err
		}
		var out []string
		seen := map[string]bool{}
		for _, sid := range seriesIDs {
			rows, err := s.db.Query(seriesWorksSQL, sid)
			if err != nil {
				return nil, err
			}
			members, err := scanPairs(rows, func(workID, pos string) seriesMember {
				return seriesMember{workID: workID, position: pos}
			})
			if err != nil {
				return nil, err
			}
			for _, m := range members {
				if positionKey(m.position) == want && !seen[m.workID] {
					seen[m.workID] = true
					out = append(out, m.workID)
				}
			}
		}
		if len(out) > 0 {
			return out, nil
		}
	}
	return nil, nil
}

// seriesMatching returns the ids of series whose name matches the query, best
// first. It goes through ftsQuery like every other search path, so no user input
// can break the MATCH.
func (s *snapshot) seriesMatching(query string, limit int) ([]string, error) {
	rows, err := s.db.Query(seriesMatchSQL, ftsQuery(query), limit)
	if err != nil {
		return nil, err
	}
	return scanIDs(rows)
}
