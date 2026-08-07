package serve

import (
	"strings"
)

// workResult is a search hit that is a work: the card fields inline, plus the
// kind discriminator and the work's narrators.
type workResult struct {
	Kind      string      `json:"kind"`
	ID        string      `json:"id"`
	Title     string      `json:"title"`
	Authors   []personRef `json:"authors"`
	Series    *seriesRef  `json:"series"`
	CoverURL  *string     `json:"cover_url"`
	AddedAt   *string     `json:"added_at"`
	Narrators []personRef `json:"narrators"`
}

type personResult struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

type seriesResult struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Name  string `json:"name"`
	Works int    `json:"works"`
}

// ftsQuery turns a raw user query into a safe FTS5 MATCH expression: every token
// is wrapped in double quotes (with embedded quotes doubled per FTS5 escaping)
// so no token is ever interpreted as an operator, and the final token gets a
// trailing '*' for prefix matching. An all-punctuation query yields a harmless
// empty-phrase match rather than a syntax error.
func ftsQuery(q string) string { return ftsMatch(q, true) }

// ftsPhrase is ftsQuery without the trailing prefix-star: the same escaping,
// matching only whole tokens. It is what a NAME lookup wants (see seriespos.go);
// a prefix-star over a short query walks a large fraction of the index, which is
// affordable for the one hit the user is typing towards and not for a lookup the
// server issues on their behalf.
func ftsPhrase(q string) string { return ftsMatch(q, false) }

// ftsMatch is the single escaping implementation behind both: it is the one
// place a token becomes an FTS5 phrase, so no caller can ever build a MATCH
// expression a quote or an operator could break.
func ftsMatch(q string, prefixLast bool) string {
	tokens := strings.Fields(q)
	if len(tokens) == 0 {
		return `""`
	}
	parts := make([]string, len(tokens))
	for i, tok := range tokens {
		escaped := strings.ReplaceAll(tok, `"`, `""`)
		parts[i] = `"` + escaped + `"`
		if prefixLast && i == len(tokens)-1 {
			parts[i] += "*"
		}
	}
	return strings.Join(parts, " ")
}

// searchHit is one row of the FTS result, kept in rank order while the work
// cards are resolved in a single batch.
type searchHit struct {
	kind searchKind
	id   string
}

// mergeHits puts the series-position work ids in front of the FTS hits, drops
// the duplicate an FTS hit would be, and truncates to the page size. The page
// stays exactly limit long, so a boost costs the last FTS hit rather than
// widening the response.
//
// It is the ONE place search results are deduped, so no producer needs its own
// seen set. Prepending is only sound because /search returns a single ranked
// page with no offset: there is no later page for a boost to shift a hit onto.
// If the endpoint ever grows ?offset, the boost has to apply at offset 0 only.
func mergeHits(boosted []string, ftsHits []searchHit, limit int) []searchHit {
	if len(boosted) == 0 {
		return ftsHits
	}
	out := make([]searchHit, 0, len(boosted)+len(ftsHits))
	seen := make(map[searchHit]bool, len(boosted))
	for _, id := range boosted {
		h := searchHit{kind: kindWork, id: id}
		if seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	for _, h := range ftsHits {
		if !seen[h] {
			out = append(out, h)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// searchKind is a value of search_fts's kind column: the three families the
// index holds. The column is stored but UNINDEXED, so a type-scoped search can
// filter on it in the WHERE - no new index and no artifact change, which is what
// makes the scoped endpoints work against every already-published release.
type searchKind string

const (
	kindWork   searchKind = "work"
	kindPerson searchKind = "person"
	kindSeries searchKind = "series"
)

// The two FTS queries behind every search endpoint. They differ only in the kind
// predicate, so a scoped page is filtered at the source rather than after the
// fact: ?limit=20 on works/search returns 20 works, not the works among 20 mixed
// hits.
const (
	searchSQL     = `SELECT kind, id FROM search_fts WHERE search_fts MATCH ? ORDER BY bm25(search_fts) LIMIT ?`
	searchKindSQL = `SELECT kind, id FROM search_fts WHERE search_fts MATCH ? AND kind = ? ORDER BY bm25(search_fts) LIMIT ?`
)

// search runs the FTS query across every kind and assembles heterogeneous
// results (work / person / series) into a single ranked slice.
//
// A query that names a series and a number ("jack reacher 2") resolves that
// volume FIRST, ahead of the FTS hits - see seriespos.go for why that resolution
// is query-side rather than extra text in the index. The boost is additive: the
// FTS hits that follow are exactly the ones the query returned before, minus any
// duplicate of a boosted work, and a query that resolves no series-position hit
// is byte-for-byte the old behaviour.
func (s *snapshot) search(q string, limit int) ([]any, error) {
	hits, err := s.ftsHits(searchSQL, ftsQuery(q), limit)
	if err != nil {
		return nil, err
	}
	return s.results(mergeHits(s.boostedWorks(q), hits, limit))
}

// searchScoped is search restricted to one kind: the query behind
// /works/search, /people/search and /series/search. Both search paths compose
// their page through results, so a hit carries the same shape and the same kind
// discriminator whichever endpoint produced it.
//
// The series-position boost applies to the WORK scope only. The ids it resolves
// are always works, so prepending them anywhere else would put a work on a page
// that promises people or series.
func (s *snapshot) searchScoped(kind searchKind, q string, limit int) ([]any, error) {
	hits, err := s.ftsHits(searchKindSQL, ftsQuery(q), string(kind), limit)
	if err != nil {
		return nil, err
	}
	var boosted []string
	if kind == kindWork {
		boosted = s.boostedWorks(q)
	}
	return s.results(mergeHits(boosted, hits, limit))
}

// ftsHits runs one of the search queries and materializes its rows in rank
// order. The hits are collected FIRST so the whole page's work cards can be
// resolved in one batch (see results) rather than inside the scan loop.
func (s *snapshot) ftsHits(query string, args ...any) ([]searchHit, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	return scanPairs(rows, func(kind, id string) searchHit {
		return searchHit{kind: searchKind(kind), id: id}
	})
}

// boostedWorks resolves a query's series-position work ids.
//
// A failed probe degrades to "no boost" instead of failing the request: the
// boost is an enrichment of a page the FTS query produces on its own, and a 500
// on a search the user could otherwise have had is the worse outcome. It is
// logged, so a broken probe is visible rather than silent.
func (s *snapshot) boostedWorks(q string) []string {
	boosted, err := s.seriesPositionHits(q)
	if err != nil {
		s.logf("serve: series-position probe for %q failed, serving the plain search page: %v", q, err)
		return nil
	}
	return boosted
}

// results composes one ranked page of hits into their per-kind result shapes.
// It is the single assembly the combined and type-scoped searches share, so the
// composition rules of a result cannot be spelled twice and drift apart.
//
// The work cards for the whole page are resolved at once (cardsByID) rather than
// four queries per work hit inside the loop. These are the busiest endpoints in
// the API - every keystroke of the site's search box - so the per-page query
// count is fixed instead of proportional to the number of work hits.
func (s *snapshot) results(hits []searchHit) ([]any, error) {
	workIDs := make([]string, 0, len(hits))
	for _, h := range hits {
		if h.kind == kindWork {
			workIDs = append(workIDs, h.id)
		}
	}
	cards, err := s.cardsByID(workIDs)
	if err != nil {
		return nil, err
	}

	out := []any{}
	for _, h := range hits {
		switch h.kind {
		case kindWork:
			card := cards[h.id]
			if card == nil {
				continue
			}
			narrators, err := s.workNarrators(h.id)
			if err != nil {
				return nil, err
			}
			out = append(out, workResult{
				Kind: string(kindWork), ID: card.ID, Title: card.Title, Authors: card.Authors,
				Series: card.Series, CoverURL: card.CoverURL, AddedAt: card.AddedAt,
				Narrators: narrators,
			})
		case kindPerson:
			name, err := s.personName(h.id)
			if err != nil {
				return nil, err
			}
			out = append(out, personResult{Kind: string(kindPerson), ID: h.id, Name: name})
		case kindSeries:
			name, n, err := s.seriesSummary(h.id)
			if err != nil {
				return nil, err
			}
			out = append(out, seriesResult{Kind: string(kindSeries), ID: h.id, Name: name, Works: n})
		}
	}
	return out, nil
}

// workNarratorsSQL reads the distinct narrators across a work's recordings. It
// rides the (work_id, recording_id) index by prefix; shared with the index guard.
const workNarratorsSQL = `SELECT p.id, p.name FROM recording_narrators rn JOIN people p ON p.id = rn.person_id ` +
	`WHERE rn.work_id=? GROUP BY p.id, p.name ORDER BY MIN(rn.ord)`

// workNarrators returns the distinct narrators across a work's recordings.
func (s *snapshot) workNarrators(workID string) ([]personRef, error) {
	rows, err := s.db.Query(workNarratorsSQL, workID)
	if err != nil {
		return nil, err
	}
	return scanPersonRefs(rows)
}

func (s *snapshot) personName(id string) (string, error) {
	var name string
	err := s.db.QueryRow(`SELECT name FROM people WHERE id=?`, id).Scan(&name)
	return name, err
}

func (s *snapshot) seriesSummary(id string) (string, int, error) {
	var name string
	if err := s.db.QueryRow(`SELECT name FROM series WHERE id=?`, id).Scan(&name); err != nil {
		return "", 0, err
	}
	var n int
	if err := s.db.QueryRow(seriesWorksCountSQL, id).Scan(&n); err != nil {
		return "", 0, err
	}
	return name, n, nil
}
