// Package model defines the on-disk entity types and the rules that tie a file
// to its slug, shard, and location. It has no dependency on validation or
// storage; both metacheck and metabuild build on it.
package model

// Kind identifies an entity type.
type Kind string

const (
	KindWork       Kind = "work"
	KindRecording  Kind = "recording"
	KindPerson     Kind = "person"
	KindSeries     Kind = "series"
	KindCharacters Kind = "characters"
	KindRecaps     Kind = "recaps"
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

// Position is a spoiler position within a work's own timeline. Chapter is the
// logical (edition-independent) work chapter, 1-based; 0 means front matter or
// knowledge carried from earlier books in a series.
type Position struct {
	Chapter int `json:"chapter"`
}

// CharacterXref holds cross-references to external databases for a character. A
// shared wikidata QID is how a recurring character is linked across the per-work
// character files of a series.
type CharacterXref struct {
	Wikidata  string `json:"wikidata,omitempty"`
	Goodreads string `json:"goodreads,omitempty"`
}

// Character is one spoiler-tagged, community-authored character entry.
type Character struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Aliases     []string       `json:"aliases,omitempty"`
	Role        string         `json:"role,omitempty"`
	Reveal      Position       `json:"reveal"`
	Description string         `json:"description,omitempty"`
	Xref        *CharacterXref `json:"xref,omitempty"`
}

// Characters is the per-work sidecar holding a work's character entries. It
// lives in the CC BY-SA layer, decoupled from the CC0 core work record.
type Characters struct {
	Work       string      `json:"work"`
	Characters []Character `json:"characters"`
	License    string      `json:"license"`
	Sources    []Source    `json:"sources"`
}

// Recap is one position-keyed "story so far" summary. Through is the position
// the recap is safe to show at (the listener has finished that chapter).
type Recap struct {
	Through Position `json:"through"`
	Scope   string   `json:"scope,omitempty"`
	Text    string   `json:"text"`
}

// Recaps is the per-work sidecar holding a work's recaps. It lives in the CC
// BY-SA layer, decoupled from the CC0 core work record. InShort is a one-
// paragraph whole-book refresher (ending included) and Ending states how the
// book closes; both are optional.
type Recaps struct {
	Work    string   `json:"work"`
	Recaps  []Recap  `json:"recaps"`
	InShort string   `json:"in_short,omitempty"`
	Ending  string   `json:"ending,omitempty"`
	License string   `json:"license"`
	Sources []Source `json:"sources"`
}

// Catalog is the fully loaded, in-memory dataset.
type Catalog struct {
	Works      []*Work
	People     []*Person
	Series     []*Series
	Characters []*Characters
	Recaps     []*Recaps
}
