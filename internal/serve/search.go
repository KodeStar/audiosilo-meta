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

// search runs the FTS query and assembles heterogeneous results (work / person
// / series) into a single ranked slice.
func (s *snapshot) search(q string, limit int) ([]any, error) {
	match := ftsQuery(q)
	rows, err := s.db.Query(
		`SELECT kind, id FROM search_fts WHERE search_fts MATCH ? ORDER BY bm25(search_fts) LIMIT ?`, match, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := []any{}
	for rows.Next() {
		var kind, id string
		if err := rows.Scan(&kind, &id); err != nil {
			return nil, err
		}
		switch kind {
		case "work":
			card, err := s.workCard(id)
			if err != nil {
				return nil, err
			}
			if card == nil {
				continue
			}
			narrators, err := s.workNarrators(id)
			if err != nil {
				return nil, err
			}
			out = append(out, workResult{
				Kind: "work", ID: card.ID, Title: card.Title, Authors: card.Authors,
				Series: card.Series, CoverURL: card.CoverURL, AddedAt: card.AddedAt,
				Narrators: narrators,
			})
		case "person":
			name, err := s.personName(id)
			if err != nil {
				return nil, err
			}
			out = append(out, personResult{Kind: "person", ID: id, Name: name})
		case "series":
			name, n, err := s.seriesSummary(id)
			if err != nil {
				return nil, err
			}
			out = append(out, seriesResult{Kind: "series", ID: id, Name: name, Works: n})
		}
	}
	return out, rows.Err()
}

// workNarrators returns the distinct narrators across a work's recordings.
func (s *snapshot) workNarrators(workID string) ([]personRef, error) {
	rows, err := s.db.Query(
		`SELECT p.id, p.name FROM recording_narrators rn JOIN people p ON p.id = rn.person_id WHERE rn.work_id=? GROUP BY p.id, p.name ORDER BY MIN(rn.ord)`, workID)
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
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM series_works WHERE series_id=?`, id).Scan(&n); err != nil {
		return "", 0, err
	}
	return name, n, nil
}
