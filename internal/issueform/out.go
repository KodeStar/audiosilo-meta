package issueform

// The out* types are issueform's view of each entity's on-disk shape, mirroring
// internal/importer's out* types (which are package-private). They exist so a
// form submission is composed into exactly the JSON a hand-authored pull request
// would carry. Field order is irrelevant: every file is run through
// internal/canonical before it is written, which sorts keys.

const (
	licenseCC0 = "CC0-1.0"
	// sourceUser is the provenance stamped on records composed from an issue
	// form. It matches the common.schema.json source.type enum.
	sourceUser = "user"
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

type outWorkXref struct {
	Wikidata    string   `json:"wikidata,omitempty"`
	Openlibrary string   `json:"openlibrary,omitempty"`
	ISBN        []string `json:"isbn,omitempty"`
}

type outWork struct {
	ID             string       `json:"id"`
	Title          string       `json:"title"`
	Subtitle       string       `json:"subtitle,omitempty"`
	Authors        []string     `json:"authors"`
	Language       string       `json:"language"`
	FirstPublished string       `json:"first_published,omitempty"`
	Xref           *outWorkXref `json:"xref,omitempty"`
	License        string       `json:"license"`
	Sources        []outSource  `json:"sources"`
}

type outASIN struct {
	Region string `json:"region"`
	ASIN   string `json:"asin"`
}

type outRecording struct {
	ID          string      `json:"id"`
	Work        string      `json:"work"`
	Narrators   []string    `json:"narrators"`
	Abridged    bool        `json:"abridged"`
	Language    string      `json:"language"`
	RuntimeMin  int         `json:"runtime_min,omitempty"`
	ReleaseDate string      `json:"release_date,omitempty"`
	Publisher   string      `json:"publisher,omitempty"`
	ASIN        []outASIN   `json:"asin,omitempty"`
	ISBN        []string    `json:"isbn,omitempty"`
	CoverURL    string      `json:"cover_url,omitempty"`
	License     string      `json:"license"`
	Sources     []outSource `json:"sources"`
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
