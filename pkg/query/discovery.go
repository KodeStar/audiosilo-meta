package query

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

// BrowseQueryMaxBytes shares the existing FTS input bound with directory forms.
const BrowseQueryMaxBytes = maxQueryBytes

// BrowseFilters selects a directory's search terms and first display character.
// Letter is empty (all), A-Z, 0-9 or Other. Non-ASCII initials belong to Other.
type BrowseFilters struct{ Query, Letter string }

// NormalizeBrowseFilters validates user input before any database work. Search
// uses the same escaped, phrase-bounded FTS expression as the public search.
func NormalizeBrowseFilters(f BrowseFilters) (BrowseFilters, error) {
	if !utf8.ValidString(f.Query) || len(f.Query) > BrowseQueryMaxBytes {
		return f, fmt.Errorf("search must be valid UTF-8 and at most %d bytes", BrowseQueryMaxBytes)
	}
	f.Query = strings.Join(strings.Fields(f.Query), " ")
	switch strings.ToUpper(f.Letter) {
	case "", "ALL":
		f.Letter = ""
	case "0-9":
		f.Letter = "0-9"
	case "OTHER":
		f.Letter = "Other"
	default:
		f.Letter = strings.ToUpper(f.Letter)
		if len(f.Letter) != 1 || f.Letter[0] < 'A' || f.Letter[0] > 'Z' {
			return f, fmt.Errorf("letter must be A-Z, 0-9 or Other")
		}
	}
	return f, nil
}

// BrowseFiltered returns a bounded directory page ordered by the displayed
// title/name (SQLite NOCASE), then id. Browse retains its exported id-order
// contract. The optional display indexes speed new artifacts; no query requires
// them, so already-published artifacts remain readable.
func (s *DB) BrowseFiltered(ctx context.Context, family PageFamily, filters BrowseFilters, limit, offset int) (*BrowsePage, error) {
	f, ok := pageFamilies[family]
	if !ok {
		return nil, nil
	}
	filters, err := NormalizeBrowseFilters(filters)
	if err != nil {
		return nil, err
	}
	limit, offset = catalogueWindow(limit, offset)
	d := BrowsePage{Entries: []BrowseEntry{}, Total: f.count(s), Limit: limit, Offset: offset}
	if d.Total == 0 {
		return &d, nil
	}
	base, title, language, args := s.browseFilteredSQL(family, filters)
	if filters.Query != "" || filters.Letter != "" {
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*)`+base, args...).Scan(&d.Total); err != nil {
			return nil, err
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, `+title+`, `+language+base+` ORDER BY `+title+` COLLATE NOCASE, id LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var e BrowseEntry
		if err := rows.Scan(&e.ID, &e.Title, &e.Language); err != nil {
			return nil, err
		}
		d.Entries = append(d.Entries, e)
	}
	return &d, rows.Err()
}

// This constructor is also exercised by the EXPLAIN regression guard. Guide
// sets enumerate indexed sidecar work ids, rather than probing all works.
func (s *DB) browseFilteredSQL(family PageFamily, f BrowseFilters) (base, title, language string, args []any) {
	table, title, language, kind := "works", "title", "language", kindWork
	switch family {
	case PersonPages:
		table, title, language, kind = "people", "name", "''", kindPerson
	case SeriesPages:
		table, title, language, kind = "series", "name", "''", kindSeries
	}
	base = ` FROM ` + table + ` WHERE 1=1`
	switch family {
	case CharacterPages:
		base += ` AND id IN (SELECT work_id FROM characters GROUP BY work_id)`
	case RecapPages:
		if s.schemaVersion >= summarySchemaVersion {
			base += ` AND id IN (SELECT work_id FROM recaps UNION SELECT work_id FROM recap_summaries)`
		} else {
			base += ` AND id IN (SELECT work_id FROM recaps GROUP BY work_id)`
		}
	}
	if f.Query != "" {
		base += ` AND id IN (SELECT id FROM search_fts WHERE search_fts MATCH ? AND kind=?)`
		args = append(args, ftsQuery(f.Query), string(kind))
	}
	switch f.Letter {
	case "":
	case "Other":
		base += ` AND NOT ((` + title + ` COLLATE NOCASE >= 'A' AND ` + title + ` COLLATE NOCASE < '{') OR (` + title + ` >= '0' AND ` + title + ` < ':'))`
	default:
		lo := strings.ToLower(f.Letter)
		hi := string(lo[0] + 1)
		if f.Letter == "0-9" {
			lo, hi = "0", ":"
		}
		base += ` AND ` + title + ` COLLATE NOCASE >= ? AND ` + title + ` COLLATE NOCASE < ?`
		args = append(args, lo, hi)
	}
	return base, title, language, args
}
