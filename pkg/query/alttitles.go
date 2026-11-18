package query

import "context"

// alttitles.go is the API's side of a work's ALTERNATE TITLES
// (alternateTitlesSchemaVersion): the other titles one book is published or known
// under, returned on the work document. Search needs nothing here: the builder
// puts them in the work's search_fts names, so every artifact's search reads them.

// alternateTitlesSchemaVersion is the artifact schema_version that first carried
// the work_alternate_titles table. A newer binary serving an older release states
// no alternate title, and an artifact CLAIMING it without the table fails to open
// (loadStats, the store links precedent).
const alternateTitlesSchemaVersion = 12

// alternateTitlesShapeSQL proves what a version 12 artifact claims: it names the
// table's columns and reads no row.
const alternateTitlesShapeSQL = `SELECT work_id, title, ord FROM work_alternate_titles LIMIT 0`

// workAlternateTitles returns a work's alternate titles in the record's order, or
// nil when it states none (or the artifact predates the table).
func (s *DB) workAlternateTitles(ctx context.Context, workID string) ([]string, error) {
	if s.schemaVersion < alternateTitlesSchemaVersion {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT title FROM work_alternate_titles WHERE work_id=? ORDER BY ord`, workID)
	if err != nil {
		return nil, err
	}
	return scanIDs(rows)
}
