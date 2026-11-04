package importer

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// The ISBN rules of meta#2827, over the seedTierTree catalogue. gapAttested is its
// recording bea-reader-2019 with a user-tier source, 605 minutes, dated 2019-05-04:
// the record an ASIN-less row may add an ISBN to.
const (
	gapISBN      = "9781427210005"
	gapOtherISBN = "9781427210012"
	// gapPrint10 and gapPrint13 are one print ISBN in its two spellings.
	gapPrint10 = "0870111175"
	gapPrint13 = "9780870111174"
	// gapNarrator reads the seeded recording.
	gapNarrator = "Bea Reader"
)

// gapAttested is the seed with the recording and work attested, which is what an
// ASIN-less row needs before it may add anything; extra entries override it.
func gapAttested(t *testing.T, extra map[string]string) string {
	t.Helper()
	files := map[string]string{tierRecRel: tierRecordingAttested, tierWorkRel: tierWorkAttested}
	for rel, content := range extra {
		files[rel] = content
	}
	return seedTierTree(t, files)
}

// gapRow is an audiosilo-books row for the seeded book, read by narrator. extra is
// spliced in as more JSON members, and an empty asin leaves the row ASIN-less.
func gapRow(narrator, asin, isbn, extra string) string {
	row := fmt.Sprintf(`"title":"The Lost Cartographer","authors":["Ada Mapmaker"],"narrators":[%q],"language":"en"`, narrator)
	if asin != "" {
		row += fmt.Sprintf(`,"asin":%q`, asin)
	}
	if isbn != "" {
		row += fmt.Sprintf(`,"isbn":%q`, isbn)
	}
	if extra != "" {
		row += "," + extra
	}
	return `{"format":"audiosilo-books","version":1,"books":[{` + row + `}]}`
}

func runGapRow(t *testing.T, dataDir, env string) Summary {
	t.Helper()
	sum, err := RunAudiosiloBooks(writeBooks(t, env), Options{DataDir: dataDir, ImportDate: testImportDate})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	return sum
}

func gapRecording(t *testing.T, dataDir string) recordingFile {
	t.Helper()
	var rec recordingFile
	readEntity(t, dataDir, tierRecRel, &rec)
	return rec
}

// gapTreeWithISBN holds a second recording of the work that already carries
// gapOtherISBN, under another narrator, so a claim has somebody to collide with.
func gapTreeWithISBN(t *testing.T) string {
	t.Helper()
	other := strings.ReplaceAll(tierRecordingAttested, "bea-reader-2019", "cy-voice-2019")
	other = strings.ReplaceAll(other, `"narrators":["bea-reader"]`, `"narrators":["cy-voice"]`)
	other = strings.ReplaceAll(other, "B0LIBEX001", "B0LIBEX002")
	other = strings.Replace(other, `"id":`, fmt.Sprintf(`"isbn":[%q],"id":`, gapOtherISBN), 1)
	return gapAttested(t, map[string]string{
		"people/cy/cy-voice.json":                                      `{"id":"cy-voice","license":"CC0-1.0","name":"Cy Voice","sources":[{"imported_at":"2026-01-05","ref":"B0LIBEX002","type":"libex-import"}]}`,
		"works/th/the-lost-cartographer/recordings/cy-voice-2019.json": other,
	})
}

// gapPrintWork is the seeded work holding the print ISBN at xref.isbn.
func gapPrintWork() map[string]string {
	work := strings.Replace(tierWorkAttested, `"id":`, fmt.Sprintf(`"xref":{"isbn":[%q]},"id":`, gapPrint10), 1)
	return map[string]string{tierWorkRel: work}
}

// A row adds its ISBN to an attested recording and nothing else, whether it names the
// recording by ASIN (first writer wins, but ISBNs are only ever appended) or passes the
// ASIN-less test: its other facts disagree with the record and stand down, the source
// is stamped so the ISBN can be retracted with it, and the same export again writes
// nothing.
func TestRowAddsISBNToAttestedRecording(t *testing.T) {
	for _, tc := range []struct{ name, env, wantRef string }{
		{"no ASIN", gapRow(gapNarrator, "", gapISBN, `"runtime_min":605,"release_date":"2021-03-04","publisher":"Other Imprint"`), "isbn:" + gapISBN},
		{"exact ASIN", gapRow(gapNarrator, "B0LIBEX001", gapISBN, `"runtime_min":600,"publisher":"Somebody Else"`), "B0LIBEX001"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := gapAttested(t, nil)
			before := gapRecording(t, dataDir)

			sum := runGapRow(t, dataDir, tc.env)

			if sum.ISBNRecordings != 1 || sum.Produced() != 1 {
				t.Errorf("ISBNRecordings/Produced = %d/%d, want 1/1 (a run that only adds an ISBN changed the tree)", sum.ISBNRecordings, sum.Produced())
			}
			if sum.NewRecordings != 0 || sum.AttestedRecordings != 0 {
				t.Errorf("summary = %+v, want no new recording and no attestation", sum)
			}
			rec := gapRecording(t, dataDir)
			if !slices.Equal(rec.ISBN, []string{gapISBN}) {
				t.Errorf("isbn = %v, want [%s]", rec.ISBN, gapISBN)
			}
			if rec.RuntimeMin != before.RuntimeMin || rec.Publisher != before.Publisher || rec.ReleaseDate != before.ReleaseDate || rec.CoverURL != before.CoverURL {
				t.Errorf("a recorded fact moved: %+v", rec)
			}
			if last := rec.Sources[len(rec.Sources)-1]; last.Type != model.SourceAudiosiloBooksImport || last.Ref != tc.wantRef {
				t.Errorf("sources = %+v, want the run's stamp appended with ref %q", rec.Sources, tc.wantRef)
			}
			if res := check.Load(dataDir); !res.OK() {
				t.Fatalf("tree failed validation:\n%v", res.Problems)
			}

			snapshot := snapshotTree(t, dataDir)
			again := runGapRow(t, dataDir, tc.env)
			if again.ISBNRecordings != 0 || again.Produced() != 0 {
				t.Errorf("re-run ISBNRecordings/Produced = %d/%d, want 0/0", again.ISBNRecordings, again.Produced())
			}
			assertTreeUnchanged(t, dataDir, snapshot)
		})
	}
}

// Every way the ASIN-less test refuses: the record is untouched and, where the
// refusal is the row's alone to explain, the warning says why.
func TestAsinlessRowISBNRefusals(t *testing.T) {
	second := strings.ReplaceAll(tierRecordingAttested, "bea-reader-2019", "bea-reader-2023")
	second = strings.ReplaceAll(second, "B0LIBEX001", "B0LIBEX002")
	noRuntime := strings.Replace(tierRecordingAttested, `"runtime_min":605,`, "", 1)

	for _, tc := range []struct {
		name  string
		seed  map[string]string
		row   string
		warns string
	}{
		{name: "two same-narrator recordings",
			seed:  map[string]string{"works/th/the-lost-cartographer/recordings/bea-reader-2023.json": second},
			row:   gapRow(gapNarrator, "", gapISBN, `"runtime_min":600`),
			warns: "2 same-narrator recordings"},
		{name: "runtime outside the window",
			row:   gapRow(gapNarrator, "", gapISBN, `"runtime_min":400`),
			warns: "runs 605 minutes and this row 400"},
		{name: "row states no runtime",
			row:   gapRow(gapNarrator, "", gapISBN, ""),
			warns: "do not both state a runtime"},
		{name: "recording states no runtime",
			seed:  map[string]string{tierRecRel: noRuntime},
			row:   gapRow(gapNarrator, "", gapISBN, `"runtime_min":600`),
			warns: "do not both state a runtime"},
		{name: "row year well before the recording's, as a print edition's",
			row:   gapRow(gapNarrator, "", gapISBN, `"runtime_min":600,"release_date":"1982"`),
			warns: "well before recording"},
		{name: "a print ISBN of the work",
			seed:  gapPrintWork(),
			row:   gapRow(gapNarrator, "", gapPrint13, `"runtime_min":600`),
			warns: "print ISBN"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := gapAttested(t, tc.seed)
			before := snapshotTree(t, dataDir)
			sum := runGapRow(t, dataDir, tc.row)
			if sum.ISBNRecordings != 0 {
				t.Errorf("ISBNRecordings = %d, want 0", sum.ISBNRecordings)
			}
			if !hasWarning(sum.Warnings, tc.warns) {
				t.Errorf("warnings = %v, want one saying %q", sum.Warnings, tc.warns)
			}
			assertTreeUnchanged(t, dataDir, before)
		})
	}
}

// A bulk-mirror-only recording never takes an ISBN from a row with no ASIN, however
// well it fits: the stamp would end its status on a match that identifies nothing.
// The refusal is counted and reported, the tree is untouched, the value stays free,
// and the later row that does name the ASIN attests the record and carries the ISBN.
func TestAsinlessRowISBNRefusedOnAMirrorOnlyRecording(t *testing.T) {
	dataDir := seedTierTree(t, nil)
	before := snapshotTree(t, dataDir)
	sum := runGapRow(t, dataDir, gapRow(gapNarrator, "", gapISBN, `"runtime_min":600`))
	if sum.ISBNRecordings != 0 || sum.ISBNsMirrorOnly != 1 || sum.Produced() != 0 {
		t.Errorf("ISBNRecordings/ISBNsMirrorOnly/Produced = %d/%d/%d, want 0/1/0", sum.ISBNRecordings, sum.ISBNsMirrorOnly, sum.Produced())
	}
	if !hasWarning(sum.Warnings, "isbn-not-attached: mirror-only") {
		t.Errorf("warnings = %v, want the refusal reported", sum.Warnings)
	}
	assertTreeUnchanged(t, dataDir, before)

	withASIN := runGapRow(t, dataDir, gapRow(gapNarrator, "B0LIBEX001", gapISBN, `"runtime_min":600`))
	if withASIN.AttestedRecordings != 1 {
		t.Errorf("AttestedRecordings = %d, want 1", withASIN.AttestedRecordings)
	}
	if got := gapRecording(t, dataDir).ISBN; !slices.Equal(got, []string{gapISBN}) {
		t.Errorf("isbn = %v, want [%s] carried by the row that names the ASIN", got, gapISBN)
	}
}

// A row that fails another test is reported under that test, so the count means
// rows the mirror-only rule alone held back.
func TestAsinlessRowISBNMirrorOnlyCountsOnlyWhatItAloneRefused(t *testing.T) {
	dataDir := seedTierTree(t, nil)
	if sum := runGapRow(t, dataDir, gapRow(gapNarrator, "", gapISBN, `"runtime_min":400`)); sum.ISBNsMirrorOnly != 0 {
		t.Errorf("ISBNsMirrorOnly = %d, want 0: the runtime refused this row first", sum.ISBNsMirrorOnly)
	}
}

// A row with nothing the claim would take has lost nothing to the mirror-only rule:
// it is not counted, and reaching the claim stamps nothing on the mirror's record.
func TestAsinlessRowISBNMirrorOnlyCountsOnlyAValueTheClaimWouldTake(t *testing.T) {
	holding := strings.Replace(tierRecording, `"id":`, fmt.Sprintf(`"isbn":[%q],"id":`, gapISBN), 1)
	for _, tc := range []struct {
		name  string
		seed  map[string]string
		isbn  string
		warns string
	}{
		{name: "a value the record already holds", seed: map[string]string{tierRecRel: holding}, isbn: gapISBN},
		{name: "a work's print ISBN", seed: gapPrintWork(), isbn: gapPrint13, warns: "print ISBN"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := seedTierTree(t, tc.seed)
			before := snapshotTree(t, dataDir)
			sum := runGapRow(t, dataDir, gapRow(gapNarrator, "", tc.isbn, `"runtime_min":600`))
			if sum.ISBNsMirrorOnly != 0 || hasWarning(sum.Warnings, "isbn-not-attached") {
				t.Errorf("ISBNsMirrorOnly = %d, warnings = %v, want the row not reported as mirror-only", sum.ISBNsMirrorOnly, sum.Warnings)
			}
			if tc.warns != "" && !hasWarning(sum.Warnings, tc.warns) {
				t.Errorf("warnings = %v, want one saying %q", sum.Warnings, tc.warns)
			}
			assertTreeUnchanged(t, dataDir, before)
		})
	}
}

// gapEnvelope is an audiosilo-books export of rows, each the JSON members of a book.
func gapEnvelope(rows ...string) string {
	return `{"format":"audiosilo-books","version":1,"books":[{` + strings.Join(rows, `},{`) + `}]}`
}

// Summary.ISBNRecordings counts recordings: two rows adding to one count once.
func TestISBNRecordingsCountsARecordingOnce(t *testing.T) {
	dataDir := gapAttested(t, nil)
	const book = `"title":"The Lost Cartographer","authors":["Ada Mapmaker"],"narrators":["Bea Reader"],"language":"en","runtime_min":600`
	sum := runGapRow(t, dataDir, gapEnvelope(book+`,"isbn":"`+gapISBN+`"`, book+`,"isbn":"`+gapOtherISBN+`"`))
	if sum.ISBNRecordings != 1 {
		t.Errorf("ISBNRecordings = %d, want 1", sum.ISBNRecordings)
	}
	if got := gapRecording(t, dataDir).ISBN; !slices.Equal(got, []string{gapISBN, gapOtherISBN}) {
		t.Errorf("isbn = %v, want both", got)
	}
}

// A sibling row of an ASIN this run landed adds its ISBN in this run, so the next run
// over the same export writes nothing. One contradicting the record adds nothing, the
// answer that next run would give. Neither is a conflict: the run wrote the record.
func TestSiblingRowOfALandedASINAddsItsISBN(t *testing.T) {
	const book = `"title":"Foo","authors":["Ada Mapmaker"],"narrators":["Bea Reader"],"language":"en","asin":"B0SIBLING1"`
	const rel = "works/fo/foo/recordings/bea-reader.json"
	recISBN := func(t *testing.T, dataDir string) []string {
		t.Helper()
		var rec recordingFile
		readEntity(t, dataDir, rel, &rec)
		return rec.ISBN
	}
	t.Run("agreeing", func(t *testing.T) {
		dataDir := t.TempDir()
		env := gapEnvelope(book+`,"runtime_min":600`, book+`,"runtime_min":600,"isbn":"`+gapISBN+`"`)
		sum := runGapRow(t, dataDir, env)
		if sum.NewRecordings != 1 || sum.Conflicts != 0 {
			t.Errorf("NewRecordings/Conflicts = %d/%d, want 1/0", sum.NewRecordings, sum.Conflicts)
		}
		if got := recISBN(t, dataDir); !slices.Equal(got, []string{gapISBN}) {
			t.Errorf("isbn = %v, want [%s]", got, gapISBN)
		}
		before := snapshotTree(t, dataDir)
		if again := runGapRow(t, dataDir, env); again.ISBNRecordings != 0 {
			t.Errorf("re-run ISBNRecordings = %d, want 0", again.ISBNRecordings)
		}
		assertTreeUnchanged(t, dataDir, before)
	})
	t.Run("contradicting", func(t *testing.T) {
		dataDir := t.TempDir()
		sum := runGapRow(t, dataDir, gapEnvelope(book+`,"runtime_min":600`, book+`,"runtime_min":300,"isbn":"`+gapISBN+`"`))
		if sum.ISBNRecordings != 0 || sum.Conflicts != 0 {
			t.Errorf("ISBNRecordings/Conflicts = %d/%d, want 0/0", sum.ISBNRecordings, sum.Conflicts)
		}
		if got := recISBN(t, dataDir); len(got) != 0 {
			t.Errorf("isbn = %v, want none", got)
		}
	})
}

// A year just before the recording's is an audio listing's own date, which rows
// and mirrors routinely disagree on by one: not the print-edition signal.
func TestAsinlessRowISBNAllowsANearbyYear(t *testing.T) {
	dataDir := gapAttested(t, nil)
	if sum := runGapRow(t, dataDir, gapRow(gapNarrator, "", gapISBN, `"runtime_min":600,"release_date":"2018"`)); sum.ISBNRecordings != 1 {
		t.Fatalf("ISBNRecordings = %d, want 1: %v", sum.ISBNRecordings, sum.Warnings)
	}
}

// Held by another recording: refused with the existing warning, no stamp written.
func TestAsinlessRowISBNHeldElsewhere(t *testing.T) {
	dataDir := gapTreeWithISBN(t)
	before := snapshotTree(t, dataDir)
	sum := runGapRow(t, dataDir, gapRow(gapNarrator, "", gapOtherISBN, `"runtime_min":600`))
	if sum.ISBNRecordings != 0 || !hasWarning(sum.Warnings, "already recorded on another recording") {
		t.Errorf("ISBNRecordings = %d, warnings = %v, want 0 and the collision named", sum.ISBNRecordings, sum.Warnings)
	}
	assertTreeUnchanged(t, dataDir, before)
}

// A row at another position of a series than the recording's is another volume,
// which the same-narrator match cannot see. Asked of the planner directly: end to
// end the serial resolution has usually sent such a row to its own work by now.
func TestAsinlessRowISBNRefusedOnASeriesPositionConflict(t *testing.T) {
	b := sourceBook{isbns: []string{gapISBN}, evidence: runtimeEvidence{min: 600}, series: []seriesRef{makeSeriesRef("Bravelands", "4")}}
	m := recCandidate{slug: "bea-reader-2019", info: &recInfo{
		known:  runtimeEvidence{min: 600},
		claims: []posClaim{{name: "BRAVELANDS", key: "name:bravelands", pos: rowPosition{source: "1"}}},
	}}
	var warned []string
	warn := func(format string, args ...any) { warned = append(warned, fmt.Sprintf(format, args...)) }
	p := &planner{}
	p.attachRowISBNs(&workState{slug: "bravelands"}, []recCandidate{m}, b, rowSeriesClaims(b, "Bravelands"), nil, warn)
	if p.summary.ISBNRecordings != 0 || !hasWarning(warned, `this row claims "4"`) {
		t.Errorf("ISBNRecordings = %d, warnings = %v, want 0 and the conflict named", p.summary.ISBNRecordings, warned)
	}
}

// A row stating abridged against a recording this run stated unabridged: the
// disk model cannot tell false from absent, so the guard's reach is the same run.
func TestAsinlessRowISBNRefusedOnAbridgedConflict(t *testing.T) {
	dataDir := t.TempDir()
	const first = `"title":"Foo","authors":["Ada Mapmaker"],"narrators":["Bea Reader"],"language":"en","runtime_min":600`
	env := `{"format":"audiosilo-books","version":1,"books":[{` + first + `,"abridged":false},{` + first + `,"abridged":true,"isbn":"` + gapISBN + `"}]}`
	sum := runGapRow(t, dataDir, env)
	if sum.ISBNRecordings != 0 || !hasWarning(sum.Warnings, "disagree on abridged") {
		t.Errorf("ISBNRecordings = %d, warnings = %v, want 0 and the abridged disagreement named", sum.ISBNRecordings, sum.Warnings)
	}
}

// The row a guard refuses adds no ISBN either, and one held elsewhere stays there.
func TestExactASINRowISBNRefusals(t *testing.T) {
	t.Run("the row contradicts the record", func(t *testing.T) {
		dataDir := gapAttested(t, nil)
		before := snapshotTree(t, dataDir)
		sum := runGapRow(t, dataDir, gapRow(gapNarrator, "B0LIBEX001", gapISBN, `"runtime_min":300`))
		if sum.ISBNRecordings != 0 || sum.Conflicts != 1 {
			t.Errorf("ISBNRecordings/Conflicts = %d/%d, want 0/1", sum.ISBNRecordings, sum.Conflicts)
		}
		assertTreeUnchanged(t, dataDir, before)
	})
	t.Run("another recording holds the ISBN", func(t *testing.T) {
		dataDir := gapTreeWithISBN(t)
		before := snapshotTree(t, dataDir)
		sum := runGapRow(t, dataDir, gapRow(gapNarrator, "B0LIBEX001", gapOtherISBN, ""))
		if sum.ISBNRecordings != 0 || !hasWarning(sum.Warnings, "already recorded on another recording") {
			t.Errorf("ISBNRecordings = %d, warnings = %v", sum.ISBNRecordings, sum.Warnings)
		}
		assertTreeUnchanged(t, dataDir, before)
	})
}

// meta#2827's third gap: a work's print ISBN is claimed by no door, under either
// spelling, whichever tier the row comes from. Every other ISBN claims as it did.
func TestPrintISBNIsNotClaimed(t *testing.T) {
	newRow := func(isbn string) string { return gapRow("Dee Newcomer", "", isbn, "") }
	recISBN := func(t *testing.T, dataDir string) []string {
		t.Helper()
		var rec recordingFile
		readEntity(t, dataDir, "works/th/the-lost-cartographer/recordings/dee-newcomer.json", &rec)
		return rec.ISBN
	}

	for name, isbn := range map[string]string{"same spelling": gapPrint10, "the other spelling": gapPrint13} {
		t.Run("a new recording, "+name, func(t *testing.T) {
			dataDir := seedTierTree(t, gapPrintWork())
			sum := runGapRow(t, dataDir, newRow(isbn))
			if sum.NewRecordings != 1 {
				t.Fatalf("NewRecordings = %d, want 1: the recording is still real", sum.NewRecordings)
			}
			if got := recISBN(t, dataDir); len(got) != 0 {
				t.Errorf("isbn = %v, want none", got)
			}
			if !hasWarning(sum.Warnings, "print ISBN") {
				t.Errorf("warnings = %v, want the refusal named", sum.Warnings)
			}
		})
	}
	t.Run("a new recording, an ISBN no work holds", func(t *testing.T) {
		dataDir := seedTierTree(t, gapPrintWork())
		runGapRow(t, dataDir, newRow(gapISBN))
		if got := recISBN(t, dataDir); !slices.Equal(got, []string{gapISBN}) {
			t.Errorf("isbn = %v, want [%s]", got, gapISBN)
		}
	})
	t.Run("libex enrichment", func(t *testing.T) {
		dataDir := seedTierTree(t, gapPrintWork())
		row, err := json.Marshal([]map[string]any{{
			"asin": "B0LIBEX001", "title": "The Lost Cartographer", "region": "us", "language": "english", "lengthMinutes": 600,
			"isbn": gapPrint13, "authors": []map[string]string{{"name": "Ada Mapmaker"}}, "narrators": []map[string]string{{"name": "Bea Reader"}},
		}})
		if err != nil {
			t.Fatal(err)
		}
		sum, err := RunLibex(writeBooks(t, string(row)), Options{DataDir: dataDir, ImportDate: testImportDate, Mode: ModeEnrich})
		if err != nil {
			t.Fatal(err)
		}
		if got := gapRecording(t, dataDir).ISBN; len(got) != 0 {
			t.Errorf("isbn = %v, want none", got)
		}
		if !hasWarning(sum.Warnings, "print ISBN") {
			t.Errorf("warnings = %v, want the refusal named", sum.Warnings)
		}
	})
}

func TestYearPredates(t *testing.T) {
	for _, tc := range []struct {
		date, other string
		want        bool
	}{
		{"1982", "2010-05-07", true},
		{"2008", "2010", true},
		{"2009", "2010", false},
		{"2010", "2010-05-07", false},
		{"2024", "1999-12-16", false},
		{"", "2010", false},
		{"2010", "", false},
	} {
		if got := yearPredates(tc.date, tc.other); got != tc.want {
			t.Errorf("yearPredates(%q, %q) = %v, want %v", tc.date, tc.other, got, tc.want)
		}
	}
}

// AttestAt loads no catalogue, so a print ISBN is its caller's to refuse, as
// applyTakeover does. One that gets through is an error from the post-write validation,
// never a recording quietly holding it.
func TestAttestAtLeavesAPrintISBNToItsCaller(t *testing.T) {
	dataDir := seedTierTree(t, gapPrintWork())
	store, err := openStore(dataDir, "")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	_, err = AttestAt(store, RecRef{Work: "the-lost-cartographer", Rec: "bea-reader-2019"}, nil,
		Attestation{ASIN: "B0LIBEX001", Source: formSource, ISBNs: []string{gapPrint13}},
		Options{DataDir: dataDir, ImportDate: testImportDate})
	if err == nil || !strings.Contains(err.Error(), "print ISBN") {
		t.Errorf("err = %v, want the validation to name the print ISBN", err)
	}
}
