package revision

import (
	"encoding/binary"
	"fmt"
	"hash"
	"reflect"
	"strings"
	"sync"

	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/query"
)

// What a page's digest reads. A page is digested from the documents metaserve
// composes it from (pkg/query's exported reads), field by field, and EVERY field
// of every struct those documents carry is decided here: hashed (a change to it
// is a change to the page) or excluded (it is not the page's content). Nothing
// is hashed by default. The digest walk refuses a field it finds no decision for,
// and TestEveryDocumentFieldIsDecided walks the same types the digests reach, so
// a field a later change adds to WorkDetail, RecordingDetail, PersonDetail,
// SeriesListeningGuide or anything they hold fails the suite until somebody
// decides which it is.
//
// The exclusions, and why each is not page content:
//
//   - WorkCard.AddedAt: provenance - when a record entered the catalogue, which a
//     page does not present as content and which an import re-stamp would
//     otherwise turn into a "change" on every page listing the work.
//   - WorkDetail.Characters, .Recaps, .RecapSummary and .PositionScale: the
//     GUIDE pages own the sidecars (see guideFacts). The work page shows only
//     that a guide exists and how its link reads (workFacts), so an edited recap
//     moves the recap page and leaves the work page alone. Of the position scale
//     a page reads only the unit (query.PositionUnit); its length and partial flag
//     reach no page.
//   - Limit and Offset everywhere, and ListeningGuideEntry.RecordingsLimit: the
//     window the read was asked for, echoed back. The digest reads every window
//     of a page with its own window size, so these describe the read, not the
//     page.
type decision struct {
	hashed   []string
	excluded []string
}

var decisions = map[reflect.Type]decision{
	// The work page.
	reflect.TypeFor[query.WorkDetail](): {
		hashed: []string{"ID", "Title", "Subtitle", "AlternateTitles", "Authors", "Language", "FirstPublished",
			"Description", "Genres", "Series", "TranslationOf", "Translations", "Xref", "Recordings",
			"CommunityDescription"},
		excluded: []string{"Characters", "Recaps", "RecapSummary", "PositionScale"},
	},
	reflect.TypeFor[query.RecordingDetail](): {hashed: []string{"ID", "Narrators", "Abridged", "Abridgement",
		"Language", "RuntimeMin", "ReleaseDate", "Publisher", "ASIN", "ISBN", "PurchaseLinks", "CoverURL",
		"ChapterCount", "LibrivoxID", "ArchiveID", "Links", "CopyrightYear"}},
	reflect.TypeFor[query.PersonRef]():        {hashed: []string{"ID", "Name"}},
	reflect.TypeFor[query.SeriesRef]():        {hashed: []string{"ID", "Name", "Position", "OrderingOf"}},
	reflect.TypeFor[query.WorkTranslation]():  {hashed: []string{"ID", "Title", "Language"}},
	reflect.TypeFor[query.WorkXref]():         {hashed: []string{"Wikidata", "Openlibrary", "Goodreads", "ISBN"}},
	reflect.TypeFor[query.ASINRef]():          {hashed: []string{"Region", "ASIN"}},
	reflect.TypeFor[query.PurchaseLink]():     {hashed: []string{"Retailer", "ID", "Region", "URL", "Availability", "Free"}},
	reflect.TypeFor[query.DescriptionOut]():   {hashed: []string{"Text", "License"}},
	reflect.TypeFor[model.RecordingLinks]():   {hashed: []string{"Librivox", "Zip", "RSS", "Text", "AudiobookGuild", "GraphicAudio"}},
	reflect.TypeFor[query.CharacterOut]():     {hashed: []string{"ID", "Name", "Aliases", "Role", "Reveal", "Description", "Xref"}},
	reflect.TypeFor[query.CharacterXref]():    {hashed: []string{"Wikidata", "Goodreads"}},
	reflect.TypeFor[query.PositionOut]():      {hashed: []string{"Chapter"}},
	reflect.TypeFor[query.RecapOut]():         {hashed: []string{"Through", "Scope", "Text"}},
	reflect.TypeFor[query.RecapSummaryOut]():  {hashed: []string{"InShort", "Ending"}},
	reflect.TypeFor[query.WorkCard]():         {hashed: []string{"ID", "Title", "Authors", "Language", "Series", "ReleaseDate", "CoverURL"}, excluded: []string{"AddedAt"}},
	reflect.TypeFor[query.RecordingSummary](): {hashed: []string{"ID", "Narrators", "Language", "Abridgement", "RuntimeMin", "ReleaseDate", "Publisher", "ChapterCount"}},
	reflect.TypeFor[query.ChapterOut]():       {hashed: []string{"Title", "StartMS", "LengthMS", "AudioURL"}},

	// The person page.
	reflect.TypeFor[query.PersonDetail](): {hashed: []string{"ID", "Name", "SortName", "Authored", "Narrated",
		"AuthoredTotal", "NarratedTotal", "Translated", "TranslatedTotal"}, excluded: []string{"Limit", "Offset"}},
	reflect.TypeFor[query.NarratedEntry]():        {hashed: []string{"Work", "RecordingID"}},
	reflect.TypeFor[query.PersonRecordingsPage](): {hashed: []string{"Recordings", "Total"}, excluded: []string{"Limit", "Offset"}},
	reflect.TypeFor[query.PersonRecordingEntry](): {hashed: []string{"Work", "Recording", "Series"}},
	reflect.TypeFor[query.RecordingSeries]():      {hashed: []string{"ID", "Name", "Position"}},
	reflect.TypeFor[query.SeriesListeningGuide](): {hashed: []string{"ID", "Name", "Language", "Ordering", "OrderingOf", "Authors", "Orderings", "TranslationOf", "Translations", "Entries", "WorksTotal"}, excluded: []string{"Limit", "Offset"}},
	reflect.TypeFor[query.SeriesOrdering]():       {hashed: []string{"ID", "Name", "Ordering"}},
	reflect.TypeFor[query.SeriesTranslation]():    {hashed: []string{"ID", "Name", "Language"}},
	reflect.TypeFor[query.ListeningGuideEntry]():  {hashed: []string{"Position", "Work", "RecordingsTotal", "Recordings"}, excluded: []string{"RecordingsLimit"}},

	// This package's own composites.
	reflect.TypeFor[workFacts]():         {hashed: []string{"Detail", "RecapGuide", "CharacterGuide", "ChapteredRecaps", "PositionUnit"}},
	reflect.TypeFor[recordingChapters](): {hashed: []string{"Recording", "Chapters"}},
	reflect.TypeFor[guideFacts]():        {hashed: []string{"Title", "Authors", "SeriesID", "SeriesName", "Cover", "PositionUnit", "Sibling", "Recaps", "RecapSummary", "Characters"}},
}

// workFacts is what a work page renders from: its detail document, minus the
// sidecars (see decisions), plus the facts the page's Guides section reads off
// them - which guides exist, whether the recap link may promise a
// chapter-by-chapter recap, and the unit it counts in.
type workFacts struct {
	Detail          *query.WorkDetail
	RecapGuide      bool
	CharacterGuide  bool
	ChapteredRecaps bool
	PositionUnit    string
}

// recordingChapters is one recording's chapter list, which its chapters pages
// render and the work page's date stands for.
type recordingChapters struct {
	Recording string
	Chapters  []query.ChapterOut
}

// guideFacts is what a guide page renders from. It is NOT the work detail: the
// guide pages show the work's title, byline, first cover and first series (the
// crawl mesh links it by id and name), the position unit, whether the SIBLING
// guide exists, and their own sidecar - so a new recording, a description or an
// edited narrator leaves them alone. Only one of the two sidecar halves is set
// per page.
type guideFacts struct {
	Title        string
	Authors      []query.PersonRef
	SeriesID     string
	SeriesName   string
	Cover        string
	PositionUnit string
	Sibling      bool
	Recaps       []query.RecapOut
	RecapSummary *query.RecapSummaryOut
	Characters   []query.CharacterOut
}

// fieldPlan is one hashed field of a struct type: its index and the name the
// encoding writes before its value.
type fieldPlan struct {
	index int
	name  string
}

var plans sync.Map // reflect.Type -> []fieldPlan

// planOf returns t's hashed fields in declaration order, or an error naming the
// first field decisions does not cover.
func planOf(t reflect.Type) ([]fieldPlan, error) {
	if p, ok := plans.Load(t); ok {
		return p.([]fieldPlan), nil
	}
	d, ok := decisions[t]
	if !ok {
		return nil, fmt.Errorf("revision: no field decisions for %s", t)
	}
	hashed := setOf(d.hashed)
	excluded := setOf(d.excluded)
	var out []fieldPlan
	for _, f := range documentFields(t) {
		switch {
		case hashed[f.Name]:
			out = append(out, fieldPlan{index: f.Index[0], name: f.Name})
		case excluded[f.Name]:
		default:
			return nil, fmt.Errorf("revision: %s.%s is neither hashed nor excluded - decide it in decisions", t, f.Name)
		}
	}
	plans.Store(t, out)
	return out, nil
}

// documentFields is t's fields as a document carries them: exported, and not
// tagged json:"-".
func documentFields(t reflect.Type) []reflect.StructField {
	var out []reflect.StructField
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		if name, _, _ := strings.Cut(f.Tag.Get("json"), ","); name == "-" {
			continue
		}
		out = append(out, f)
	}
	return out
}

func setOf(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// encoder writes a value into a hash as an unambiguous byte stream: every string
// and every list is length-prefixed, every pointer states whether it is nil, and
// every struct field is preceded by its name - so two different documents cannot
// write the same bytes, and adding a hashed field changes every digest that
// carries a value for it, not the ones that do not reach it.
type encoder struct {
	h   hash.Hash
	buf [binary.MaxVarintLen64]byte
	// scratch carries a string into h without a fresh []byte(s) allocation per
	// field (h is an interface, so the conversion escapes).
	scratch []byte
}

func (e *encoder) uint(n uint64) {
	k := binary.PutUvarint(e.buf[:], n)
	_, _ = e.h.Write(e.buf[:k])
}

func (e *encoder) str(s string) {
	e.uint(uint64(len(s)))
	e.scratch = append(e.scratch[:0], s...)
	_, _ = e.h.Write(e.scratch)
}

func (e *encoder) value(v reflect.Value) error {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			e.uint(0)
			return nil
		}
		e.uint(1)
		return e.value(v.Elem())
	case reflect.Struct:
		plan, err := planOf(v.Type())
		if err != nil {
			return err
		}
		for _, f := range plan {
			e.str(f.name)
			if err := e.value(v.Field(f.index)); err != nil {
				return err
			}
		}
		return nil
	case reflect.Slice:
		e.uint(uint64(v.Len()))
		for i := range v.Len() {
			if err := e.value(v.Index(i)); err != nil {
				return err
			}
		}
		return nil
	case reflect.String:
		e.str(v.String())
		return nil
	case reflect.Bool:
		if v.Bool() {
			e.uint(1)
		} else {
			e.uint(0)
		}
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		// Zigzag, so a negative (a BC copyright year) is as unambiguous as the rest.
		n := v.Int()
		e.uint(uint64(n<<1) ^ uint64(n>>63))
		return nil
	default:
		return fmt.Errorf("revision: cannot digest a %s", v.Type())
	}
}
