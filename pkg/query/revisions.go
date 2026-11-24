package query

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

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
	// anyRevisionSQL settles DB.hasRevisions at load and proves the table a
	// version 14 artifact claims - with the three columns every revision read
	// names, so a malformed table fails the load rather than every page.
	anyRevisionSQL = `SELECT EXISTS(SELECT kind, id, revised_at FROM page_revisions)`
	// allRevisionsSQL is the whole table, for the release-time revision pass
	// (internal/revision) - a deliberate scan, never a serve read.
	allRevisionsSQL = `SELECT kind, id, revised_at FROM page_revisions`
	// revisionsAtSQL is the stamp the artifact's revisions were made at. The meta
	// table is in every artifact, so it is asked of any version.
	revisionsAtSQL = `SELECT value FROM meta WHERE key = 'revisions_at'`
)

// PageRevisions calls fn for every page whose revision date the artifact knows,
// in no particular order: the release-time revision pass reads the previous
// release's dates through it. An artifact older than revisionsSchemaVersion, or
// one built without a previous release, knows none.
func (s *DB) PageRevisions(fn func(family PageFamily, id, revisedAt string)) error {
	if !s.hasRevisions {
		return nil
	}
	rows, err := s.db.Query(allRevisionsSQL)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var family, id, at string
		if err := rows.Scan(&family, &id, &at); err != nil {
			return err
		}
		fn(PageFamily(family), id, at)
	}
	return rows.Err()
}

// RevisionsAt is the artifact's meta(revisions_at), the stamp its changed pages
// were dated at: the zero time when it states none (an artifact older than the
// revision layer, or one built without a previous release), and an error when
// it states one that does not parse - unlike Info's BuiltAt, because the revision
// pass clamps the next stamp to it and must not lose that floor silently.
func (s *DB) RevisionsAt() (time.Time, error) {
	var v string
	err := s.db.QueryRow(revisionsAtSQL).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, fmt.Errorf("meta(revisions_at) %q: %w", v, err)
	}
	return t.UTC(), nil
}

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
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return at, nil
}
