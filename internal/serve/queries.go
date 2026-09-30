package serve

import (
	"database/sql"
	"sort"
	"strconv"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// scalarInt runs a query expected to return a single integer (COUNT, etc.).
func (s *snapshot) scalarInt(query string, args ...any) (int, error) {
	var n int
	err := s.db.QueryRow(query, args...).Scan(&n)
	return n, err
}

// scanIDs collects a single string column into a slice.
func scanIDs(rows *sql.Rows) ([]string, error) {
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// latestSeriesCap is the per-series diversity cap for the latest-works list: a
// bulk import shares one added_at date, so without a cap the title tie-break
// clusters one series' volumes and fills the whole grid with them.
const latestSeriesCap = 2

// latestWorks returns up to limit work cards ordered by added_at DESC (NULLS
// LAST), then title, with at most latestSeriesCap works from any one series
// (keyed by the work's first series membership, the one the card carries).
// The key is that membership's ordering FAMILY - its ordering_of where it is a
// variant, else its own id (model.Series.OrderingPrimary) - so two reading
// orders of one franchise count as ONE series: a prequel that sits only in the
// chronological variant shares the cap with the primary's volumes rather than
// opening a bucket of its own.
// Works with no series are each their own bucket and are never capped. It
// over-fetches candidate rows so capped skips still leave enough to fill the
// page; the SQL ordering (id as the final tie-break) keeps the walk
// deterministic.
//
// It runs in TWO phases because the cap only needs to know each candidate's
// series: phase one resolves just that over the ~200 candidates, phase two
// builds full cards for the <=limit survivors. Building cards for every
// candidate would mean authors, series and covers for 200 works to return 12 -
// on the home page, the most-hit endpoint there is.
func (s *snapshot) latestWorks(limit int) ([]*workCard, error) {
	fetch := limit * 4
	if fetch < 200 {
		fetch = 200
	}
	rows, err := s.db.Query(
		`SELECT id FROM works ORDER BY (added_at IS NULL) ASC, added_at DESC, title ASC, id ASC LIMIT ?`, fetch)
	if err != nil {
		return nil, err
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return nil, err
	}
	series, err := s.firstSeriesByWork(ids)
	if err != nil {
		return nil, err
	}
	winners := make([]string, 0, limit)
	perSeries := map[string]int{}
	for _, id := range ids {
		if len(winners) == limit {
			break
		}
		if sr := series[id]; sr != nil {
			family := (&model.Series{ID: sr.ID, OrderingOf: sr.OrderingOf}).OrderingPrimary()
			if perSeries[family] >= latestSeriesCap {
				continue
			}
			perSeries[family]++
		}
		winners = append(winners, id)
	}
	return s.cards(winners)
}

// cards builds work cards for the given ids, preserving order and skipping ids
// with no work. It resolves the whole set in a fixed number of queries
// (cardsByID) rather than four per id: the caller's list length is the size of a
// person's or series' credit list, which is unbounded.
func (s *snapshot) cards(ids []string) ([]*workCard, error) {
	byID, err := s.cardsByID(ids)
	if err != nil {
		return nil, err
	}
	out := make([]*workCard, 0, len(ids))
	for _, id := range ids {
		if wc := byID[id]; wc != nil {
			out = append(out, wc)
		}
	}
	return out, nil
}

// ---- work detail ------------------------------------------------------------

type workXref struct {
	Wikidata    string   `json:"wikidata,omitempty"`
	Openlibrary string   `json:"openlibrary,omitempty"`
	Goodreads   string   `json:"goodreads,omitempty"`
	ISBN        []string `json:"isbn,omitempty"`
}

type recordingDetail struct {
	ID            string         `json:"id"`
	Narrators     []personRef    `json:"narrators"`
	Abridged      bool           `json:"abridged,omitempty"`
	RuntimeMin    int            `json:"runtime_min,omitempty"`
	ReleaseDate   string         `json:"release_date,omitempty"`
	Publisher     string         `json:"publisher,omitempty"`
	ASIN          []asinRef      `json:"asin"`
	ISBN          []string       `json:"isbn"`
	PurchaseLinks []purchaseLink `json:"purchase_links,omitempty"`
	CoverURL      string         `json:"cover_url,omitempty"`
	ChapterCount  int            `json:"chapter_count"`
}

type asinRef struct {
	Region string `json:"region"`
	ASIN   string `json:"asin"`
}

type positionOut struct {
	Chapter int `json:"chapter"`
}

type characterXref struct {
	Wikidata  string `json:"wikidata,omitempty"`
	Goodreads string `json:"goodreads,omitempty"`
}

type characterOut struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Aliases     []string       `json:"aliases,omitempty"`
	Role        string         `json:"role,omitempty"`
	Reveal      positionOut    `json:"reveal"`
	Description string         `json:"description,omitempty"`
	Xref        *characterXref `json:"xref,omitempty"`
}

type recapOut struct {
	Through positionOut `json:"through"`
	Scope   string      `json:"scope,omitempty"`
	Text    string      `json:"text"`
}

// recapSummaryOut is the per-work whole-book summary: a one-paragraph refresher
// and a plain statement of how the book ends. Both fields are optional.
type recapSummaryOut struct {
	InShort string `json:"in_short,omitempty"`
	Ending  string `json:"ending,omitempty"`
}

// descriptionOut is the community SPOILER-FREE description - the paragraph a
// stranger reads before deciding to listen.
//
// It is an OBJECT under its own key rather than a string in workDetail's
// `description`, and the two are deliberately not merged: `description` is the
// CC0 work record's own field, this text is CC BY-SA, and one JSON key carrying
// either would erase the license boundary the schema keeps structural - a
// consumer republishing the text has to know which one it got. The license rides
// with the text for the same reason (the guide pages otherwise hardcode it).
type descriptionOut struct {
	Text    string `json:"text"`
	License string `json:"license,omitempty"`
}

type workDetail struct {
	ID             string      `json:"id"`
	Title          string      `json:"title"`
	Subtitle       string      `json:"subtitle,omitempty"`
	Authors        []personRef `json:"authors"`
	Language       string      `json:"language"`
	FirstPublished string      `json:"first_published,omitempty"`
	Description    string      `json:"description,omitempty"`
	Genres         []string    `json:"genres,omitempty"`
	Series         []seriesRef `json:"series"`
	// TranslationOf is the work(s) this one translates, and Translations the
	// works that translate it - the two directions of the translations table,
	// each in work id order and omitted when empty (or the artifact predates
	// languagesSchemaVersion). A translated omnibus names every original it
	// collects, so TranslationOf is a list even though it almost always holds one.
	TranslationOf []workTranslation `json:"translation_of,omitempty"`
	Translations  []workTranslation `json:"translations,omitempty"`
	Xref          *workXref         `json:"xref,omitempty"`
	Recordings    []recordingDetail `json:"recordings"`
	Characters    []characterOut    `json:"characters,omitempty"`
	Recaps        []recapOut        `json:"recaps,omitempty"`
	RecapSummary  *recapSummaryOut  `json:"recap_summary,omitempty"`
	// CommunityDescription is the CC BY-SA spoiler-free description. Named apart
	// from Description above, which is the CC0 work record's own field - see
	// descriptionOut.
	CommunityDescription *descriptionOut `json:"community_description,omitempty"`
}

// workDetail returns the full work document, or (nil, nil) when absent.
func (s *snapshot) workDetail(id string) (*workDetail, error) {
	var d workDetail
	var subtitle, firstPub, desc, wiki, ol, gr sql.NullString
	err := s.db.QueryRow(
		`SELECT id, title, subtitle, language, first_published, description, wikidata, openlibrary, goodreads FROM works WHERE id=?`, id).
		Scan(&d.ID, &d.Title, &subtitle, &d.Language, &firstPub, &desc, &wiki, &ol, &gr)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d.Subtitle = subtitle.String
	d.FirstPublished = firstPub.String
	d.Description = desc.String

	if d.Authors, err = s.authorsOf(id); err != nil {
		return nil, err
	}
	if d.Genres, err = s.workGenres(id); err != nil {
		return nil, err
	}
	if d.Series, err = s.seriesOf(id); err != nil {
		return nil, err
	}
	if d.TranslationOf, d.Translations, err = s.workTranslations(id); err != nil {
		return nil, err
	}

	isbns, err := s.workISBNs(id)
	if err != nil {
		return nil, err
	}
	if wiki.String != "" || ol.String != "" || gr.String != "" || len(isbns) > 0 {
		d.Xref = &workXref{Wikidata: wiki.String, Openlibrary: ol.String, Goodreads: gr.String, ISBN: isbns}
	}

	if d.Recordings, err = s.recordingsOf(id); err != nil {
		return nil, err
	}
	if d.Characters, err = s.charactersOf(id); err != nil {
		return nil, err
	}
	if d.Recaps, err = s.recapsOf(id); err != nil {
		return nil, err
	}
	if d.RecapSummary, err = s.recapSummaryOf(id); err != nil {
		return nil, err
	}
	if d.CommunityDescription, err = s.communityDescriptionOf(id); err != nil {
		return nil, err
	}
	return &d, nil
}

// workForABS returns just the slice of a work absBooksFor consumes: the work
// row, authors, series, print ISBNs, recordings (with narrators/asins/isbns but
// NO chapter count) and the community DESCRIPTION - and none of the
// characters/recaps/recap-summary sidecars. absSearch calls it once per candidate
// on the public /abs/search hot path, so it deliberately skips workDetail's ~100+
// discarded round-trips; the description is the one sidecar it carries at all,
// because ABS displays a description and ours is own-words rather than scraped.
//
// genresByWork and descriptionsByWork are the BATCHED maps absSearch resolves
// for the whole candidate set up front - one query each - so this runs no
// per-work query for either. That is the point of passing them: a point lookup
// per candidate is still up to absMaxMatches sequential round trips on an
// unauthenticated public endpoint.
// Returns (nil, nil) when the work is absent.
func (s *snapshot) workForABS(id string, genresByWork map[string][]string, descriptionsByWork map[string]*descriptionOut) (*workDetail, error) {
	var d workDetail
	var subtitle, firstPub, desc sql.NullString
	err := s.db.QueryRow(
		`SELECT id, title, subtitle, language, first_published, description FROM works WHERE id=?`, id).
		Scan(&d.ID, &d.Title, &subtitle, &d.Language, &firstPub, &desc)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d.Subtitle = subtitle.String
	d.FirstPublished = firstPub.String
	d.Description = desc.String

	if d.Authors, err = s.authorsOf(id); err != nil {
		return nil, err
	}
	d.Genres = genresByWork[id]
	if d.Series, err = s.seriesOf(id); err != nil {
		return nil, err
	}
	// absBooksFor only reads the work's print ISBN off Xref; skip the other xref
	// identifiers entirely.
	isbns, err := s.workISBNs(id)
	if err != nil {
		return nil, err
	}
	if len(isbns) > 0 {
		d.Xref = &workXref{ISBN: isbns}
	}
	if d.Recordings, err = s.recordingsBase(id); err != nil {
		return nil, err
	}
	d.CommunityDescription = descriptionsByWork[id]
	return &d, nil
}

// sidecarSchemaVersion is the artifact schema_version that first carried the
// characters/recaps tables. A newer binary may briefly serve an older release,
// so the sidecar queries no-op below it rather than probing for the tables.
const sidecarSchemaVersion = 2

// summarySchemaVersion is the artifact schema_version that first carried the
// recap_summaries table (per-work in_short / ending). A newer binary serving an
// older (v2) release must degrade to "no summary", so the query no-ops below
// this version rather than probing for the table.
const summarySchemaVersion = 3

// genresSchemaVersion is the artifact schema_version that first carried the
// work_genres table. A newer binary serving an older (v3 or earlier) release
// must degrade to "no genres", so the query no-ops below this version rather
// than probing for the table.
const genresSchemaVersion = 4

// redirectSchemaVersion is the artifact schema_version that first carried the
// redirects table (the slug tombstones). A newer binary serving an older release
// must degrade to "no redirects" - a retired slug simply 404s as it did before
// the mechanism existed - so the gate is applied ONCE at load (see
// snapshot.loadStats, which only asks the table anything at or above this
// version) rather than by probing for the table per request.
const redirectSchemaVersion = 5

// descriptionSchemaVersion is the artifact schema_version that first carried the
// work_descriptions table (the community spoiler-free description). A newer
// binary serving an older release must degrade to "no description" - the pages
// then compose the fact sentence they always did - so the query no-ops below
// this version rather than probing for the table, and an artifact CLAIMING it
// without the table fails to open (loadStats, the redirects precedent).
const descriptionSchemaVersion = 6

// languagesSchemaVersion is the artifact schema_version that first carried the
// LANGUAGES layer: the translations table (translation_of links, works and
// series), the series table's derived language, ordering and ordering_of
// columns, and idx_works_language. A newer binary serving an older release must
// degrade to "no data" - no translation lists, no series language or ordering
// family, no stats census, series memberships in plain id order - so every read
// of those gates on this version, and an artifact CLAIMING it without the table
// or the columns fails to open (loadStats, the redirects precedent).
const languagesSchemaVersion = 7

const (
	// anyRedirectSQL asks whether the tombstone table holds anything, once per
	// snapshot (see snapshot.hasRedirects).
	anyRedirectSQL = `SELECT EXISTS(SELECT 1 FROM redirects)`
	// anyDescriptionSQL is its twin for the community descriptions, asked once
	// per snapshot for the same two jobs: settle snapshot.hasDescriptions, and
	// prove the table a version 6 artifact claims actually exists.
	anyDescriptionSQL = `SELECT EXISTS(SELECT 1 FROM work_descriptions)`
	// descriptionOfSQL reads ONE work's description off the table's primary key.
	descriptionOfSQL = `SELECT text, license FROM work_descriptions WHERE work_id=?`
	// redirectTargetSQL resolves one retired slug in one namespace. It reads the
	// artifact's primary key, so it is a point lookup - which is what lets it sit
	// on the miss path of every id route without costing anything measurable.
	redirectTargetSQL = `SELECT new_slug FROM redirects WHERE kind=? AND old_slug=?`

	// anyTranslationSQL settles snapshot.hasTranslations once per snapshot and
	// proves the table a version 7 artifact claims.
	anyTranslationSQL = `SELECT EXISTS(SELECT 1 FROM translations)`
	// seriesShapeSQL proves the three series columns a version 7 artifact claims -
	// every one the gated reads name - and reads no row (LIMIT 0): a column that is
	// missing fails the PREPARE, which is the whole check.
	seriesShapeSQL = `SELECT language, ordering, ordering_of FROM series LIMIT 0`
	// orderingPrimariesSQL is the set of series some variant names as its primary,
	// read once per snapshot (snapshot.orderingPrimaries) as a NOT NULL range over
	// idx_series_ordering_of, never a read of the series table.
	orderingPrimariesSQL = `SELECT DISTINCT ordering_of FROM series WHERE ordering_of IS NOT NULL`
	// languageCensusSQL is the stats languages census, read once per snapshot. It
	// walks idx_works_language as a covering index; only the handful of result
	// rows are sorted.
	languageCensusSQL = `SELECT language, COUNT(*) FROM works GROUP BY language ORDER BY COUNT(*) DESC, language`
)

// The translations reads, ONE query per record for both directions of its links:
// the first arm is "what does this translate" (direction 0) and walks the primary
// key (kind, id, target); the second is "what translates this" (direction 1) and
// walks idx_translations_target (kind, target, id), which COVERS it. kind is the
// family spelling, as in the redirects table, and ?1 is the record's id. Each arm
// joins the family it names, so a row pointing at a record the artifact does not
// carry (which pkg/check refuses, but an artifact is data) is dropped rather than
// served as a link to nothing.
//
// The second column is the OTHER record's id read off the translations row (t.target
// in the first arm, t.id in the second) rather than off the joined record, because
// that is what each arm's index is already ordered by: ORDER BY 2 then merges the
// two arms with no sort at all (MERGE (UNION ALL), pinned by
// TestReverseTranslationsUseTheTargetIndex), and each direction comes out in id
// order once translationLinks splits them. A series' derived language is NULL on a
// tie, so its arm COALESCEs it and both families scan three plain strings.
const (
	workTranslationsSQL = `SELECT 0, t.target, w.title, w.language FROM translations t JOIN works w ON w.id = t.target ` +
		`WHERE t.kind = 'works' AND t.id = ?1 ` +
		`UNION ALL SELECT 1, t.id, w.title, w.language FROM translations t JOIN works w ON w.id = t.id ` +
		`WHERE t.kind = 'works' AND t.target = ?1 ORDER BY 2`
	seriesTranslationsSQL = `SELECT 0, t.target, s.name, COALESCE(s.language, '') FROM translations t JOIN series s ON s.id = t.target ` +
		`WHERE t.kind = 'series' AND t.id = ?1 ` +
		`UNION ALL SELECT 1, t.id, s.name, COALESCE(s.language, '') FROM translations t JOIN series s ON s.id = t.id ` +
		`WHERE t.kind = 'series' AND t.target = ?1 ORDER BY 2`
)

// workTranslation is one end of a work's translation link: the other work, its
// title and its language tag (always present - every work states one).
type workTranslation struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Language string `json:"language"`
}

// seriesTranslation is one end of a series' translation link. Language is the
// other series' DERIVED language, omitted where its members tie.
type seriesTranslation struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Language string `json:"language,omitempty"`
}

// translationLinks runs one family's translations query for id and splits it by
// direction: of is what id translates, by is what translates id, each in id order.
// mk builds the family's own shape from (id, title or name, language). It answers
// nil, nil without a query while the artifact holds no translation at all (which
// includes every artifact older than languagesSchemaVersion).
func translationLinks[T any](s *snapshot, query, id string, mk func(id, label, language string) T) (of, by []T, err error) {
	if !s.hasTranslations {
		return nil, nil, nil
	}
	rows, err := s.db.Query(query, id)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var dir int
		var other, label, lang string
		if err := rows.Scan(&dir, &other, &label, &lang); err != nil {
			return nil, nil, err
		}
		if dir == 0 {
			of = append(of, mk(other, label, lang))
		} else {
			by = append(by, mk(other, label, lang))
		}
	}
	return of, by, rows.Err()
}

// workTranslations returns both directions of a work's translation links.
func (s *snapshot) workTranslations(workID string) (of, by []workTranslation, err error) {
	return translationLinks(s, workTranslationsSQL, workID, func(id, title, lang string) workTranslation {
		return workTranslation{ID: id, Title: title, Language: lang}
	})
}

// seriesTranslations is workTranslations for a series.
func (s *snapshot) seriesTranslations(seriesID string) (of, by []seriesTranslation, err error) {
	return translationLinks(s, seriesTranslationsSQL, seriesID, func(id, name, lang string) seriesTranslation {
		return seriesTranslation{ID: id, Name: name, Language: lang}
	})
}

// redirectTarget returns the live slug that the retired slug id now stands for
// in namespace kind, or "" when nothing does - including on any artifact older
// than redirectSchemaVersion, and without a query at all while the table is
// empty, which it is in every release until the first duplicate-merge lands.
//
// ONE lookup is always enough: pkg/check refuses a table where a target is
// itself a source, and the writer collapses chains as they are recorded, so
// following the answer further could only ever return the same slug (see
// model.Redirects).
func (s *snapshot) redirectTarget(kind model.RedirectKind, id string) (string, error) {
	if !s.hasRedirects {
		return "", nil
	}
	var to string
	err := s.db.QueryRow(redirectTargetSQL, string(kind), id).Scan(&to)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return to, nil
}

// charactersOf returns the per-work character sidecar entries in authored order,
// or nil when the work has none (or the artifact predates the sidecar tables).
func (s *snapshot) charactersOf(workID string) ([]characterOut, error) {
	if s.schemaVersion < sidecarSchemaVersion {
		return nil, nil
	}
	rows, err := s.db.Query(
		`SELECT id, name, role, reveal_chapter, description, wikidata, goodreads FROM characters WHERE work_id=? ORDER BY ord`, workID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []characterOut
	for rows.Next() {
		var c characterOut
		var role, desc, wiki, gr sql.NullString
		if err := rows.Scan(&c.ID, &c.Name, &role, &c.Reveal.Chapter, &desc, &wiki, &gr); err != nil {
			return nil, err
		}
		c.Role, c.Description = role.String, desc.String
		if wiki.String != "" || gr.String != "" {
			c.Xref = &characterXref{Wikidata: wiki.String, Goodreads: gr.String}
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	arows, err := s.db.Query(characterAliasSQL, workID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = arows.Close() }()
	aliases := map[string][]string{}
	for arows.Next() {
		var cid, alias string
		if err := arows.Scan(&cid, &alias); err != nil {
			return nil, err
		}
		aliases[cid] = append(aliases[cid], alias)
	}
	if err := arows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Aliases = aliases[out[i].ID]
	}
	return out, nil
}

// recapsOf returns the per-work recap sidecar entries ordered by position, or
// nil when the work has none (or the artifact predates the sidecar tables).
func (s *snapshot) recapsOf(workID string) ([]recapOut, error) {
	if s.schemaVersion < sidecarSchemaVersion {
		return nil, nil
	}
	rows, err := s.db.Query(
		`SELECT through_chapter, scope, text FROM recaps WHERE work_id=? ORDER BY through_chapter`, workID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []recapOut
	for rows.Next() {
		var r recapOut
		var scope sql.NullString
		if err := rows.Scan(&r.Through.Chapter, &scope, &r.Text); err != nil {
			return nil, err
		}
		r.Scope = scope.String
		out = append(out, r)
	}
	return out, rows.Err()
}

// recapSummaryOf returns the per-work whole-book summary (in_short / ending), or
// nil when the work has none (or the artifact predates the recap_summaries
// table). A row present with both fields empty is treated as no summary.
func (s *snapshot) recapSummaryOf(workID string) (*recapSummaryOut, error) {
	if s.schemaVersion < summarySchemaVersion {
		return nil, nil
	}
	var inShort, ending sql.NullString
	err := s.db.QueryRow(`SELECT in_short, ending FROM recap_summaries WHERE work_id=?`, workID).
		Scan(&inShort, &ending)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if inShort.String == "" && ending.String == "" {
		return nil, nil
	}
	return &recapSummaryOut{InShort: inShort.String, Ending: ending.String}, nil
}

// communityDescriptionOf returns the work's CC BY-SA spoiler-free description,
// or nil when it has none (or the artifact predates the work_descriptions
// table, or the table is empty). One point lookup on the table's primary key,
// which is why it needs no entry in TestServeLookupsAreIndexed - the same shape,
// and the same reason, as recapSummaryOf above.
//
// The hasDescriptions gate (settled once at load, snapshot.hasDescriptions) is
// what keeps it free until the layer has data: the work page, both guide pages
// and the ABS candidates all reach this on ORDINARY 200 paths, so an empty table
// would otherwise cost a round trip per request to learn there is nothing there.
//
// A non-nil result ALWAYS carries text: work_descriptions.text is NOT NULL and
// the schema puts a 200-character floor under it, so unlike its recap_summaries
// neighbour there is no "states nothing" row to filter out. That is the
// invariant the license boundary rests on - a CommunityDescription present means
// CC BY-SA prose is present - so a third state must not be invented here.
func (s *snapshot) communityDescriptionOf(workID string) (*descriptionOut, error) {
	if !s.hasDescriptions {
		return nil, nil
	}
	var text, license string
	err := s.db.QueryRow(descriptionOfSQL, workID).Scan(&text, &license)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &descriptionOut{Text: text, License: license}, nil
}

// descriptionsForWorks fetches the community description of every given work in
// ONE query, mirroring genresForWorks: /abs/search resolves the whole candidate
// set up front so workForABS makes no per-candidate read on the public hot path.
// Up to absMaxMatches sequential point lookups is exactly the N+1 the batched
// genre map two lines above it exists to avoid, and the two are resolved
// together. An empty input, an artifact predating the table, or an empty table
// yields an empty map.
func (s *snapshot) descriptionsForWorks(workIDs []string) (map[string]*descriptionOut, error) {
	out := make(map[string]*descriptionOut, len(workIDs))
	if len(workIDs) == 0 || !s.hasDescriptions {
		return out, nil
	}
	placeholders := make([]string, len(workIDs))
	args := make([]any, len(workIDs))
	for i, id := range workIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	rows, err := s.db.Query(
		`SELECT work_id, text, license FROM work_descriptions WHERE work_id IN (`+
			strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var wid, text, license string
		if err := rows.Scan(&wid, &text, &license); err != nil {
			return nil, err
		}
		out[wid] = &descriptionOut{Text: text, License: license}
	}
	return out, rows.Err()
}

// seriesOfSQL is a work's series memberships, for the work detail, in the order
// membershipColumns chooses - the order firstSeriesByWorkSQL cards by, so series[0]
// of the work page, its JSON-LD isPartOf and the card agree about which series a
// work is in.
func seriesOfSQL(orderings bool) string {
	orderingOf, order := membershipColumns(orderings)
	return `SELECT s.id, s.name, sw.position, ` + orderingOf + ` FROM series_works sw JOIN series s ON s.id = sw.series_id ` +
		`WHERE sw.work_id=? ORDER BY ` + order
}

func (s *snapshot) seriesOf(workID string) ([]seriesRef, error) {
	rows, err := s.db.Query(seriesOfSQL(s.hasOrderings()), workID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []seriesRef{}
	for rows.Next() {
		var sr seriesRef
		var orderingOf sql.NullString
		if err := rows.Scan(&sr.ID, &sr.Name, &sr.Position, &orderingOf); err != nil {
			return nil, err
		}
		sr.OrderingOf = orderingOf.String
		out = append(out, sr)
	}
	return out, rows.Err()
}

// workGenres returns a work's normalized genre slugs ascending, or nil when the
// work has none (or the artifact predates the work_genres table). Genres are a
// set, so genre is the order (checkGenresSorted pins the authored files to the
// same ascending order).
func (s *snapshot) workGenres(workID string) ([]string, error) {
	if s.schemaVersion < genresSchemaVersion {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT genre FROM work_genres WHERE work_id=? ORDER BY genre`, workID)
	if err != nil {
		return nil, err
	}
	return scanIDs(rows)
}

func (s *snapshot) workISBNs(workID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT isbn FROM work_isbns WHERE work_id=? ORDER BY isbn`, workID)
	if err != nil {
		return nil, err
	}
	return scanIDs(rows)
}

// recordingsBase fetches a work's recordings with their narrators, ASINs,
// ISBNs and derived purchase links, but WITHOUT the per-recording chapter
// count. recordingsOf adds the count on top; the ABS path (workForABS) reuses
// this directly, since it never reads the count - a hot public endpoint should
// not run a COUNT(*) per recording it discards. The purchase links stay here
// even though ABS discards those too: they are pure in-memory derivation,
// nanoseconds beside this function's per-recording queries, and deriving at
// construction means no future recordingDetail surface can silently omit them
// (omitempty would hide the gap).
func (s *snapshot) recordingsBase(workID string) ([]recordingDetail, error) {
	rows, err := s.db.Query(
		`SELECT id, abridged, runtime_min, release_date, publisher, cover_url FROM recordings WHERE work_id=? ORDER BY id`, workID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var recs []recordingDetail
	for rows.Next() {
		var rd recordingDetail
		var abridged int
		var runtime sql.NullInt64
		var release, publisher, cover sql.NullString
		if err := rows.Scan(&rd.ID, &abridged, &runtime, &release, &publisher, &cover); err != nil {
			return nil, err
		}
		rd.Abridged = abridged != 0
		rd.RuntimeMin = int(runtime.Int64)
		rd.ReleaseDate = release.String
		rd.Publisher = publisher.String
		rd.CoverURL = cover.String
		recs = append(recs, rd)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range recs {
		rid := recs[i].ID
		if recs[i].Narrators, err = s.narratorsOf(workID, rid); err != nil {
			return nil, err
		}
		if recs[i].ASIN, err = s.asinsOf(workID, rid); err != nil {
			return nil, err
		}
		if recs[i].ISBN, err = s.recordingISBNs(workID, rid); err != nil {
			return nil, err
		}
		recs[i].PurchaseLinks = derivedPurchaseLinks(recs[i].ASIN, recs[i].ISBN)
	}
	return recs, nil
}

func (s *snapshot) recordingsOf(workID string) ([]recordingDetail, error) {
	recs, err := s.recordingsBase(workID)
	if err != nil {
		return nil, err
	}
	for i := range recs {
		if err = s.db.QueryRow(recordingCountSQL, workID, recs[i].ID).
			Scan(&recs[i].ChapterCount); err != nil {
			return nil, err
		}
	}
	return recs, nil
}

// The per-recording queries. GET /works/{id} runs all three once per recording,
// so their plans are the ones the covering indexes exist for; they are named
// constants so the index guard checks the real text.
const (
	narratorsOfSQL = `SELECT p.id, p.name FROM recording_narrators rn JOIN people p ON p.id = rn.person_id ` +
		`WHERE rn.work_id=? AND rn.recording_id=? ORDER BY rn.ord`
	asinsOfSQL        = `SELECT region, asin FROM recording_asins WHERE work_id=? AND recording_id=? ORDER BY region, asin`
	recordingISBNsSQL = `SELECT isbn FROM recording_isbns WHERE work_id=? AND recording_id=? ORDER BY isbn`
	characterAliasSQL = `SELECT character_id, alias FROM character_aliases WHERE work_id=? ORDER BY character_id, ord`
	recordingCountSQL = `SELECT COUNT(*) FROM chapters WHERE work_id=? AND recording_id=?`
)

func (s *snapshot) narratorsOf(workID, rid string) ([]personRef, error) {
	rows, err := s.db.Query(narratorsOfSQL, workID, rid)
	if err != nil {
		return nil, err
	}
	return scanPersonRefs(rows)
}

func (s *snapshot) asinsOf(workID, rid string) ([]asinRef, error) {
	rows, err := s.db.Query(asinsOfSQL, workID, rid)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []asinRef{}
	for rows.Next() {
		var a asinRef
		if err := rows.Scan(&a.Region, &a.ASIN); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *snapshot) recordingISBNs(workID, rid string) ([]string, error) {
	rows, err := s.db.Query(recordingISBNsSQL, workID, rid)
	if err != nil {
		return nil, err
	}
	out, err := scanIDs(rows)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}

// ---- chapters ---------------------------------------------------------------

type chapterOut struct {
	Title    string `json:"title"`
	StartMS  int64  `json:"start_ms"`
	LengthMS int64  `json:"length_ms"`
}

func (s *snapshot) chapters(workID, rid string) ([]chapterOut, error) {
	rows, err := s.db.Query(
		`SELECT title, start_ms, length_ms FROM chapters WHERE work_id=? AND recording_id=? ORDER BY idx`, workID, rid)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []chapterOut{}
	for rows.Next() {
		var c chapterOut
		if err := rows.Scan(&c.Title, &c.StartMS, &c.LengthMS); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---- person -----------------------------------------------------------------

type narratedEntry struct {
	Work        *workCard `json:"work"`
	RecordingID string    `json:"recording_id"`
}

// personDetail is one person plus a PAGE of each credit list. The totals are
// the unpaged counts, so a client can tell that it is seeing a slice and page
// through it; limit/offset echo the window that was applied. Both lists use the
// same window (a person page shows both side by side).
//
// The lists are paginated because a person's credits are unbounded: corporate
// credits ("Full Cast", "Audible Studios") already narrate ~2,000 works at 9.5k
// works and grow with the catalogue. The new fields are additive - existing
// consumers that read only authored/narrated keep working.
type personDetail struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	SortName      string          `json:"sort_name,omitempty"`
	Authored      []*workCard     `json:"authored"`
	Narrated      []narratedEntry `json:"narrated"`
	AuthoredTotal int             `json:"authored_total"`
	NarratedTotal int             `json:"narrated_total"`
	Limit         int             `json:"limit"`
	Offset        int             `json:"offset"`
}

// The person page's four queries. Each total counts over exactly the join its
// page query pages, so the total and the list can never disagree; they are named
// constants so the index guard EXPLAINs what actually runs.
const (
	authoredTotalSQL = `SELECT COUNT(*) FROM work_authors wa JOIN works w ON w.id = wa.work_id WHERE wa.person_id=?`
	narratedTotalSQL = `SELECT COUNT(*) FROM recording_narrators rn JOIN works w ON w.id = rn.work_id WHERE rn.person_id=?`

	authoredPageSQL = `SELECT wa.work_id FROM work_authors wa JOIN works w ON w.id = wa.work_id WHERE wa.person_id=? ` +
		`ORDER BY w.title, wa.work_id LIMIT ? OFFSET ?`
	narratedPageSQL = `SELECT rn.work_id, rn.recording_id FROM recording_narrators rn JOIN works w ON w.id = rn.work_id ` +
		`WHERE rn.person_id=? ORDER BY w.title, rn.work_id, rn.recording_id LIMIT ? OFFSET ?`
)

// person returns one page of a person's authored and narrated credits. limit
// and offset are applied to each list independently (both are ordered by work
// title, then id, so the window is stable across requests on one artifact).
func (s *snapshot) person(id string, limit, offset int) (*personDetail, error) {
	var d personDetail
	var sortName sql.NullString
	err := s.db.QueryRow(`SELECT id, name, sort_name FROM people WHERE id=?`, id).Scan(&d.ID, &d.Name, &sortName)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d.SortName = sortName.String
	d.Limit, d.Offset = limit, offset

	// The totals count over the SAME join as the page queries, so "showing 4 of
	// 5" can never be reported when the 5th row references a work the artifact
	// does not carry: such a row is dropped from the page and must not be counted
	// in the total either.
	if d.AuthoredTotal, err = s.scalarInt(authoredTotalSQL, id); err != nil {
		return nil, err
	}
	if d.NarratedTotal, err = s.scalarInt(narratedTotalSQL, id); err != nil {
		return nil, err
	}

	rows, err := s.db.Query(authoredPageSQL, id, limit, offset)
	if err != nil {
		return nil, err
	}
	authoredIDs, err := scanIDs(rows)
	if err != nil {
		return nil, err
	}
	if d.Authored, err = s.cards(authoredIDs); err != nil {
		return nil, err
	}
	if d.Authored == nil {
		d.Authored = []*workCard{}
	}

	nrows, err := s.db.Query(narratedPageSQL, id, limit, offset)
	if err != nil {
		return nil, err
	}
	credits, err := scanPairs(nrows, func(workID, rid string) narratedCredit {
		return narratedCredit{workID: workID, recordingID: rid}
	})
	if err != nil {
		return nil, err
	}
	narratedIDs := make([]string, len(credits))
	for i, c := range credits {
		narratedIDs[i] = c.workID
	}
	byID, err := s.cardsByID(narratedIDs)
	if err != nil {
		return nil, err
	}
	d.Narrated = []narratedEntry{}
	for _, c := range credits {
		if card := byID[c.workID]; card != nil {
			d.Narrated = append(d.Narrated, narratedEntry{Work: card, RecordingID: c.recordingID})
		}
	}
	return &d, nil
}

// narratedCredit is one row of a narrator's credit list: which recording of
// which work. The same work appears once per recording the person narrated.
type narratedCredit struct{ workID, recordingID string }

// seriesMember is one row of a series' membership: a work and its position
// string ("2", "2.5", "1-3.5").
type seriesMember struct{ workID, position string }

// scanPairs collects two-column rows through mk and closes rows. The membership
// lists it reads are resolved to work cards in one batch afterwards, so the row
// set is materialized first rather than built card-by-card inside the scan.
func scanPairs[T any](rows *sql.Rows, mk func(first, second string) T) ([]T, error) {
	defer func() { _ = rows.Close() }()
	var out []T
	for rows.Next() {
		var a, b string
		if err := rows.Scan(&a, &b); err != nil {
			return nil, err
		}
		out = append(out, mk(a, b))
	}
	return out, rows.Err()
}

// ---- series -----------------------------------------------------------------

type seriesEntry struct {
	Position string    `json:"position"`
	Work     *workCard `json:"work"`
}

// seriesDetail is a series plus its member works in position order. WorksTotal
// is the unpaged membership count and Limit/Offset echo the window that was
// applied (Limit 0 = the whole series, the default - see snapshot.series). The
// three fields are additive; a consumer reading only works is unaffected.
//
// The LANGUAGES fields are additive and all omitempty, and every one is absent on
// an artifact older than languagesSchemaVersion: Language is DERIVED at build
// (the strict plurality of the members' primary subtags, absent on a tie), Ordering and
// OrderingOf are what the record states, TranslationOf/Translations are the two
// directions of the series' translation links, and Orderings is the whole
// ordering FAMILY this series belongs to - the primary first, then its variants
// in id order - present on the primary and on every variant alike, and omitted
// when the series has no variant ordering at all.
type seriesDetail struct {
	ID            string              `json:"id"`
	Name          string              `json:"name"`
	Language      string              `json:"language,omitempty"`
	Ordering      string              `json:"ordering,omitempty"`
	OrderingOf    string              `json:"ordering_of,omitempty"`
	Authors       []personRef         `json:"authors"`
	Works         []seriesEntry       `json:"works"`
	WorksTotal    int                 `json:"works_total"`
	Limit         int                 `json:"limit"`
	Offset        int                 `json:"offset"`
	TranslationOf []seriesTranslation `json:"translation_of,omitempty"`
	Translations  []seriesTranslation `json:"translations,omitempty"`
	Orderings     []seriesOrdering    `json:"orderings,omitempty"`
}

// seriesOrdering is one member of a series' ordering family: the series and the
// reading order its positions state (omitted where the record states none - a
// primary need not say it is the publication order).
type seriesOrdering struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Ordering string `json:"ordering,omitempty"`
}

// The series detail queries. seriesWorksSQL joins works so WorksTotal counts
// exactly the members that can be rendered: a membership row pointing at a work
// the artifact does not carry is dropped from the list, so counting it in the
// total would report a page shorter than it claims.
const (
	// seriesNameSQL is the header without the id, for the caller that already
	// holds it (see seriesName). Reading a column back to discard it is a scan
	// target a reader has to stop and account for.
	seriesNameSQL    = `SELECT name FROM series WHERE id=?`
	seriesAuthorsSQL = `SELECT p.id, p.name FROM series_authors sa JOIN people p ON p.id = sa.person_id ` +
		`WHERE sa.series_id=? ORDER BY sa.ord`
	seriesWorksSQL = `SELECT sw.work_id, sw.position FROM series_works sw JOIN works w ON w.id = sw.work_id ` +
		`WHERE sw.series_id=?`
	// seriesOrderingFamilySQL reads a whole ordering family from its PRIMARY's id
	// (?1): the primary by its primary key and every variant naming it through
	// idx_series_ordering_of, the primary first (its ordering_of is NULL -
	// pkg/check keeps a family one hop deep), then the variants in id order.
	seriesOrderingFamilySQL = `SELECT id, name, ordering FROM series WHERE id = ?1 OR ordering_of = ?1 ` +
		`ORDER BY (ordering_of IS NOT NULL), id`
)

// seriesHeaderSQL is the series page's header row. A languages-era artifact
// carries three more columns on the same primary-key row; an older one has none of
// them, so its text selects NULL in their place and both scan into one shape.
func seriesHeaderSQL(languages bool) string {
	if !languages {
		return `SELECT id, name, NULL, NULL, NULL FROM series WHERE id=?`
	}
	return `SELECT id, name, language, ordering, ordering_of FROM series WHERE id=?`
}

// seriesName reads a series' name and nothing else, reporting whether the series
// exists at all. It is what a caller that does not want the MEMBERSHIP asks (the
// watch feed, which then selects the few members that can be news):
// snapshot.series materializes a card per member, which is the right answer for
// the series page and a whole catalogue read for a feed that keeps a handful of
// them.
//
// It hands back the name rather than a seriesDetail because a seriesDetail with
// two of its fields filled is a lie about the other four - a caller has no way
// to tell an empty membership from an unread one.
func (s *snapshot) seriesName(id string) (name string, ok bool, err error) {
	switch err := s.db.QueryRow(seriesNameSQL, id).Scan(&name); {
	case err == sql.ErrNoRows:
		return "", false, nil
	case err != nil:
		return "", false, err
	}
	return name, true, nil
}

// watchMember is one series membership the watch feed may report: the series it
// is reported under, the work, its position and the two facts the feed's rule
// turns on, with no card built. seriesName is filled by the caller - the query
// reads one series at a time and already knows which - and is what the emitted
// item is titled under when a work belongs to several watched series.
type watchMember struct {
	seriesName string
	workID     string
	position   string
	addedAt    sql.NullString
}

// watchMembersSQL reads a series' membership WITHOUT resolving a card per
// member: the work id, the position and works.added_at, which is one of the two
// facts watchItem judges a member by (the other, the release date, is read for
// every watched series at once - see snapshot.selectWatchCandidates).
//
// It exists because the feed reads up to 200 series and keeps only what falls
// inside a rolling window (90 days by default). snapshot.series builds a card,
// its authors, its first series membership and its recordings' facts for every
// member, which for a 200-slug request materialized a large part of the
// catalogue and then threw nearly all of it away.
const watchMembersSQL = `SELECT sw.work_id, sw.position, w.added_at ` +
	`FROM series_works sw JOIN works w ON w.id = sw.work_id WHERE sw.series_id = ?`

// watchMembers returns one series' membership with the cards unresolved, each
// member stamped with the series name it is reported under (the caller resolved
// it to reach this id, so nothing re-reads it).
func (s *snapshot) watchMembers(seriesID, seriesName string) ([]watchMember, error) {
	rows, err := s.db.Query(watchMembersSQL, seriesID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []watchMember
	for rows.Next() {
		m := watchMember{seriesName: seriesName}
		if err := rows.Scan(&m.workID, &m.position, &m.addedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// series returns a series with its member works. limit 0 means "all works",
// which is the endpoint's DEFAULT: unlike a person's credit list, series
// membership is bounded by what a series is (175 works at the top of the
// catalogue today), and audiosilo-server's /libraries/{id}/meta composes the
// player's series rail from the full list (workspace CROSS-REPO.md section 17) -
// truncating it by default would silently shorten that rail. Pagination is
// available for a client that wants it, and the member cards are resolved in a
// fixed number of queries either way.
//
// Position ordering is computed in Go (positionStart parses "2.5" and "1-3.5"),
// so a window is applied AFTER sorting, not by SQL.
func (s *snapshot) series(id string, limit, offset int) (*seriesDetail, error) {
	var d seriesDetail
	var lang, ordering, orderingOf sql.NullString
	err := s.db.QueryRow(seriesHeaderSQL(s.schemaVersion >= languagesSchemaVersion), id).
		Scan(&d.ID, &d.Name, &lang, &ordering, &orderingOf)
	d.Language, d.Ordering, d.OrderingOf = lang.String, ordering.String, orderingOf.String
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d.Limit, d.Offset = limit, offset
	if d.TranslationOf, d.Translations, err = s.seriesTranslations(id); err != nil {
		return nil, err
	}
	if d.Orderings, err = s.orderingFamily(&d); err != nil {
		return nil, err
	}

	arows, err := s.db.Query(seriesAuthorsSQL, id)
	if err != nil {
		return nil, err
	}
	if d.Authors, err = scanPersonRefs(arows); err != nil {
		return nil, err
	}

	wrows, err := s.db.Query(seriesWorksSQL, id)
	if err != nil {
		return nil, err
	}
	members, err := scanPairs(wrows, func(workID, pos string) seriesMember {
		return seriesMember{workID: workID, position: pos}
	})
	if err != nil {
		return nil, err
	}
	d.WorksTotal = len(members)
	sort.SliceStable(members, func(i, j int) bool {
		return positionStart(members[i].position) < positionStart(members[j].position)
	})
	if offset > len(members) {
		offset = len(members)
	}
	members = members[offset:]
	if limit > 0 && limit < len(members) {
		members = members[:limit]
	}

	ids := make([]string, len(members))
	for i, m := range members {
		ids[i] = m.workID
	}
	byID, err := s.cardsByID(ids)
	if err != nil {
		return nil, err
	}
	d.Works = []seriesEntry{}
	for _, m := range members {
		if card := byID[m.workID]; card != nil {
			d.Works = append(d.Works, seriesEntry{Position: m.position, Work: card})
		}
	}
	return &d, nil
}

// orderingFamily returns the ordering family d belongs to (see seriesDetail), or
// nil when it has none. Only a variant (one naming a primary) or a series some
// variant names (snapshot.orderingPrimaries) HAS a family, so every other series -
// all of them on an artifact carrying no ordering, and on every artifact older than
// languagesSchemaVersion - is answered without a query.
func (s *snapshot) orderingFamily(d *seriesDetail) ([]seriesOrdering, error) {
	if d.OrderingOf == "" && !s.orderingPrimaries[d.ID] {
		return nil, nil
	}
	// The family's primary is model.Series.OrderingPrimary's answer, asked of the
	// two fields the header read, so "which series heads the family" has one
	// definition across check, repair and serve.
	primary := (&model.Series{ID: d.ID, OrderingOf: d.OrderingOf}).OrderingPrimary()
	rows, err := s.db.Query(seriesOrderingFamilySQL, primary)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []seriesOrdering
	for rows.Next() {
		var o seriesOrdering
		var ordering sql.NullString
		if err := rows.Scan(&o.ID, &o.Name, &ordering); err != nil {
			return nil, err
		}
		o.Ordering = ordering.String
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// A primary alone is no family: the list exists to offer another order.
	if len(out) < 2 {
		return nil, nil
	}
	return out, nil
}

// parsePositionRange reads a series position's numeric span. It is
// model.ParsePositionRange, aliased here under the name this package's callers
// (positionStart's sort key, the coverage browser's gap set, seriespos.go's
// positionsEqual) already read.
//
// The implementation MOVED to pkg/model, beside the schema's position rules, when
// the consumer this comment used to anticipate turned up: internal/audit judges
// range bounds and slot identity with the same grammar, and pkg/model is the leaf
// both packages can reach. The alias stays so there is one call shape in here and
// still exactly one definition anywhere.
var parsePositionRange = model.ParsePositionRange

// positionStart returns the numeric start of a series position string: "2.5"
// -> 2.5, "1-3.5" -> 1, unparseable -> +Inf-ish large so it sorts last. A
// malformed range still sorts by its parseable prefix ("1-garbage" -> 1); only
// a value with no parseable start gets the sort-last sentinel.
//
// HAND-MIRRORED TWIN of `positionStart` in site/src/lib/watch-flat.ts, which
// orders the watching page's cross-series list. This side is the rule of
// record - it orders the series rail every consumer reads - and the sentinel is
// the only deliberate difference (1e18 here, +Infinity there). The same cases
// are pinned on both sides: TestPositionStart in serve_test.go and the
// `positionStart` describe block in watch-flat.test.ts.
func positionStart(pos string) float64 {
	if lo, _, ok := parsePositionRange(pos); ok {
		return lo
	}
	p := strings.TrimSpace(pos)
	if i := strings.IndexByte(p, '-'); i > 0 {
		p = strings.TrimSpace(p[:i])
	}
	f, err := strconv.ParseFloat(p, 64)
	if err != nil {
		return 1e18
	}
	return f
}

// ---- lookup -----------------------------------------------------------------

type lookupResult struct {
	Work        *workCard `json:"work"`
	RecordingID string    `json:"recording_id"`
}

func (s *snapshot) lookup(asin, isbn string) (*lookupResult, error) {
	var workID, rid string
	find := func(query, arg string) (bool, error) {
		err := s.db.QueryRow(query, arg).Scan(&workID, &rid)
		if err == sql.ErrNoRows {
			return false, nil
		}
		return err == nil, err
	}
	found := false
	var err error
	switch {
	case asin != "":
		found, err = find(`SELECT work_id, recording_id FROM recording_asins WHERE asin=? ORDER BY region LIMIT 1`, asin)
	case isbn != "":
		if found, err = find(`SELECT work_id, recording_id FROM recording_isbns WHERE isbn=? LIMIT 1`, isbn); err == nil && !found {
			// Fall back to a print ISBN on the work; point at its first recording.
			var wid string
			e := s.db.QueryRow(`SELECT work_id FROM work_isbns WHERE isbn=? LIMIT 1`, isbn).Scan(&wid)
			if e == nil {
				workID, found = wid, true
				_ = s.db.QueryRow(`SELECT id FROM recordings WHERE work_id=? ORDER BY id LIMIT 1`, wid).Scan(&rid)
			} else if e != sql.ErrNoRows {
				err = e
			}
		}
	}
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	card, err := s.workCard(workID)
	if err != nil {
		return nil, err
	}
	if card == nil {
		return nil, nil
	}
	return &lookupResult{Work: card, RecordingID: rid}, nil
}
