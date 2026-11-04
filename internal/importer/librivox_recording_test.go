package importer

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// The LibriVox RECORDING PASS (librivoxrecording.go): the project's own title,
// genres, translators, section numbers and section readers on the recording,
// for a row that lands and for one the catalogue already holds.

// lvFidelity is a recording's fidelity fields as stored.
type lvFidelity struct {
	Narrators      []string       `json:"narrators"`
	LibrivoxTitle  string         `json:"librivox_title"`
	LibrivoxGenres []string       `json:"librivox_genres"`
	Credits        []model.Credit `json:"credits"`
	Chapters       []struct {
		Title         string   `json:"title"`
		Readers       []string `json:"readers"`
		SectionNumber *int     `json:"section_number"`
	} `json:"chapters"`
}

// lvRecordingOf reads the recording a LibriVox project landed as.
func lvRecordingOf(t *testing.T, dataDir string, project int) (work, rec string, f lvFidelity) {
	t.Helper()
	for _, a := range testpack.Addresses(t, dataDir) {
		if !strings.Contains(a, "/recordings/") {
			continue
		}
		var r lvRecording
		readEntity(t, dataDir, a, &r)
		if r.LibrivoxID == project {
			readEntity(t, dataDir, a, &f)
			return r.Work, r.ID, f
		}
	}
	t.Fatalf("no recording of project %d", project)
	return "", "", f
}

func TestLibrivoxRecordingFactsOnTheFixtures(t *testing.T) {
	dataDir := t.TempDir()
	sum := runLibrivox(t, librivoxFixture, dataDir, allLibrivox)
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("imported tree failed validation:\n%v", res.Problems)
	}
	if sum.LibrivoxRestated.Recordings != 4 || sum.LibrivoxRestated.ReadersUnresolved+sum.LibrivoxRestated.TranslatorsUnresolved != 0 {
		t.Errorf("restated = %+v, want 4 recordings and nothing unresolved", sum.LibrivoxRestated)
	}

	// 801: LibriVox's own title, markers kept, and its sections numbered from 0
	// (every chapter states its number, none equal to its position).
	_, _, candide := lvRecordingOf(t, dataDir, 801)
	if candide.LibrivoxTitle != "Candide (version 2)" || !reflect.DeepEqual(candide.LibrivoxGenres, []string{"General Fiction"}) {
		t.Errorf("801 title/genres = %q %v", candide.LibrivoxTitle, candide.LibrivoxGenres)
	}
	for i, ch := range candide.Chapters {
		if ch.SectionNumber == nil || *ch.SectionNumber != i {
			t.Errorf("801 chapter %d section_number = %v, want %d", i, deref(ch.SectionNumber), i)
		}
		if ch.Readers != nil {
			t.Errorf("801 chapter %d readers = %v: a solo reading states none", i, ch.Readers)
		}
	}

	// 296: eight readers over ten sections, each section's own reader stated,
	// and LibriVox's own 1-based numbering left unstated.
	_, _, coll := lvRecordingOf(t, dataDir, 296)
	wantReaders := []string{"tom-yates", "catharine-eastman", "william-coon", "peter-eastman", "william-coon",
		"william-coon", "betsie-bush", "eric-s-piotrowski", "eugene-pinto", "don-morgan"}
	if len(coll.Chapters) != len(wantReaders) {
		t.Fatalf("296 chapters = %d", len(coll.Chapters))
	}
	for i, ch := range coll.Chapters {
		if !reflect.DeepEqual(ch.Readers, []string{wantReaders[i]}) || ch.SectionNumber != nil {
			t.Errorf("296 chapter %d readers %v number %v, want [%s] and none", i, ch.Readers, deref(ch.SectionNumber), wantReaders[i])
		}
	}
	if coll.Credits != nil {
		t.Errorf("296 credits = %v, LibriVox states no translator", coll.Credits)
	}

	// 527: the project's translator on the recording.
	_, _, kafka := lvRecordingOf(t, dataDir, 527)
	if want := []model.Credit{{Person: "ian-johnston", Role: model.RoleTranslator}}; !reflect.DeepEqual(kafka.Credits, want) {
		t.Errorf("527 credits = %v, want %v", kafka.Credits, want)
	}
	if kafka.LibrivoxTitle != "Metamorphosis" {
		t.Errorf("527 librivox_title = %q", kafka.LibrivoxTitle)
	}

	// 1870: a genre the vocabulary does not map is still LibriVox's own.
	_, _, fr := lvRecordingOf(t, dataDir, 1870)
	if !reflect.DeepEqual(fr.LibrivoxGenres, []string{"Published before 1800"}) {
		t.Errorf("1870 librivox_genres = %v", fr.LibrivoxGenres)
	}

	rerunIsANoOp(t, librivoxFixture, dataDir)
}

// stripFidelity rewrites every LibriVox recording of dataDir as it was before
// the recording pass existed: no librivox_title, librivox_genres, credits,
// chapter readers or section numbers. It returns the stripped tree's files.
func stripFidelity(t *testing.T, dataDir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	for _, a := range testpack.Addresses(t, dataDir) {
		raw, _ := testpack.Raw(t, dataDir, a)
		if !strings.Contains(a, "/recordings/") {
			files[a] = string(raw)
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal(raw, &rec); err != nil {
			t.Fatal(err)
		}
		delete(rec, "librivox_title")
		delete(rec, "librivox_genres")
		delete(rec, "credits")
		chs, _ := rec["chapters"].([]any)
		for _, c := range chs {
			delete(c.(map[string]any), "readers")
			delete(c.(map[string]any), "section_number")
		}
		b, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		files[a] = string(b)
	}
	return files
}

func TestLibrivoxRerunBackfillsAHeldRecording(t *testing.T) {
	// A tree imported before the pass existed: re-running the import over the
	// same snapshot restates every fact, exactly as a fresh import writes it,
	// and a second rerun changes nothing.
	fresh := t.TempDir()
	runLibrivox(t, librivoxFixture, fresh, allLibrivox)
	want := testpack.Snapshot(t, fresh)

	dataDir := t.TempDir()
	seedTree(t, dataDir, stripFidelity(t, fresh))
	sum := runLibrivox(t, librivoxFixture, dataDir, allLibrivox)
	if sum.NewRecordings != 0 || sum.LibrivoxRestated.Recordings != 4 {
		t.Errorf("backfill = %d new recordings, %+v restated; want 0 and 4", sum.NewRecordings, sum.LibrivoxRestated)
	}
	if got := testpack.Snapshot(t, dataDir); !reflect.DeepEqual(got, want) {
		for k := range want {
			if got[k] != want[k] {
				t.Errorf("%s differs from a fresh import", k)
			}
		}
	}
	rerunIsANoOp(t, librivoxFixture, dataDir)
}

func TestLibrivoxRecordingCreditsAreTheProjectsOwn(t *testing.T) {
	// The catalogued Metamorphosis credits another translator (Willa Muir) and
	// Ian Johnston, LibriVox author 1019, is catalogued with that id: the
	// recording credits Ian Johnston alone, the work's credits are untouched.
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{
		personAddr("franz-kafka"):  testpack.PersonJSON(t, "franz-kafka", "Franz Kafka"),
		personAddr("willa-muir"):   testpack.PersonJSON(t, "willa-muir", "Willa Muir"),
		personAddr("ian-johnston"): testpack.WithField(t, testpack.PersonJSON(t, "ian-johnston", "Ian Johnston"), "librivox_author_id", 1019),
		personAddr("someone-else"): testpack.PersonJSON(t, "someone-else", "Someone Else"),
		workAddr("metamorphosis"): testpack.WorkJSON(t, "metamorphosis", "Metamorphosis",
			testpack.WithAuthors("franz-kafka"), testpack.WithCredits("willa-muir", "translator")),
		recAddr("metamorphosis", "se"): testpack.RecJSON(t, "se", "metamorphosis", testpack.WithNarrators("someone-else")),
	})
	runLibrivox(t, librivoxFixture, dataDir, mustLibrivoxIDs("527"))
	work, _, f := lvRecordingOf(t, dataDir, 527)
	if work != "metamorphosis" {
		t.Fatalf("527 landed on %q, want the catalogued work", work)
	}
	if want := []model.Credit{{Person: "ian-johnston", Role: model.RoleTranslator}}; !reflect.DeepEqual(f.Credits, want) {
		t.Errorf("recording credits = %v, want %v", f.Credits, want)
	}
	var w lvWork
	readEntity(t, dataDir, workAddr("metamorphosis"), &w)
	if want := []model.Credit{{Person: "willa-muir", Role: model.RoleTranslator}}; !reflect.DeepEqual(w.Credits, want) {
		t.Errorf("work credits = %v, want them untouched", w.Credits)
	}
}

func TestLibrivoxRecordingCreditNeverNamesAnotherAuthorID(t *testing.T) {
	// The only "Ian Johnston" catalogued carries ANOTHER LibriVox author id: he
	// is someone else (the person rules date the credit), so the recording
	// credits a person of the translator's own, created carrying author id 1019,
	// never the catalogued holder of 5555.
	dataDir := t.TempDir()
	seedKafka(t, dataDir, "franz-kafka", "Franz Kafka")
	seedTree(t, dataDir, map[string]string{
		personAddr("ian-johnston"): testpack.WithField(t, testpack.PersonJSON(t, "ian-johnston", "Ian Johnston"), "librivox_author_id", 5555),
	})
	runLibrivox(t, librivoxFixture, dataDir, mustLibrivoxIDs("527"))
	_, _, f := lvRecordingOf(t, dataDir, 527)
	if len(f.Credits) != 1 || f.Credits[0].Person == "ian-johnston" {
		t.Fatalf("credits = %v, want one translator who is not ian-johnston", f.Credits)
	}
	if p := personOf(t, dataDir, f.Credits[0].Person); p.AuthorID != 1019 {
		t.Errorf("%s carries author id %d, want 1019", f.Credits[0].Person, p.AuthorID)
	}
	if p := personOf(t, dataDir, "ian-johnston"); p.AuthorID != 5555 {
		t.Errorf("ian-johnston carries author id %d, want 5555 kept", p.AuthorID)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("tree failed validation:\n%v", res.Problems)
	}
}

func TestLibrivoxUnsluggableTranslatorIsLeftOff(t *testing.T) {
	// A translator whose name slugs to nothing is credited nowhere (never the
	// shared catch-all person): left off the recording and counted.
	pr := fixtureProject(t, 527)
	pr.Translators = []LibrivoxPerson{{ID: 7777, FirstName: "", LastName: "Ἰωάννης"}}
	dataDir := t.TempDir()
	sum := runLibrivox(t, writeSnapshot(t, pr), dataDir, allLibrivox)
	if sum.LibrivoxRestated.TranslatorsUnresolved != 1 || sum.LibrivoxRestated.TranslatorsCreated != 0 {
		t.Errorf("restated = %+v, want one unresolved translator", sum.LibrivoxRestated)
	}
	if _, _, f := lvRecordingOf(t, dataDir, 527); f.Credits != nil {
		t.Errorf("credits = %v, want none", f.Credits)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("tree failed validation:\n%v", res.Problems)
	}
}

func TestLibrivoxSoloReaderWithAnUnreadSection(t *testing.T) {
	// One reader, but a section names nobody: the other sections state their
	// reader and the unread one states none, as LibriVox does.
	pr := fixtureProject(t, 527)
	pr.Sections = append([]LibrivoxSection(nil), pr.Sections...)
	pr.Sections[1].Readers = nil
	dataDir := t.TempDir()
	runLibrivox(t, writeSnapshot(t, pr), dataDir, allLibrivox)
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("tree failed validation:\n%v", res.Problems)
	}
	_, _, f := lvRecordingOf(t, dataDir, 527)
	var got [][]string
	for _, ch := range f.Chapters {
		got = append(got, ch.Readers)
	}
	if want := [][]string{{"david-barnes"}, nil, {"david-barnes"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("chapter readers = %v, want %v", got, want)
	}
}

func TestLibrivoxSectionReaderOffTheNarratorsIsNotRecorded(t *testing.T) {
	// A held recording crediting someone the row's reader does not resolve to:
	// no chapter reader is guessed, and the run counts it.
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{
		personAddr("someone-else"): testpack.PersonJSON(t, "someone-else", "Someone Else"),
		personAddr("franz-kafka"):  testpack.PersonJSON(t, "franz-kafka", "Franz Kafka"),
		workAddr("metamorphosis"):  testpack.WorkJSON(t, "metamorphosis", "Metamorphosis", testpack.WithAuthors("franz-kafka")),
		recAddr("metamorphosis", "se"): testpack.WithFields(t, testpack.RecJSON(t, "se", "metamorphosis",
			testpack.WithNarrators("someone-else")), map[string]any{"librivox_id": 91002, "chapters": []map[string]any{
			{"title": "I", "start_ms": 0, "length_ms": 1000},
			{"title": "II", "start_ms": 1000, "length_ms": 1000},
			{"title": "III", "start_ms": 2000, "length_ms": 1000},
		}}),
	})
	pr := readProject(t, 91002, LibrivoxReader{ReaderID: 634, DisplayName: "Martina"}, LibrivoxReader{ReaderID: 94, DisplayName: "David Barnes"})
	sum := runLibrivox(t, writeSnapshot(t, pr), dataDir, allLibrivox)
	if sum.LibrivoxRestated.ReadersUnresolved != 1 {
		t.Errorf("unresolved readers = %d, want 1 (warnings %v)", sum.LibrivoxRestated.ReadersUnresolved, sum.Warnings)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("tree failed validation:\n%v", res.Problems)
	}
	for i, ch := range lvFidelityOf(t, dataDir, "metamorphosis", "se").Chapters {
		if ch.Readers != nil {
			t.Errorf("chapter %d readers = %v, want none recorded", i, ch.Readers)
		}
	}
}

// lvFidelityOf reads one recording's fidelity fields.
func lvFidelityOf(t *testing.T, dataDir, work, rec string) lvFidelity {
	t.Helper()
	var f lvFidelity
	readEntity(t, dataDir, recAddr(work, rec), &f)
	return f
}

// heldKafka seeds fixture 527 as held, Franz Kafka (LibriVox author 100)
// carrying kafka's fields.
func heldKafka(t *testing.T, kafka map[string]any) string {
	t.Helper()
	fresh := t.TempDir()
	runLibrivox(t, librivoxFixture, fresh, mustLibrivoxIDs("527"))
	files := stripFidelity(t, fresh)
	files[personAddr("franz-kafka")] = testpack.WithFields(t, testpack.PersonJSON(t, "franz-kafka", "Franz Kafka"), kafka)
	dataDir := t.TempDir()
	seedTree(t, dataDir, files)
	return dataDir
}

func TestLibrivoxLifeYearsFilledOnAHeldAuthor(t *testing.T) {
	// LibriVox states 1883-1924; the person carries the author id but no years
	// (the id was written before the years were read): both are filled.
	dataDir := heldKafka(t, map[string]any{"librivox_author_id": 100})
	sum := runLibrivox(t, librivoxFixture, dataDir, allLibrivox)
	if sum.LibrivoxRestated.LifeYearsFilled != 1 {
		t.Errorf("filled = %d, want 1", sum.LibrivoxRestated.LifeYearsFilled)
	}
	if p := personOf(t, dataDir, "franz-kafka"); p.BirthYear != 1883 || p.DeathYear != 1924 {
		t.Errorf("years = %d-%d, want 1883-1924", p.BirthYear, p.DeathYear)
	}
	rerunIsANoOp(t, librivoxFixture, dataDir)
}

func TestLibrivoxLifeYearsFillTheMissingHalf(t *testing.T) {
	dataDir := heldKafka(t, map[string]any{"librivox_author_id": 100, "birth_year": 1883})
	runLibrivox(t, librivoxFixture, dataDir, allLibrivox)
	if p := personOf(t, dataDir, "franz-kafka"); p.BirthYear != 1883 || p.DeathYear != 1924 {
		t.Errorf("years = %d-%d, want 1883-1924", p.BirthYear, p.DeathYear)
	}
}

func TestLibrivoxLifeYearsNeverOverwriteAStatedYear(t *testing.T) {
	// The record states 1882: LibriVox's 1883 contradicts it, so nothing is
	// written (not even the death year beside it) and the run warns.
	dataDir := heldKafka(t, map[string]any{"librivox_author_id": 100, "birth_year": 1882})
	sum := runLibrivox(t, librivoxFixture, dataDir, allLibrivox)
	if sum.LibrivoxRestated.LifeYearConflicts != 1 || sum.LibrivoxRestated.LifeYearsFilled != 0 {
		t.Errorf("restated = %+v, want one conflict and nothing filled", sum.LibrivoxRestated)
	}
	if p := personOf(t, dataDir, "franz-kafka"); p.BirthYear != 1882 || p.DeathYear != 0 {
		t.Errorf("years = %d-%d, want 1882 kept and no death year", p.BirthYear, p.DeathYear)
	}
	found := false
	for _, w := range sum.Warnings {
		found = found || strings.Contains(w, `person "franz-kafka" states life years 1882-?, LibriVox author 100`)
	}
	if !found {
		t.Errorf("no conflict warning in %v", sum.Warnings)
	}
}

// TestYearsTextSpellsBCAsTheRunDoes: a conflict warning prints a year BC as
// datedName does ("43 BC"), never as the signed number.
func TestYearsTextSpellsBCAsTheRunDoes(t *testing.T) {
	for _, tc := range []struct {
		birth, death int
		want         string
	}{
		{1883, 1924, "1883-1924"},
		{-106, -43, "106 BC-43 BC"},
		{1938, 0, "1938-?"},
		{0, 0, "?-?"},
	} {
		if got := yearsText(tc.birth, tc.death); got != tc.want {
			t.Errorf("yearsText(%d, %d) = %q, want %q", tc.birth, tc.death, got, tc.want)
		}
	}
	if y, ok := rawYear(json.Number("-43")); y != -43 || !ok {
		t.Errorf("rawYear(-43) = %d %v", y, ok)
	}
	if _, ok := rawYear(nil); ok {
		t.Error("rawYear(nil) states a year")
	}
}
