package importer

// The out* types are the importer's own view of each entity's on-disk shape.
// They exist separately from internal/model so the importer controls exactly
// which fields are emitted - notably abridged, which is a tri-state pointer here
// (omitted when unknown) rather than a plain bool. Field order is irrelevant:
// every file is run through internal/canonical before it is written, which sorts
// keys.

const (
	licenseCC0    = "CC0-1.0"
	sourceOpenAud = "openaudible-import"
)

type outSource struct {
	Type       string `json:"type"`
	Ref        string `json:"ref,omitempty"`
	ImportedAt string `json:"imported_at,omitempty"`
}

type outPerson struct {
	ID      string      `json:"id"`
	Name    string      `json:"name"`
	License string      `json:"license"`
	Sources []outSource `json:"sources"`
}

type outWork struct {
	ID       string      `json:"id"`
	Title    string      `json:"title"`
	Authors  []string    `json:"authors"`
	Language string      `json:"language"`
	License  string      `json:"license"`
	Sources  []outSource `json:"sources"`
}

type outASIN struct {
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
	ASIN        []outASIN    `json:"asin,omitempty"`
	CoverURL    string       `json:"cover_url,omitempty"`
	Chapters    []outChapter `json:"chapters,omitempty"`
	License     string       `json:"license"`
	Sources     []outSource  `json:"sources"`
}

type outSeriesWork struct {
	Work     string `json:"work"`
	Position string `json:"position"`
}

type outSeries struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	Works   []outSeriesWork `json:"works"`
	License string          `json:"license"`
	Sources []outSource     `json:"sources"`
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
	// Warnings are informational "asin/title: reason" lines for books or fields
	// that could not be imported cleanly.
	Warnings []string
}
