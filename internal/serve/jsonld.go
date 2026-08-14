package serve

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// The JSON-LD each entity page carries, built as Go structs and marshaled with
// encoding/json rather than written into a template by hand. Two consequences
// are the reason:
//
//   - it cannot be malformed. A title carrying a quote, a newline or a "</script>"
//     is escaped by the encoder, and encoding/json's DEFAULT HTML escaping turns
//     "<" into <, so the block can never break out of the script element it
//     sits in.
//   - a field that is not a stated fact is OMITTED. Every optional field is
//     omitempty and nothing is defaulted: an unknown runtime emits no duration, an
//     unknown publisher no publisher node. Google reads a defaulted value as a
//     claim, and this project does not make claims it was not given.
const ldContext = "https://schema.org"

type ldRef struct {
	ID string `json:"@id"`
}

type ldPerson struct {
	Type string `json:"@type"`
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

type ldOrganization struct {
	Type string `json:"@type"`
	Name string `json:"name"`
}

// ldSeriesRef is the BookSeries a work belongs to, with the volume position when
// the membership states one. position is a string because a series position is
// ("2.5", "1-3.5"), and schema.org's position accepts text.
type ldSeriesRef struct {
	Type     string `json:"@type"`
	Name     string `json:"name"`
	URL      string `json:"url,omitempty"`
	Position string `json:"position,omitempty"`
}

// ldBook is the abstract work node of a work page's @graph. Its recordings hang
// off it as Audiobook nodes cross-referenced by @id in both directions
// (workExample down, exampleOfWork up), which is how schema.org models an
// edition of a work.
type ldBook struct {
	Type        string       `json:"@type"`
	ID          string       `json:"@id"`
	Name        string       `json:"name"`
	URL         string       `json:"url"`
	Author      []ldPerson   `json:"author,omitempty"`
	InLanguage  string       `json:"inLanguage,omitempty"`
	IsPartOf    *ldSeriesRef `json:"isPartOf,omitempty"`
	WorkExample []ldRef      `json:"workExample,omitempty"`
}

// ldAudiobook is one recording: a specific narration/production of the book.
type ldAudiobook struct {
	Type          string          `json:"@type"`
	ID            string          `json:"@id"`
	Name          string          `json:"name"`
	ReadBy        []ldPerson      `json:"readBy,omitempty"`
	Duration      string          `json:"duration,omitempty"`
	ISBN          string          `json:"isbn,omitempty"`
	Publisher     *ldOrganization `json:"publisher,omitempty"`
	DatePublished string          `json:"datePublished,omitempty"`
	Abridged      bool            `json:"abridged,omitempty"`
	Image         string          `json:"image,omitempty"`
	InLanguage    string          `json:"inLanguage,omitempty"`
	ExampleOfWork *ldRef          `json:"exampleOfWork,omitempty"`
}

type ldGraph struct {
	Context string `json:"@context"`
	Graph   []any  `json:"@graph"`
}

// ldPersonPage is the whole document a person page carries. A person is one
// node: everything else on the page (their credits) is already modeled as the
// works' own pages.
type ldPersonPage struct {
	Context          string `json:"@context"`
	Type             string `json:"@type"`
	Name             string `json:"name"`
	URL              string `json:"url"`
	MainEntityOfPage string `json:"mainEntityOfPage"`
}

type ldBookRef struct {
	Type string `json:"@type"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

type ldListItem struct {
	Type     string    `json:"@type"`
	Position int       `json:"position,omitempty"`
	Item     ldBookRef `json:"item"`
}

// ldBookSeries is a series page: the series plus its ordered volumes. The whole
// membership is emitted - a series list is bounded by what a series is (the
// largest in the catalogue is a few hundred volumes), unlike a person's credits.
type ldBookSeries struct {
	Context         string       `json:"@context"`
	Type            string       `json:"@type"`
	Name            string       `json:"name"`
	URL             string       `json:"url"`
	ItemListElement []ldListItem `json:"itemListElement,omitempty"`
}

// workJSONLD builds the @graph for a work page: the Book, then one Audiobook per
// recording, in the order the detail carries them (recording id order), so two
// renders of one snapshot produce identical bytes.
func workJSONLD(d *workDetail, siteURL, canonical string) []byte {
	bookID := canonical + "#book"
	book := ldBook{Type: "Book", ID: bookID, Name: d.Title, URL: canonical, InLanguage: d.Language}
	for _, a := range d.Authors {
		book.Author = append(book.Author, ldPerson{Type: "Person", Name: a.Name, URL: siteURL + personPath + a.ID})
	}
	// The FIRST membership is the one the page presents as the series, matching
	// the card rule (see snapshot.firstSeriesByWork).
	if len(d.Series) > 0 {
		sr := d.Series[0]
		book.IsPartOf = &ldSeriesRef{Type: "BookSeries", Name: sr.Name, URL: siteURL + seriesPath + sr.ID, Position: sr.Position}
	}

	nodes := make([]any, 0, len(d.Recordings)+1)
	for _, rec := range d.Recordings {
		recID := canonical + "#" + rec.ID
		book.WorkExample = append(book.WorkExample, ldRef{ID: recID})
		ab := ldAudiobook{
			Type: "Audiobook", ID: recID, Name: d.Title,
			Duration:      isoDuration(rec.RuntimeMin),
			DatePublished: rec.ReleaseDate,
			Image:         rec.CoverURL,
			// The artifact carries language on the WORK; a recording is a
			// production of that work, so this is the work's stated language rather
			// than a second fact about the recording.
			InLanguage:    d.Language,
			ExampleOfWork: &ldRef{ID: bookID},
			// Abridged is a plain bool in the artifact, so "stated false" and
			// "unstated" are one value to any reader of it: only true is emitted,
			// because only true is something the data can be said to state.
			Abridged: rec.Abridged,
		}
		for _, n := range rec.Narrators {
			ab.ReadBy = append(ab.ReadBy, ldPerson{Type: "Person", Name: n.Name, URL: siteURL + personPath + n.ID})
		}
		if len(rec.ISBN) > 0 {
			ab.ISBN = rec.ISBN[0]
		}
		if rec.Publisher != "" {
			ab.Publisher = &ldOrganization{Type: "Organization", Name: rec.Publisher}
		}
		nodes = append(nodes, ab)
	}
	return mustMarshalLD(ldGraph{Context: ldContext, Graph: append([]any{book}, nodes...)})
}

func personJSONLD(d *personDetail, canonical string) []byte {
	return mustMarshalLD(ldPersonPage{
		Context: ldContext, Type: "Person", Name: d.Name,
		URL: canonical, MainEntityOfPage: canonical,
	})
}

func seriesJSONLD(d *seriesDetail, siteURL, canonical string) []byte {
	doc := ldBookSeries{Context: ldContext, Type: "BookSeries", Name: d.Name, URL: canonical}
	for _, entry := range d.Works {
		if entry.Work == nil {
			continue
		}
		item := ldListItem{
			Type: "ListItem",
			Item: ldBookRef{Type: "Book", Name: entry.Work.Title, URL: siteURL + workPath + entry.Work.ID},
		}
		if n, ok := listPosition(entry.Position); ok {
			item.Position = n
		}
		doc.ItemListElement = append(doc.ItemListElement, item)
	}
	return mustMarshalLD(doc)
}

// listPosition reads a series position as a ListItem position, which schema.org
// defines as an integer. "2" is one; "2.5" and "1-3.5" are not, and a novella or
// an omnibus is left without a position rather than rounded into somebody else's
// slot.
func listPosition(pos string) (int, bool) {
	lo, hi, ok := parsePositionRange(pos)
	if !ok || lo != hi || lo != math.Trunc(lo) || lo < 0 || lo > math.MaxInt32 {
		return 0, false
	}
	return int(lo), true
}

// isoDuration renders a runtime in minutes as an ISO 8601 duration ("PT16H10M").
// A runtime of zero is UNKNOWN in this artifact, not a zero-length recording, so
// it renders as the empty string and the omitempty drops the field.
func isoDuration(min int) string {
	if min <= 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("PT")
	if h := min / 60; h > 0 {
		b.WriteString(strconv.Itoa(h))
		b.WriteByte('H')
	}
	if m := min % 60; m > 0 {
		b.WriteString(strconv.Itoa(m))
		b.WriteByte('M')
	}
	return b.String()
}

// mustMarshalLD marshals a JSON-LD document. The inputs are this package's own
// structs over strings and ints, so marshaling cannot fail; an error would be a
// programming mistake rather than a request-time condition, and an empty object
// keeps the page renderable either way.
func mustMarshalLD(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return b
}
