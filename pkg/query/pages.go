package query

// The reads behind metaserve's own PAGES: the entity families its sitemaps list
// and the presence probes in front of its two community guide pages
// (/works/{id}/recap and /works/{id}/characters). They live here, beside every
// other query, so the index guard (TestServeLookupsAreIndexed) EXPLAINs them and
// no SQL is written outside this package; the pages and sitemaps that render
// them are internal/serve's.

// PageFamily is one set of entity pages a sitemap lists - one URL per row its
// query returns. The value is the family's sitemap file prefix.
type PageFamily string

// The families, by what one page addresses. The two GUIDE families list one
// page per work CARRYING the sidecar the page renders.
const (
	RecapPages     PageFamily = "recaps"
	CharacterPages PageFamily = "characters"
	SeriesPages    PageFamily = "series"
	PersonPages    PageFamily = "people"
	WorkPages      PageFamily = "works"
)

// PageCount is how many pages family holds. It reads what Open settled rather
// than counting per request: the guide families count works CARRYING A SIDECAR,
// which is not a catalogue statistic and is deliberately not on Stats (the
// /api/v1/stats payload). Zero for an unknown family, and for the guide families
// on an artifact older than the sidecar tables.
func (s *DB) PageCount(family PageFamily) int {
	switch family {
	case RecapPages:
		return s.recapWorks
	case CharacterPages:
		return s.characterWorks
	case SeriesPages:
		return s.stats.Series
	case PersonPages:
		return s.stats.People
	case WorkPages:
		return s.stats.Works
	}
	return 0
}

// PageEntry is one page of a family: the record's id and, for works, its
// added_at (nil where the family carries no date).
type PageEntry struct {
	ID      string
	AddedAt *string
}

// Pages reads one window of family's pages in id order: limit rows from offset.
// An unknown family reads nothing.
func (s *DB) Pages(family PageFamily, limit, offset int) ([]PageEntry, error) {
	query := s.pageSQL(family)
	if query == "" {
		return nil, nil
	}
	rows, err := s.db.Query(query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []PageEntry
	for rows.Next() {
		var e PageEntry
		if err := rows.Scan(&e.ID, &e.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// pageSQL is the shard query of one family. It is a function of the database
// for the recap family's sake: its URL set spans two tables, the second of which
// only exists at schema_version 3, so WHICH query is honest depends on the
// artifact.
func (s *DB) pageSQL(family PageFamily) string {
	switch family {
	case RecapPages:
		_, shard := recapSitemapSQL(s.schemaVersion)
		return shard
	case CharacterPages:
		return charactersSitemapSQL
	case SeriesPages:
		return seriesSitemapSQL
	case PersonPages:
		return peopleSitemapSQL
	case WorkPages:
		return worksSitemapSQL
	}
	return ""
}

// The per-family shard queries. Each walks an index in id order - the same order
// the OFFSET pages through - so a shard is a windowed index walk rather than a
// scan (TestServeLookupsAreIndexed runs these very constants through EXPLAIN
// QUERY PLAN).
//
// Only works carries an added_at column, so every other query selects a literal
// NULL for it: one row shape, one scan loop, and no per-family branch that could
// disagree with the query it belongs to. A family with no date simply emits no
// <lastmod>, which is the protocol's own "unknown" - nothing is fabricated, and
// nothing about the artifact changes to serve any of these.
const (
	worksSitemapSQL  = `SELECT id, added_at FROM works ORDER BY id LIMIT ? OFFSET ?`
	peopleSitemapSQL = `SELECT id, NULL FROM people ORDER BY id LIMIT ? OFFSET ?`
	seriesSitemapSQL = `SELECT id, NULL FROM series ORDER BY id LIMIT ? OFFSET ?`

	// The guide families list one URL per work that CARRIES the sidecar the page
	// renders, which is the same condition the compose func applies (see
	// hasRecapGuide / hasCharacterGuide) - a sitemap must never promise a URL
	// that 404s.
	charactersSitemapSQL = `SELECT DISTINCT work_id, NULL FROM characters ORDER BY work_id LIMIT ? OFFSET ?`
	// A recap page is served for a chaptered recap OR a whole-book summary, so
	// its URL set is the union of two tables. The membership of the second one is
	// exactly what the page needs from it - internal/build writes a
	// recap_summaries row only for a sidecar that states in_short or ending - so
	// a listed URL always has a page. It is written as a COMPOUND select
	// rather than as a subquery deliberately: wrapping the union in a FROM makes
	// the outer step an unindexed scan of a co-routine, while each arm of a
	// compound still walks its own index.
	recapsSitemapSQL = `SELECT work_id, NULL FROM recaps UNION SELECT work_id, NULL FROM recap_summaries ` +
		`ORDER BY work_id LIMIT ? OFFSET ?`
	// recapsOnlySitemapSQL is that same set on an artifact that predates
	// recap_summaries (schema_version 2), where the second table does not exist
	// to be named - a query mentioning it would not even parse.
	recapsOnlySitemapSQL = `SELECT DISTINCT work_id, NULL FROM recaps ORDER BY work_id LIMIT ? OFFSET ?`

	// The guide families' counts, each counting exactly the rows its shard query
	// pages, so "how many shards" and "what is in them" cannot disagree.
	characterWorksCountSQL = `SELECT COUNT(DISTINCT work_id) FROM characters`
	recapWorksCountSQL     = `SELECT COUNT(*) FROM (SELECT work_id FROM recaps UNION SELECT work_id FROM recap_summaries)`
	recapWorksOnlyCountSQL = `SELECT COUNT(DISTINCT work_id) FROM recaps`
)

// recapSitemapSQL derives the recap family's two queries from the artifact's
// schema_version: how many pages the family holds, and how one shard's rows are
// read. A recap page is served for a chaptered recap OR a whole-book summary, so
// the set spans two tables and the second only exists from
// summarySchemaVersion - below it, a query naming recap_summaries would not even
// parse.
//
// It is one PURE function of the version rather than a value threaded through
// the snapshot, so the pairing is structural: the count and the listing are
// derived from the same input, at every call, and a snapshot's schemaVersion is
// immutable - so "how many shards a family advertises" and "what is in them"
// cannot be computed against different tables however the two are reached.
func recapSitemapSQL(schemaVersion int) (countSQL, shardSQL string) {
	if schemaVersion >= summarySchemaVersion {
		return recapWorksCountSQL, recapsSitemapSQL
	}
	return recapWorksOnlyCountSQL, recapsOnlySitemapSQL
}

// ---- the guide pages' presence probes ---------------------------------------

// The guide pages' presence probes: does this work carry the sidecar the page is
// about? ONE indexed point query each (every table below is keyed on work_id -
// see internal/build's DDL), asked BEFORE the page is composed.
//
// It is what keeps the 404 path cheap. Almost every work in the catalogue
// carries no sidecar at all, so almost every request to these routes - a stale
// link, a crawler walking a guessed URL, a bot probing the shape - ends in a
// 404, and without a probe each one first paid workDetail's 6+4N-query cascade
// to learn there was nothing to render.
//
// The recap probe reuses ONE bound parameter across its two EXISTS clauses
// (`?1`), so both version variants take exactly one argument and the caller
// needs no branch of its own.
const (
	recapGuideExistsSQL = `SELECT EXISTS(SELECT 1 FROM recaps WHERE work_id=?1) ` +
		`OR EXISTS(SELECT 1 FROM recap_summaries WHERE work_id=?1)`
	// recapGuideExistsOnlySQL is that same question on an artifact that predates
	// recap_summaries (schema_version 2), where naming the second table would not
	// parse - the sitemap family's own pair of spellings, one work at a time.
	recapGuideExistsOnlySQL = `SELECT EXISTS(SELECT 1 FROM recaps WHERE work_id=?1)`
	characterGuideExistsSQL = `SELECT EXISTS(SELECT 1 FROM characters WHERE work_id=?1)`
)

// recapGuideProbeSQL picks the recap probe's spelling from the artifact's
// schema_version, in the same style (and for the same reason) as the sitemap
// family's recapSitemapSQL: a pure function of the version, so the probe and the
// listing agree about which tables a recap page can be built from.
func recapGuideProbeSQL(schemaVersion int) string {
	if schemaVersion >= summarySchemaVersion {
		return recapGuideExistsSQL
	}
	return recapGuideExistsOnlySQL
}

// HasRecapPage reports whether a recap page exists for this work id. False - with
// no query at all - below the sidecar tables' version, where the answer is "no
// page" for every work in the artifact.
//
// A work id the catalogue does not hold answers false too, since nothing can
// reference it: the probe therefore covers "no such work" as well, which is why
// it can stand in front of the workDetail cascade rather than beside it.
func (s *DB) HasRecapPage(workID string) (bool, error) {
	return s.sidecarExists(recapGuideProbeSQL(s.schemaVersion), workID)
}

// HasCharacterPage is its sibling for the character guide.
func (s *DB) HasCharacterPage(workID string) (bool, error) {
	return s.sidecarExists(characterGuideExistsSQL, workID)
}

func (s *DB) sidecarExists(query, workID string) (bool, error) {
	if s.schemaVersion < sidecarSchemaVersion {
		return false, nil
	}
	var exists bool
	if err := s.db.QueryRow(query, workID).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}
