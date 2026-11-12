package importer

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// The --copies-of direct attachment (copies.go): a row the caller proved to be
// another marketplace's copy of a catalogued production merges onto the
// recording its anchor ASIN sits on, under the physical guards alone.

const rebornAnchor = "B0GV5B7VF2"

// seedRebornCatalogue is work reborn-as-a-demonic-tree-8 as the catalogue held
// it: credited to `xkarnation`, its one recording (Ramon de Ocampo, 600 min,
// unabridged English) carrying the CA ASIN only, at position 8 of its series.
func seedRebornCatalogue(t *testing.T) string {
	t.Helper()
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{
		"people/xk/xkarnation.json":      `{"id":"xkarnation","license":"CC0-1.0","name":"XKarnation","sources":[{"type":"user"}]}`,
		"people/ra/ramon-de-ocampo.json": `{"id":"ramon-de-ocampo","license":"CC0-1.0","name":"Ramon de Ocampo","sources":[{"type":"user"}]}`,
		"works/re/reborn-as-a-demonic-tree-8/work.json": `{"authors":["xkarnation"],"id":"reborn-as-a-demonic-tree-8","language":"en",` +
			`"license":"CC0-1.0","sources":[{"type":"user"}],"title":"Reborn as a Demonic Tree 8"}`,
		"works/re/reborn-as-a-demonic-tree-8/recordings/ramon-de-ocampo-2026.json": `{"abridged":false,"asin":[{"asin":"` + rebornAnchor + `","region":"ca"}],` +
			`"id":"ramon-de-ocampo-2026","language":"en","license":"CC0-1.0","narrators":["ramon-de-ocampo"],"runtime_min":600,` +
			`"sources":[{"type":"user"}],"work":"reborn-as-a-demonic-tree-8"}`,
		"series/re/reborn-as-a-demonic-tree.json": `{"id":"reborn-as-a-demonic-tree","license":"CC0-1.0","name":"Reborn as a Demonic Tree",` +
			`"sources":[{"type":"user"}],"works":[{"position":"8","work":"reborn-as-a-demonic-tree-8"}]}`,
	})
	return dataDir
}

// rebornCopyRow is a regional libex row of the Reborn production. authors is
// the row's author array as JSON.
func rebornCopyRow(asin, region, language, format string, minutes int, authors string) string {
	return `{"asin":"` + asin + `","title":"Reborn as a Demonic Tree 8","region":"` + region + `","language":"` + language +
		`","bookFormat":"` + format + `","lengthMinutes":` + strconv.Itoa(minutes) + `,"authors":` + authors +
		`,"narrators":[{"name":"Ramon de Ocampo"}],"series":[{"name":"Reborn as a Demonic Tree","position":"8"}]}`
}

const doubledAuthors = `[{"name":"XKarnation"},{"name":"XKarnation XKarnation"}]`

// rebornCopies are the six regional rows of libex skuGroup BK_RECO_021995.
var rebornCopies = map[string]string{
	"B0GV5C28QN": "us", "B0GV5H3V89": "uk", "B0GV5G17H4": "au",
	"B0GV5M8DBN": "de", "B0GV5FD6SL": "fr", "B0GV5NYJ2Z": "jp",
}

func runCopies(t *testing.T, dataDir string, copies map[string]string, rows ...string) Summary {
	t.Helper()
	return runLibexWith(t, dataDir, Options{Mode: ModeRecordingsOnly, CopiesOf: copies}, rows...)
}

type rebornRecording struct {
	ASIN    []struct{ ASIN, Region string }
	Sources []struct{ Type, Ref string }
}

func readRebornRecording(t *testing.T, dataDir string) rebornRecording {
	t.Helper()
	var rec rebornRecording
	readEntity(t, dataDir, recAddr("reborn-as-a-demonic-tree-8", "ramon-de-ocampo-2026"), &rec)
	return rec
}

// The six copies land on the CA recording, which then carries seven ASINs;
// an unlisted row in the same run takes the ordinary recordings-only path.
func TestCopiesOfMergesOntoTheAnchorsRecording(t *testing.T) {
	dataDir := seedRebornCatalogue(t)
	copies := map[string]string{}
	var rows []string
	for asin, region := range rebornCopies {
		copies[asin] = rebornAnchor
		rows = append(rows, rebornCopyRow(asin, region, "english", "unabridged", 601, doubledAuthors))
	}
	rows = append(rows, `{"asin":"B0UNLISTED","title":"A Book Nobody Holds","region":"us","language":"english",`+
		`"authors":[{"name":"Ada Nobody"}],"narrators":[{"name":"Cal Voice"}]}`)
	sum := runCopies(t, dataDir, copies, rows...)
	if sum.CopiesAttached != 6 || sum.MergedASINs != 6 || sum.NewRecordings != 0 || sum.SkippedNoWork != 1 || len(sum.Skips) != 0 {
		t.Errorf("CopiesAttached/MergedASINs/NewRecordings/SkippedNoWork/Skips = %d/%d/%d/%d/%v, want 6/6/0/1/[]",
			sum.CopiesAttached, sum.MergedASINs, sum.NewRecordings, sum.SkippedNoWork, sum.Skips)
	}
	rec := readRebornRecording(t, dataDir)
	got := map[string]string{}
	for _, a := range rec.ASIN {
		got[a.ASIN] = a.Region
	}
	want := map[string]string{rebornAnchor: "ca"}
	for asin, region := range rebornCopies {
		want[asin] = region
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("asin = %v, want %v", got, want)
	}
	stamped := map[string]bool{}
	for _, s := range rec.Sources {
		stamped[s.Ref] = s.Type == sourceLibex
	}
	for asin := range rebornCopies {
		if !stamped[asin] {
			t.Errorf("no libex provenance for %s: %+v", asin, rec.Sources)
		}
	}
	assertTreeValid(t, dataDir)

	// A rerun is the ordinary dedup skip.
	again := runCopies(t, dataDir, copies, rows...)
	if again.CopiesAttached != 0 || again.Skipped != 6 || len(again.Files) != 0 {
		t.Errorf("rerun CopiesAttached/Skipped/Files = %d/%d/%v, want 0/6/none", again.CopiesAttached, again.Skipped, again.Files)
	}
}

// A copy whose credits are a doubled name, a narrator the catalogue has never
// seen and a series nothing holds mints no person and no series: the credits
// and series of a listed row are never read.
func TestCopiesOfMintsNoPerson(t *testing.T) {
	dataDir := seedRebornCatalogue(t)
	row := `{"asin":"B0GV5C28QN","title":"Reborn as a Demonic Tree 8","region":"us","language":"english","lengthMinutes":600,` +
		`"authors":[{"name":"XKarnation XKarnation"},{"name":"Zed Newcomer"}],"narrators":[{"name":"Somebody Unheard"}],` +
		`"series":[{"name":"A Series Nobody Holds","position":"3"}]}`
	sum := runCopies(t, dataDir, map[string]string{"B0GV5C28QN": rebornAnchor}, row)
	if sum.CopiesAttached != 1 || sum.NewPeople != 0 || sum.NewSeries != 0 || sum.NewWorks != 0 {
		t.Errorf("CopiesAttached/NewPeople/NewSeries/NewWorks = %d/%d/%d/%d, want 1/0/0/0",
			sum.CopiesAttached, sum.NewPeople, sum.NewSeries, sum.NewWorks)
	}
	for _, slug := range []string{"xkarnation-xkarnation", "zed-newcomer", "somebody-unheard"} {
		if entryExists(t, dataDir, personAddr(slug)) {
			t.Errorf("person %s was minted", slug)
		}
	}
	assertTreeValid(t, dataDir)
}

// Each physical guard refuses under its own code, and a refused copy writes
// nothing.
func TestCopiesOfRefusals(t *testing.T) {
	cases := []struct {
		name, anchor, row, code string
	}{
		{"an unknown anchor", "B0NOTHERE1", rebornCopyRow("B0GV5C28QN", "us", "english", "unabridged", 600, doubledAuthors), RefusalCopyAnchorUnknown},
		{"a runtime mismatch", rebornAnchor, rebornCopyRow("B0GV5C28QN", "us", "english", "unabridged", 300, doubledAuthors), RefusalCopyRuntime},
		{"a region the recording holds", rebornAnchor, rebornCopyRow("B0GV5C28QN", "ca", "english", "unabridged", 600, doubledAuthors), RefusalCopyRegionHeld},
		{"another language", rebornAnchor, rebornCopyRow("B0GV5C28QN", "de", "german", "unabridged", 600, doubledAuthors), RefusalCopyLanguage},
		{"an abridged copy", rebornAnchor, rebornCopyRow("B0GV5C28QN", "us", "english", "abridged", 600, doubledAuthors), RefusalCopyAbridged},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dataDir := seedRebornCatalogue(t)
			sum := runCopies(t, dataDir, map[string]string{"B0GV5C28QN": c.anchor}, c.row)
			want := []RowSkip{{ASIN: "B0GV5C28QN", Reason: c.code}}
			if sum.CopiesAttached != 0 || sum.MergedASINs != 0 || !reflect.DeepEqual(sum.Skips, want) {
				t.Errorf("CopiesAttached/MergedASINs/Skips = %d/%d/%+v, want 0/0/%+v", sum.CopiesAttached, sum.MergedASINs, sum.Skips, want)
			}
			if len(sum.Files) != 0 {
				t.Errorf("a refused copy wrote %v", sum.Files)
			}
			if rec := readRebornRecording(t, dataDir); len(rec.ASIN) != 1 {
				t.Errorf("asin = %+v, want the anchor alone", rec.ASIN)
			}
		})
	}
}

// --copies-of attaches in the recordings-only pass alone.
func TestCopiesOfOutsideRecordingsOnlyIsAnError(t *testing.T) {
	dataDir := seedRebornCatalogue(t)
	_, err := RunLibex(writeBooks(t, rebornCopyRow("B0GV5C28QN", "us", "english", "unabridged", 600, doubledAuthors)), Options{
		DataDir: dataDir, ImportDate: testImportDate, CopiesOf: map[string]string{"B0GV5C28QN": rebornAnchor},
	})
	if err == nil || !strings.Contains(err.Error(), "recordings-only") {
		t.Errorf("err = %v, want the recordings-only refusal", err)
	}
}

func TestParseCopiesOf(t *testing.T) {
	got, err := parseCopiesOf(strings.NewReader(
		`{"asin":"b0gv5c28qn","anchor":"B0GV5B7VF2"}` + "\n\n" +
			`{"asin":"B0GV5H3V89","anchor":"B0GV5B7VF2","store":"uk"}` + "\n" +
			`{"asin":"B0GV5C28QN","anchor":"B0GV5B7VF2"}` + "\n"))
	want := map[string]string{"B0GV5C28QN": rebornAnchor, "B0GV5H3V89": rebornAnchor}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("parseCopiesOf = %v, %v; want %v", got, err, want)
	}
	for _, bad := range []string{
		`{"asin":"B0GV5C28QN"}`,
		`{"asin":"not-an-asin","anchor":"B0GV5B7VF2"}`,
		`{"asin":"B0GV5B7VF2","anchor":"B0GV5B7VF2"}`,
		`{"asin":"B0GV5C28QN","anchor":"B0GV5B7VF2"}` + "\n" + `{"asin":"B0GV5C28QN","anchor":"B0GV5H3V89"}`,
		`["B0GV5C28QN","B0GV5B7VF2"]`,
	} {
		if _, err := parseCopiesOf(strings.NewReader(bad)); err == nil {
			t.Errorf("parseCopiesOf(%q) accepted it", bad)
		}
	}
}
