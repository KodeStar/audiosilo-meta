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
func ftsQuery(q string) string {
	tokens := strings.Fields(q)
	if len(tokens) == 0 {
		return `""`
	}
	parts := make([]string, len(tokens))
	for i, tok := range tokens {
		escaped := strings.ReplaceAll(tok, `"`, `""`)
		parts[i] = `"` + escaped + `"`
		if i == len(tokens)-1 {
			parts[i] += "*"
		}
	}
	return strings.Join(parts, " ")
}

// searchHit is one row of the FTS result, kept in rank order while the work
// cards are resolved in a single batch.
type searchHit struct{ kind, id string }

// mergeHits puts the series-position work ids in front of the FTS hits, drops
// the duplicate an FTS hit would be, and truncates to the page size. The page
// stays exactly limit long, so a boost costs the last FTS hit rather than
// widening the response.
func mergeHits(boosted []string, ftsHits []searchHit, limit int) []searchHit {
	if len(boosted) == 0 {
		return ftsHits
	}
	out := make([]searchHit, 0, len(boosted)+len(ftsHits))
	seen := make(map[searchHit]bool, len(boosted))
	for _, id := range boosted {
		h := searchHit{kind: "work", id: id}
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

// search runs the FTS query and assembles heterogeneous results (work / person
// / series) into a single ranked slice.
//
// A query that names a series and a number ("jack reacher 2") resolves that
// volume FIRST, ahead of the FTS hits - see seriespos.go for why that resolution
// is query-side rather than extra text in the index. The boost is additive: the
// FTS hits that follow are exactly the ones the query returned before, minus any
// duplicate of a boosted work, and a query that resolves no series-position hit
// is byte-for-byte the old behaviour.
//
// The hits are materialized FIRST and the work cards resolved for the whole page
// at once (cardsByID), rather than four queries per work hit inside the scan
// loop. This is the busiest endpoint in the API - every keystroke of the site's
// search box - so the per-page query count is fixed instead of proportional to
// the number of work hits.
func (s *snapshot) search(q string, limit int) ([]any, error) {
	boosted, err := s.seriesPositionHits(q)
	if err != nil {
		return nil, err
	}

	match := ftsQuery(q)
	rows, err := s.db.Query(
		`SELECT kind, id FROM search_fts WHERE search_fts MATCH ? ORDER BY bm25(search_fts) LIMIT ?`, match, limit)
	if err != nil {
		return nil, err
	}
	ftsHits, err := scanPairs(rows, func(kind, id string) searchHit {
		return searchHit{kind: kind, id: id}
	})
	if err != nil {
		return nil, err
	}
	hits := mergeHits(boosted, ftsHits, limit)

	workIDs := make([]string, 0, len(hits))
	for _, h := range hits {
		if h.kind == "work" {
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
		case "work":
			card := cards[h.id]
			if card == nil {
				continue
			}
			narrators, err := s.workNarrators(h.id)
			if err != nil {
				return nil, err
			}
			out = append(out, workResult{
				Kind: "work", ID: card.ID, Title: card.Title, Authors: card.Authors,
				Series: card.Series, CoverURL: card.CoverURL, AddedAt: card.AddedAt,
				Narrators: narrators,
			})
		case "person":
			name, err := s.personName(h.id)
			if err != nil {
				return nil, err
			}
			out = append(out, personResult{Kind: "person", ID: h.id, Name: name})
		case "series":
			name, n, err := s.seriesSummary(h.id)
			if err != nil {
				return nil, err
			}
			out = append(out, seriesResult{Kind: "series", ID: h.id, Name: name, Works: n})
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
