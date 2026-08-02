package importer

import "github.com/kodestar/audiosilo-meta/pkg/model"

// The out* types are the importer's own view of each entity's on-disk shape.
// They exist separately from pkg/model so the importer controls exactly
// which fields are emitted - notably abridged, which is a tri-state pointer here
// (omitted when unknown) rather than a plain bool. Field order is irrelevant:
// every file is run through pkg/canonical before it is written, which sorts
// keys.
//
// OutSource/OutPerson/OutASIN/OutSeriesWork/OutSeries are exported and reused by
// internal/issueform (whose composed records must be byte-identical to a
// hand-authored or imported one). The importer-private outWork/outRecording/
// outChapter stay unexported: issueform emits a richer work/recording shape of
// its own, so only the entities with an identical shape are shared.

// The source types this package stamps are the vocabulary pkg/model ranks - see
// model.TierOfSource. Aliasing rather than re-spelling is what keeps a stamp and
// its trust tier from drifting apart (a re-spelled literal here would silently
// become an unranked reference source).
const (
	licenseCC0     = "CC0-1.0"
	sourceOpenAud  = model.SourceOpenAudibleImport
	sourceLibation = model.SourceLibationImport
	sourceLibex    = model.SourceLibexImport
)

// OutSource is a record's provenance stamp (type/ref/imported_at).
type OutSource struct {
	Type       string `json:"type"`
	Ref        string `json:"ref,omitempty"`
	ImportedAt string `json:"imported_at,omitempty"`
}

// OutPerson is the on-disk person record shape.
type OutPerson struct {
	ID      string      `json:"id"`
	Name    string      `json:"name"`
	License string      `json:"license"`
	Sources []OutSource `json:"sources"`
}

type outWork struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Authors []string `json:"authors"`
	// Credits are the role-qualified contributor credits a source STATED, as
	// (person, role) pairs from the schema's controlled vocabulary. Omitted
	// when the source stated no role - the overwhelming majority of rows.
	Credits  []model.Credit `json:"credits,omitempty"`
	Language string         `json:"language"`
	// Genres are the project's own vocabulary slugs, sorted ascending
	// (checkGenresSorted pins the order). Omitted when the source carried no
	// genre that maps - never a retailer's raw genre strings (LICENSING.md).
	Genres []string `json:"genres,omitempty"`
	// AddedAt is the date this work entered the database, stamped only on the
	// record a run CREATES (never on a merge or an enrichment backfill, which
	// touch records that entered earlier). Same value family as
	// sources[].imported_at: the run's import date, YYYY-MM-DD.
	AddedAt string      `json:"added_at,omitempty"`
	License string      `json:"license"`
	Sources []OutSource `json:"sources"`
}

// OutASIN is a region-scoped ASIN entry on a recording.
type OutASIN struct {
	Region string `json:"region"`
	ASIN   string `json:"asin"`
}

type outChapter struct {
	Title    string `json:"title"`
	StartMS  int64  `json:"start_ms"`
	LengthMS int64  `json:"length_ms"`
}

type outRecording struct {
	ID          string       `json:"id"`
	Work        string       `json:"work"`
	Narrators   []string     `json:"narrators"`
	Abridged    *bool        `json:"abridged,omitempty"`
	Language    string       `json:"language"`
	RuntimeMin  int          `json:"runtime_min,omitempty"`
	ReleaseDate string       `json:"release_date,omitempty"`
	Publisher   string       `json:"publisher,omitempty"`
	ASIN        []OutASIN    `json:"asin,omitempty"`
	ISBN        []string     `json:"isbn,omitempty"`
	CoverURL    string       `json:"cover_url,omitempty"`
	Chapters    []outChapter `json:"chapters,omitempty"`
	// AddedAt is stamped only on a recording the run CREATES; see outWork.
	AddedAt string      `json:"added_at,omitempty"`
	License string      `json:"license"`
	Sources []OutSource `json:"sources"`
}

// OutSeriesWork is one (work, position) membership in a series record.
type OutSeriesWork struct {
	Work     string `json:"work"`
	Position string `json:"position"`
}

// OutSeries is the on-disk series record shape.
type OutSeries struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	Works   []OutSeriesWork `json:"works"`
	License string          `json:"license"`
	Sources []OutSource     `json:"sources"`
}

// Mode selects a run's planning pass. The three passes are disjoint by design,
// and making the choice ONE field is what makes that structural: a run cannot be
// asked for two of them, so there is no combination to police at each layer.
type Mode int

const (
	// ModeCreate is the default: books the catalogue does not have become
	// work/recording/person/series records. See importer.go.
	ModeCreate Mode = iota
	// ModeEnrich is the ASIN-matched enrichment pass: a row whose ASIN the
	// catalogue does not hold is counted and ignored, and a matched row only
	// fills facts the existing records do not have. Nothing is ever created.
	// See enrich.go.
	ModeEnrich
	// ModeRecordingsOnly is the alternate-narration pass: a row is added as a
	// recording under a work the catalogue already holds (or its ASIN merges
	// into a matching sibling recording), and a row whose work is not here is
	// counted and dropped. It never creates a work and never touches a series.
	// See recordings.go.
	ModeRecordingsOnly
)

// boundedByCatalogue reports whether the mode's run is bounded by THIS
// catalogue rather than by the source's - true for enrichment and
// recordings-only, both of which do nothing at all for a row the catalogue does
// not already match.
//
// It is why those two modes report the parse layer's warnings in AGGREGATE:
// their natural input is a large export whose rows are overwhelmingly
// irrelevant here, so a per-row line for each of a million rows the run was
// never going to touch buries the output it exists to produce.
func (m Mode) boundedByCatalogue() bool {
	return m == ModeEnrich || m == ModeRecordingsOnly
}

// Options configures a run of the importer.
type Options struct {
	// DataDir is the data root (contains works/, people/, series/).
	DataDir string
	// ImportDate is the YYYY-MM-DD stamp written to every created record's
	// source.imported_at.
	ImportDate string
	// DryRun plans without writing any files.
	DryRun bool
	// Mode selects the planning pass; the zero value is ModeCreate.
	Mode Mode
}

// Summary is the outcome counts of a run.
type Summary struct {
	NewWorks      int
	NewRecordings int
	NewPeople     int
	NewSeries     int
	// Skipped counts books skipped because their ASIN already exists in the
	// catalog (already-present).
	Skipped int
	// MergedASINs counts re-release ASINs merged into an existing recording (a
	// same-work, same-narrator entry whose only new fact was another ASIN),
	// rather than minting a sibling work or dropping the ASIN.
	MergedASINs int
	// EnrichedWorks / EnrichedRecordings count the existing records a run
	// changed by FILLING facts they did not carry (a record whose every fact was
	// already present is not counted - a run never rewrites a record it did not
	// change). A record the run took over from the bulk mirror is counted as
	// Attested* instead, so the two are disjoint.
	EnrichedWorks      int
	EnrichedRecordings int
	// AttestedWorks / AttestedRecordings count the bulk-mirror-only records a
	// USER-library run took over: the row's stated facts overwrote the mirror's
	// and the run's source entry was appended, so the record is user-attested
	// from now on (LICENSING.md's trust tiers; internal/importer/attest.go).
	// Counted even when no field changed - the attestation itself is the change,
	// and it is what a "libex-only records remaining" metric watches fall. Always
	// 0 for a libex run.
	AttestedWorks      int
	AttestedRecordings int
	// Conflicts counts rows a USER-library run refused because they contradicted
	// what a record already states (see recordingContradicts). The recorded value
	// stands - first writer wins - and this is the "flag for review" half of that
	// rule: the intake bot surfaces the count so a maintainer adjudicates rather
	// than the catalogue churning between two users. Always 0 for a libex run,
	// whose contradictions are ordinary source noise and only warn.
	Conflicts int
	// Credits counts the (person, role) contributor credits a run wrote onto a
	// work, whether it created that work or filled the field on an existing one.
	// It is the counter that makes role capture VISIBLE in a seed wave: the
	// qualifiers were previously stripped and discarded, so a wave summary that
	// did not report this would look identical whether roles were captured or
	// silently lost again.
	Credits int
	// SeriesPlacements counts works an enrichment run placed into an existing
	// series they were not yet a member of. Always 0 outside ModeEnrich.
	SeriesPlacements int
	// Matched counts enrichment rows whose ASIN located a catalogued recording,
	// whether or not the row then changed anything (a row every one of whose
	// facts was already recorded, and a row dropped for contradicting the record,
	// both count as matched). Always 0 outside ModeEnrich.
	Matched int
	// NotInCatalog counts enrichment rows whose ASIN matches nothing in the
	// catalogue. They are ignored (enrichment never creates), so this is the
	// expected outcome for the overwhelming majority of a large export's rows.
	// Always 0 outside ModeEnrich.
	NotInCatalog int
	// SkippedNoWork counts RECORDINGS-ONLY rows whose work is not in the
	// catalogue. That mode never creates a work, so those rows are dropped -
	// which makes this the counter that proves an excerpt, a trivia title or a
	// book we simply do not hold did NOT fall through to work creation. Always 0
	// outside ModeRecordingsOnly.
	SkippedNoWork int
	// SkippedTitleNoMatch is the SUBSET of SkippedNoWork whose title IS
	// catalogued but whose credits matched no work stored under it. The two
	// failures look identical in a counter and are not the same news: "we do not
	// hold this book" is the mode's expected answer on an unfiltered dump, while
	// "we hold this title and could not agree on who wrote it" is where a
	// title-matching or work-identity defect surfaces. Reported as its own
	// aggregate warning with examples. Always 0 outside ModeRecordingsOnly.
	SkippedTitleNoMatch int
	// SkippedRows counts rows the source's PARSE layer refused before planning
	// ever saw them (no well-formed ASIN, or a marketplace that does not map).
	// It is what makes the run's accounting reconcile: in enrichment mode the
	// rows read are exactly Matched + NotInCatalog + SkippedRows, so a row can
	// never vanish unexplained - which matters precisely because enrichment
	// reports its parse warnings in aggregate rather than per row. Set in both
	// modes; only the libex parser refuses rows of its own today.
	SkippedRows int
	// HonorificMerges lists every credit spelling the honorific rule resolved
	// onto a bare twin this run, as sorted "<credited> -> <bare>" lines
	// (honorific.go). It is reported for the same reason MergedASINs is
	// counted: the rule silently decides that two spellings are ONE HUMAN, which
	// is the least reversible thing an import does, and a wave's list is
	// something a maintainer can read in a minute and a diff of 40,000 files is
	// not. Empty when the rule never fired.
	HonorificMerges []string
	// Warnings are informational "asin/title: reason" lines for books or fields
	// that could not be imported cleanly.
	Warnings []string
}

// Produced counts the outcomes that mean a run actually CHANGED the tree: it
// created records, merged a re-release ASIN into one, or attested a
// bulk-mirror-only record on the submitter's behalf (LICENSING.md's trust tiers
// - an export whose every book is already catalogued still takes those records
// over, and reporting that as "nothing new" would silently discard it).
//
// It lives beside the counters rather than at the caller that asks the question
// (internal/issueform's import composer, which branches its duplicate verdict on
// it) so that adding a counter here is visibly a decision about this verdict too.
//
// Deliberately excluded: Skipped, which is the opposite of a change, and the
// Enriched*/Matched/NotInCatalog family, which only a libex mode ever sets.
func (s Summary) Produced() int {
	return s.NewWorks + s.NewRecordings + s.NewPeople + s.NewSeries + s.MergedASINs +
		s.AttestedWorks + s.AttestedRecordings
}
