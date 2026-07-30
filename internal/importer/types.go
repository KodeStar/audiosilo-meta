package importer

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

const (
	licenseCC0     = "CC0-1.0"
	sourceOpenAud  = "openaudible-import"
	sourceLibation = "libation-import"
	sourceLibex    = "libex-import"
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
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Authors  []string `json:"authors"`
	Language string   `json:"language"`
	// Genres are the project's own vocabulary slugs, sorted ascending
	// (checkGenresSorted pins the order). Omitted when the source carried no
	// genre that maps - never a retailer's raw genre strings (LICENSING.md).
	Genres  []string    `json:"genres,omitempty"`
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
	License     string       `json:"license"`
	Sources     []OutSource  `json:"sources"`
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
	// EnrichedWorks / EnrichedRecordings count the existing records an
	// enrichment run actually changed (a record whose every fact was already
	// present is not counted - enrichment never rewrites a file it did not
	// change). Always 0 outside ModeEnrich.
	EnrichedWorks      int
	EnrichedRecordings int
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
	// SkippedRows counts rows the source's PARSE layer refused before planning
	// ever saw them (no well-formed ASIN, or a marketplace that does not map).
	// It is what makes the run's accounting reconcile: in enrichment mode the
	// rows read are exactly Matched + NotInCatalog + SkippedRows, so a row can
	// never vanish unexplained - which matters precisely because enrichment
	// reports its parse warnings in aggregate rather than per row. Set in both
	// modes; only the libex parser refuses rows of its own today.
	SkippedRows int
	// Warnings are informational "asin/title: reason" lines for books or fields
	// that could not be imported cleanly.
	Warnings []string
}
