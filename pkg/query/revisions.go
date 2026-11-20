package query

import "database/sql"

// The REVISION layer (revisionsSchemaVersion): the page_revisions table, one row
// per page whose last meaningful content change is known, keyed by the page's
// family (PageFamily, spelled as the sitemap family) and id. internal/revision
// writes it at release time by comparing each page's content digest with the
// previous release's; a page with no row has no known date, which every surface
// presents as "unknown" (no <lastmod>, no dateModified, no Last-Modified) rather
// than as some other date.

// revisionsSchemaVersion is the artifact schema_version that first carried the
// page_revisions table and meta(revisions_at).
const revisionsSchemaVersion = 14

// hasRevisionLayer reports whether this artifact carries the page_revisions
// table at all.
func (s *DB) hasRevisionLayer() bool { return s.schemaVersion >= revisionsSchemaVersion }

// revisedAtSQL is one page's revision as a correlated point read of
// page_revisions' primary key. The shard queries select it as their third column
// rather than JOINing the table, so each still walks its own family's index in id
// order exactly as its plain spelling does, and only adds a primary-key probe per
// row.
func revisedAtSQL(family PageFamily, idColumn string) string {
	return `(SELECT pr.revised_at FROM page_revisions pr WHERE pr.kind = '` + string(family) +
		`' AND pr.id = ` + idColumn + `)`
}

// The *Revised* shard spellings (see the plain ones in pages.go): the same walks
// with the revision date as the third column.
var (
	worksRevisedSitemapSQL = `SELECT id, added_at, ` + revisedAtSQL(WorkPages, "works.id") +
		` FROM works ORDER BY id LIMIT ? OFFSET ?`
	peopleRevisedSitemapSQL = `SELECT id, NULL, ` + revisedAtSQL(PersonPages, "people.id") +
		` FROM people ORDER BY id LIMIT ? OFFSET ?`
	seriesRevisedSitemapSQL = `SELECT id, NULL, ` + revisedAtSQL(SeriesPages, "series.id") +
		` FROM series ORDER BY id LIMIT ? OFFSET ?`
	charactersRevisedSitemapSQL = `SELECT DISTINCT work_id, NULL, ` + revisedAtSQL(CharacterPages, "characters.work_id") +
		` FROM characters ORDER BY work_id LIMIT ? OFFSET ?`
	// The UNION still deduplicates: both arms read the one recap page's row, so a
	// work in both tables yields two identical rows.
	recapsRevisedSitemapSQL = `SELECT work_id, NULL, ` + revisedAtSQL(RecapPages, "recaps.work_id") + ` FROM recaps ` +
		`UNION SELECT work_id, NULL, ` + revisedAtSQL(RecapPages, "recap_summaries.work_id") + ` FROM recap_summaries ` +
		`ORDER BY work_id LIMIT ? OFFSET ?`
)

const (
	// pageRevisionSQL is one page's revision date: the point query every entity
	// page makes, through the table's primary key.
	pageRevisionSQL = `SELECT revised_at FROM page_revisions WHERE kind = ? AND id = ?`
	// anyRevisionSQL settles DB.hasRevisions at load (and proves the table a
	// version 14 artifact claims).
	anyRevisionSQL = `SELECT EXISTS(SELECT 1 FROM page_revisions)`
)

// PageRevision is the date of the last meaningful content change of family's
// page id (RFC 3339), or "" when the artifact does not know one: the page has
// not changed since the revision chain started, there is no such page, or the
// artifact predates revisionsSchemaVersion. An artifact whose table is empty -
// one built without a previous release - answers "" without a query.
func (s *DB) PageRevision(family PageFamily, id string) (string, error) {
	if !s.hasRevisions {
		return "", nil
	}
	var at string
	err := s.db.QueryRow(pageRevisionSQL, string(family), id).Scan(&at)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return at, nil
}
