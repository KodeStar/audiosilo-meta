package importer

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
)

// The Languages Phase 2 link fields - a work's and a series' translation_of, a
// series' ordering and ordering_of - are facts no import source states, so no
// importer path writes them. Every path that REWRITES an entry carrying them must
// therefore carry them through byte for byte: the importer edits entries as raw
// maps precisely so a field it does not model survives, and these tests pin that
// for each path that reads-modifies-writes a work or a series entry.

// langLinkMembers are the members these tests follow through a rewrite.
var langLinkMembers = []string{"translation_of", "ordering", "ordering_of"}

// langFieldTree seeds a small bilingual franchise, every record mirror-only (the
// fixture default) so a user-library run may attest it:
//
//   - the-lost-coast (en) and the-far-shore (en), by Ada Mapmaker;
//   - la-cote-perdue (fr), a translation_of the-lost-coast, publisher unstated so
//     an enrichment has something to fill;
//   - atlas-cycle, the PRIMARY ordering (publication) holding both English works;
//   - atlas-cycle-chronological-order, a variant (ordering_of atlas-cycle) holding the
//     first only, so an enrichment can place the second;
//   - cycle-atlas, a translation_of atlas-cycle holding the French work.
func langFieldTree(t *testing.T) string {
	t.Helper()
	work := func(id, title, lang string, extra map[string]any) string {
		doc := testpack.WorkJSON(t, id, title, testpack.WithAuthors("ada-mapmaker"), testpack.WithLanguage(lang))
		for k, v := range extra {
			doc = testpack.WithField(t, doc, k, v)
		}
		return doc
	}
	series := func(id, name string, extra map[string]any, members ...string) string {
		doc := testpack.SeriesJSON(t, id, name, members...)
		for k, v := range extra {
			doc = testpack.WithField(t, doc, k, v)
		}
		return doc
	}
	rec := func(work, asin, lang string, opts ...testpack.RecOpt) string {
		opts = append([]testpack.RecOpt{testpack.WithNarrators("bea-reader"), testpack.WithASIN(asin), testpack.WithRuntime(600)}, opts...)
		return testpack.WithField(t, testpack.RecJSON(t, "r1", work, opts...), "language", lang)
	}
	files := map[string]string{
		"people/ad/ada-mapmaker.json":                 testpack.PersonJSON(t, "ada-mapmaker", "Ada Mapmaker"),
		"people/be/bea-reader.json":                   testpack.PersonJSON(t, "bea-reader", "Bea Reader"),
		workAddr("the-lost-coast"):                    work("the-lost-coast", "The Lost Coast", "en", nil),
		recAddr("the-lost-coast", "r1"):               rec("the-lost-coast", "B0LANGEN01", "en"),
		workAddr("the-far-shore"):                     work("the-far-shore", "The Far Shore", "en", nil),
		recAddr("the-far-shore", "r1"):                rec("the-far-shore", "B0LANGEN02", "en"),
		workAddr("la-cote-perdue"):                    work("la-cote-perdue", "La Côte Perdue", "fr", map[string]any{"translation_of": []string{"the-lost-coast"}}),
		recAddr("la-cote-perdue", "r1"):               rec("la-cote-perdue", "B0LANGFR01", "fr", testpack.WithoutPublisher()),
		seriesAddr("atlas-cycle"):                     series("atlas-cycle", "Atlas Cycle", map[string]any{"ordering": "publication"}, "the-lost-coast@1", "the-far-shore@2"),
		seriesAddr("atlas-cycle-chronological-order"): series("atlas-cycle-chronological-order", "Atlas Cycle Chronological Order", map[string]any{"ordering": "chronological", "ordering_of": "atlas-cycle"}, "the-lost-coast@1"),
		seriesAddr("cycle-atlas"):                     series("cycle-atlas", "Cycle Atlas", map[string]any{"translation_of": []string{"atlas-cycle"}}, "la-cote-perdue@1"),
	}
	dataDir := t.TempDir()
	seedTree(t, dataDir, files)
	assertTreeValid(t, dataDir)
	return dataDir
}

// langLinked are the addresses of every record the fixture links, which is what
// every test follows through its run.
var langLinked = []string{
	workAddr("la-cote-perdue"),
	seriesAddr("atlas-cycle"),
	seriesAddr("atlas-cycle-chronological-order"),
	seriesAddr("cycle-atlas"),
}

// linkSnapshot reads the link members of each address, as the raw bytes the tree
// holds, plus the whole record so a test can prove the run really rewrote it.
func linkSnapshot(t *testing.T, dataDir string, addrs []string) (links, whole map[string]string) {
	t.Helper()
	links, whole = map[string]string{}, map[string]string{}
	for _, a := range addrs {
		raw, ok := testpack.Raw(t, dataDir, a)
		if !ok {
			t.Fatalf("no record at %s", a)
		}
		whole[a] = string(raw)
		// A work's record is its own fields; the recordings ride in the same entry,
		// so a run that only rewrote a recording rewrote the entry too.
		if strings.HasPrefix(a, "works/") {
			slug := strings.Split(a, "/")[2]
			for _, r := range testpack.Recordings(t, dataDir, slug) {
				rr, _ := testpack.Raw(t, dataDir, recAddr(slug, r))
				whole[a] += "\n" + string(rr)
			}
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err != nil {
			t.Fatalf("parse %s: %v", a, err)
		}
		var parts []string
		for _, m := range langLinkMembers {
			if v, ok := obj[m]; ok {
				parts = append(parts, m+"="+string(v))
			}
		}
		if len(parts) == 0 {
			t.Fatalf("%s carries none of the link members - the fixture is wrong", a)
		}
		links[a] = strings.Join(parts, " ")
	}
	return links, whole
}

// assertLinksSurvive fails for any address whose link members changed, and
// requires the run to have rewritten every address in rewritten - otherwise the
// test would pass for a path that never touched the entry.
func assertLinksSurvive(t *testing.T, dataDir string, before, beforeWhole map[string]string, rewritten ...string) {
	t.Helper()
	after, afterWhole := linkSnapshot(t, dataDir, langLinked)
	for _, a := range langLinked {
		if after[a] != before[a] {
			t.Errorf("%s link members changed:\n got %s\nwant %s", a, after[a], before[a])
		}
	}
	for _, a := range rewritten {
		if afterWhole[a] == beforeWhole[a] {
			t.Errorf("%s was not rewritten by the run, so the test proves nothing about it", a)
		}
	}
	assertTreeValid(t, dataDir)
}

// The create path EXTENDING existing series: a volume placed in the primary and
// in its variant, and a French volume placed in the translated series. All three
// series entries are read, extended and written back.
func TestCreateExtendingASeriesKeepsItsLinks(t *testing.T) {
	dataDir := langFieldTree(t)
	before, whole := linkSnapshot(t, dataDir, langLinked)

	hidden := multiSeriesRow("B0LANGEN03", "The Hidden Bay", "Ada Mapmaker", "Bea Reader", "english",
		[2]string{"Atlas Cycle", "3"}, [2]string{"Atlas Cycle Chronological Order", "2"})
	baie := multiSeriesRow("B0LANGFR03", "La Baie Cachee", "Ada Mapmaker", "Bea Reader", "french",
		[2]string{"Cycle Atlas", "2"})
	sum := runLibexWith(t, dataDir, Options{}, hidden, baie)
	if sum.NewWorks != 2 || sum.NewSeries != 0 {
		t.Fatalf("NewWorks/NewSeries = %d/%d, want 2/0: %v", sum.NewWorks, sum.NewSeries, sum.Warnings)
	}
	assertLinksSurvive(t, dataDir, before, whole,
		seriesAddr("atlas-cycle"), seriesAddr("atlas-cycle-chronological-order"), seriesAddr("cycle-atlas"))
}

// --enrich: an ASIN-matched row fills the translation's absent publisher, which
// rewrites its work's composite entry, and places the second English work in the
// variant it was missing from, which rewrites the variant.
func TestEnrichKeepsTheLinks(t *testing.T) {
	dataDir := langFieldTree(t)
	before, whole := linkSnapshot(t, dataDir, langLinked)

	rows := `[{"asin":"B0LANGFR01","title":"La Côte Perdue","region":"fr","publisher":"Presses du Large","language":"french",
	  "authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}]},
	 {"asin":"B0LANGEN02","title":"The Far Shore","region":"us","language":"english",
	  "authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}],
	  "series":[{"name":"Atlas Cycle Chronological Order","position":"2"}]}]`
	sum := runEnrich(t, dataDir, rows, false)
	if sum.EnrichedRecordings != 1 || sum.SeriesPlacements != 1 {
		t.Fatalf("EnrichedRecordings/SeriesPlacements = %d/%d, want 1/1: %v", sum.EnrichedRecordings, sum.SeriesPlacements, sum.Warnings)
	}
	assertLinksSurvive(t, dataDir, before, whole, workAddr("la-cote-perdue"), seriesAddr("atlas-cycle-chronological-order"))
}

// A user-tier ATTESTATION (the intake bot's takeover door, AttestAt): the mirror
// seed is overwritten with what the submission states, and its genres are
// unioned into the work, so the work entry carrying translation_of is rewritten.
func TestAttestationKeepsTheLinks(t *testing.T) {
	dataDir := langFieldTree(t)
	before, whole := linkSnapshot(t, dataDir, langLinked)

	store, err := openStore(dataDir, "")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	sum, err := AttestAt(store, RecRef{Work: "la-cote-perdue", Rec: "r1"}, nil, Attestation{
		ASIN: "B0LANGFR01", Source: formSource, Publisher: "Presses du Large", Genres: []string{"fantasy"},
	}, Options{DataDir: dataDir, ImportDate: testImportDate})
	if err != nil {
		t.Fatalf("AttestAt: %v", err)
	}
	if sum.AttestedRecordings != 1 {
		t.Fatalf("AttestedRecordings = %d, want 1: %+v", sum.AttestedRecordings, sum)
	}
	assertLinksSurvive(t, dataDir, before, whole, workAddr("la-cote-perdue"))
}

// --recordings-only: a second narration of the translation lands as a new
// recording inside its work's composite entry.
func TestRecordingsOnlyKeepsTheLinks(t *testing.T) {
	dataDir := langFieldTree(t)
	before, whole := linkSnapshot(t, dataDir, langLinked)

	row := `[{"asin":"B0LANGFR02","title":"La Côte Perdue","region":"fr","language":"french","bookFormat":"unabridged",
	  "lengthMinutes":640,"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Cal Voice"}]}]`
	sum := runRecordingsOnly(t, dataDir, row, false)
	if sum.NewRecordings != 1 || sum.NewWorks != 0 {
		t.Fatalf("NewRecordings/NewWorks = %d/%d, want 1/0: %v", sum.NewRecordings, sum.NewWorks, sum.Warnings)
	}
	assertLinksSurvive(t, dataDir, before, whole, workAddr("la-cote-perdue"))
}

// The series-completion sync bot's shape (--existing-series-only
// --attach-editions): a second edition of a volume the primary already fills is
// ATTACHED to that work, a new volume extends the primary, and the row's second,
// uncatalogued claim is dropped rather than founding a series.
func TestSyncBotRunKeepsTheLinks(t *testing.T) {
	dataDir := langFieldTree(t)
	before, whole := linkSnapshot(t, dataDir, langLinked)

	edition := multiSeriesRow("B0LANGFR04", "La Côte Perdue", "Ada Mapmaker", "Cal Voice", "french",
		[2]string{"Cycle Atlas", "1"})
	next := multiSeriesRow("B0LANGEN05", "The Hidden Bay", "Ada Mapmaker", "Bea Reader", "english",
		[2]string{"Atlas Cycle", "3"}, [2]string{"Atlas Cycle Omnibus Order", "1"})
	sum := runLibexWith(t, dataDir, Options{ExistingSeriesOnly: true, AttachEditions: true}, edition, next)
	if sum.Attached != 1 || sum.NewWorks != 1 || sum.NewSeries != 0 || sum.SeriesClaimsDropped != 1 {
		t.Fatalf("Attached/NewWorks/NewSeries/SeriesClaimsDropped = %d/%d/%d/%d, want 1/1/0/1: %v",
			sum.Attached, sum.NewWorks, sum.NewSeries, sum.SeriesClaimsDropped, sum.Warnings)
	}
	assertLinksSurvive(t, dataDir, before, whole, workAddr("la-cote-perdue"), seriesAddr("atlas-cycle"))
}
