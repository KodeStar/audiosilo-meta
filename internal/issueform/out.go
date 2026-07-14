package issueform

import "github.com/kodestar/audiosilo-meta/internal/importer"

// The out* types are issueform's view of each entity's on-disk shape. They exist
// so a form submission is composed into exactly the JSON a hand-authored pull
// request would carry. Field order is irrelevant: every file is run through
// internal/canonical before it is written, which sorts keys.
//
// The entities with a shape identical to a bulk import (source/person/asin/
// series) are ALIASES of internal/importer's exported types, so the two paths
// can never drift. The work and recording shapes here are richer than the
// importer's (subtitle, first_published, xref; a plain-bool abridged and a
// recording ISBN), so they stay local.

const (
	licenseCC0 = "CC0-1.0"
	// sourceUser is the provenance stamped on records composed from an issue
	// form. It matches the common.schema.json source.type enum.
	sourceUser = "user"
)

// These four entities are byte-identical to a bulk import, so issueform reuses
// the importer's exported types rather than redeclaring them.
type (
	outSource     = importer.OutSource
	outPerson     = importer.OutPerson
	outASIN       = importer.OutASIN
	outSeriesWork = importer.OutSeriesWork
	outSeries     = importer.OutSeries
)

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
