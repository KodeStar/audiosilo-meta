// Package model defines the on-disk entity types and the rules that tie a file
// to its slug, shard, and location. It has no dependency on validation or
// storage; both metacheck and metabuild build on it.
package model

// Kind identifies an entity type.
type Kind string

const (
	KindWork      Kind = "work"
	KindRecording Kind = "recording"
	KindPerson    Kind = "person"
	KindSeries    Kind = "series"
)

// Source records the provenance of a record. Every entity carries at least one.
type Source struct {
	Type       string `json:"type"`
	Ref        string `json:"ref,omitempty"`
	ImportedAt string `json:"imported_at,omitempty"`
}

// PersonXref holds cross-references to external databases for a person.
type PersonXref struct {
	Wikidata    string `json:"wikidata,omitempty"`
	Openlibrary string `json:"openlibrary,omitempty"`
	Audible     string `json:"audible,omitempty"`
}

// Person is an author and/or narrator.
type Person struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	SortName    string      `json:"sort_name,omitempty"`
	Description string      `json:"description,omitempty"`
	Xref        *PersonXref `json:"xref,omitempty"`
	License     string      `json:"license"`
	Sources     []Source    `json:"sources"`
}

// WorkXref holds cross-references to external databases for a work.
type WorkXref struct {
	Wikidata    string   `json:"wikidata,omitempty"`
	Openlibrary string   `json:"openlibrary,omitempty"`
	Goodreads   string   `json:"goodreads,omitempty"`
	ISBN        []string `json:"isbn,omitempty"`
}

// Work is the abstract book.
type Work struct {
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	Subtitle       string    `json:"subtitle,omitempty"`
	Authors        []string  `json:"authors"`
	Language       string    `json:"language"`
	FirstPublished string    `json:"first_published,omitempty"`
	Description    string    `json:"description,omitempty"`
	Xref           *WorkXref `json:"xref,omitempty"`
	License        string    `json:"license"`
	Sources        []Source  `json:"sources"`

	// Recordings is populated by the loader, not read from work.json.
	Recordings []*Recording `json:"-"`
}

// ASIN pairs a region with a region-scoped Audible identifier.
type ASIN struct {
	Region string `json:"region"`
	ASIN   string `json:"asin"`
}

// Chapter is one chapter of a recording, on the recording's own timeline.
type Chapter struct {
	Title    string `json:"title"`
	StartMS  int64  `json:"start_ms"`
	LengthMS int64  `json:"length_ms"`
}

// Recording is a specific narration/production of a work.
type Recording struct {
	ID          string    `json:"id"`
	Work        string    `json:"work"`
	Narrators   []string  `json:"narrators"`
	Abridged    bool      `json:"abridged"`
	Language    string    `json:"language"`
	RuntimeMin  int       `json:"runtime_min,omitempty"`
	ReleaseDate string    `json:"release_date,omitempty"`
	Publisher   string    `json:"publisher,omitempty"`
	ASIN        []ASIN    `json:"asin,omitempty"`
	ISBN        []string  `json:"isbn,omitempty"`
	CoverURL    string    `json:"cover_url,omitempty"`
	Chapters    []Chapter `json:"chapters,omitempty"`
	License     string    `json:"license"`
	Sources     []Source  `json:"sources"`
}

// SeriesWork is one work's membership in a series, with its ordering position.
type SeriesWork struct {
	Work     string `json:"work"`
	Position string `json:"position"`
}

// SeriesXref holds cross-references to external databases for a series.
type SeriesXref struct {
	Wikidata  string `json:"wikidata,omitempty"`
	Goodreads string `json:"goodreads,omitempty"`
}

// Series is an ordered set of works.
type Series struct {
	ID      string       `json:"id"`
	Name    string       `json:"name"`
	Authors []string     `json:"authors,omitempty"`
	Works   []SeriesWork `json:"works"`
	Xref    *SeriesXref  `json:"xref,omitempty"`
	License string       `json:"license"`
	Sources []Source     `json:"sources"`
}

// Catalog is the fully loaded, in-memory dataset.
type Catalog struct {
	Works  []*Work
	People []*Person
	Series []*Series
}
