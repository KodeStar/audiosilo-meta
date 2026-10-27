package importer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	meta "github.com/kodestar/audiosilo-meta"
	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// The fixture snapshot (testdata/librivox/snapshot) holds four REAL responses
// of LibriVox's audiobooks feed (extended=1, coverart=1, format=json), fetched
// once on 2026-10-27 with the importer's User-Agent, `description` dropped:
//
//	527   Metamorphosis (Kafka, tr. Ian Johnston) - a solo reading, English
//	296   Short Story Collection Vol. 001 - "Various", eight readers over ten sections
//	801   Candide (version 2) (Voltaire) - LibriVox's repeat-recording marker
//	1870  Candide ou L'optimisme (Voltaire) - a French reading
const librivoxFixture = "testdata/librivox/snapshot"

// allLibrivox selects every project id.
var allLibrivox = mustLibrivoxIDs("1-")

func mustLibrivoxIDs(s string) LibrivoxIDs {
	ids, err := ParseLibrivoxIDs(s)
	if err != nil {
		panic(err)
	}
	return ids
}

// runLibrivox imports the snapshot at snap into dataDir.
func runLibrivox(t *testing.T, snap, dataDir string, ids LibrivoxIDs) Summary {
	t.Helper()
	sum, err := RunLibrivox(snap, ids, Options{DataDir: dataDir, ImportDate: testImportDate})
	if err != nil {
		t.Fatalf("librivox import: %v\nwarnings: %v", err, sum.Warnings)
	}
	return sum
}

// fixtureProject reads one project of the fixture snapshot.
func fixtureProject(t *testing.T, id int) LibrivoxProject {
	t.Helper()
	projects, err := ReadLibrivoxSnapshot(librivoxFixture)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range projects {
		if int(p.ID) == id {
			return p
		}
	}
	t.Fatalf("fixture has no project %d", id)
	return LibrivoxProject{}
}

// writeSnapshot writes projects as a one-page snapshot and returns its dir.
func writeSnapshot(t *testing.T, projects ...LibrivoxProject) string {
	t.Helper()
	dir := t.TempDir()
	body, err := json.Marshal(librivoxPage{Books: &projects})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, librivoxPagesDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, librivoxPagesDir, "offset-0000000.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

type lvRecording struct {
	ID         string                `json:"id"`
	Work       string                `json:"work"`
	Narrators  []string              `json:"narrators"`
	Language   string                `json:"language"`
	RuntimeMin int                   `json:"runtime_min"`
	Publisher  string                `json:"publisher"`
	CoverURL   string                `json:"cover_url"`
	LibrivoxID int                   `json:"librivox_id"`
	ArchiveID  string                `json:"archive_id"`
	Links      *model.RecordingLinks `json:"links"`
	Chapters   []model.Chapter       `json:"chapters"`
	Abridged   *bool                 `json:"abridged"`
	Sources    []model.Source        `json:"sources"`
}

type lvWork struct {
	Title       string         `json:"title"`
	Authors     []string       `json:"authors"`
	Credits     []model.Credit `json:"credits"`
	Language    string         `json:"language"`
	Genres      []string       `json:"genres"`
	Description string         `json:"description"`
	Sources     []model.Source `json:"sources"`
}

type lvPerson struct {
	Name     string         `json:"name"`
	ReaderID int            `json:"librivox_reader_id"`
	AuthorID int            `json:"librivox_author_id"`
	Sources  []model.Source `json:"sources"`
}

func TestLibrivoxImportFixtures(t *testing.T) {
	dataDir := t.TempDir()
	sum := runLibrivox(t, librivoxFixture, dataDir, allLibrivox)
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("imported tree failed validation:\n%v", res.Problems)
	}
	// Four projects, four works (801 and 1870 are both Candide, but one is
	// English and one French, so two works), four recordings. People: Kafka, his translator,
	// David Barnes; Various and eight readers (William Coon reads three
	// sections and is one person); Voltaire (both Candides), Ted Delorme,
	// Bernard.
	if sum.LibrivoxProjects != 4 || sum.NewWorks != 4 || sum.NewRecordings != 4 || sum.NewSeries != 0 {
		t.Errorf("projects/works/recordings/series = %d/%d/%d/%d, want 4/4/4/0\nwarnings: %v",
			sum.LibrivoxProjects, sum.NewWorks, sum.NewRecordings, sum.NewSeries, sum.Warnings)
	}
	if sum.NewPeople != 15 {
		t.Errorf("NewPeople = %d, want 15", sum.NewPeople)
	}

	// The solo book: identity, links, sections as chapters, translator credit.
	var w lvWork
	readEntity(t, dataDir, workAddr("metamorphosis"), &w)
	if w.Title != "Metamorphosis" || w.Language != "en" || w.Description != "" {
		t.Errorf("work = %+v", w)
	}
	if !reflect.DeepEqual(w.Authors, []string{"franz-kafka", "ian-johnston"}) {
		t.Errorf("authors = %v, want franz-kafka + the translator", w.Authors)
	}
	if !reflect.DeepEqual(w.Credits, []model.Credit{{Person: "ian-johnston", Role: model.RoleTranslator}}) {
		t.Errorf("credits = %v, want the translator credit", w.Credits)
	}
	if len(w.Genres) != 0 {
		t.Errorf("genres = %v; General Fiction maps to nothing", w.Genres)
	}
	if len(w.Sources) != 1 || w.Sources[0] != (model.Source{Type: "librivox", Ref: "527", ImportedAt: testImportDate}) {
		t.Errorf("work sources = %+v", w.Sources)
	}
	var r lvRecording
	readEntity(t, dataDir, recAddr("metamorphosis", "david-barnes"), &r)
	wantLinks := &model.RecordingLinks{
		Librivox: "https://librivox.org/the-metamorphosis-by-franz-kafka/",
		Zip:      "https://archive.org/compress/metamorphosis_librivox/formats=64KBPS MP3&file=/metamorphosis_librivox.zip",
		RSS:      "https://librivox.org/rss/527",
		Text:     "https://www.kafka.org/index.php?id=162,170,0,0,1,0",
	}
	if r.LibrivoxID != 527 || r.ArchiveID != "metamorphosis_librivox" || !reflect.DeepEqual(r.Links, wantLinks) {
		t.Errorf("recording identity = %d %q %+v", r.LibrivoxID, r.ArchiveID, r.Links)
	}
	if r.Publisher != LibrivoxPublisher || r.Language != "en" || r.RuntimeMin != 154 || r.Abridged != nil ||
		r.CoverURL != "https://archive.org/download/metamorphosis_librivox/metamorphosis_1009.jpg" {
		t.Errorf("recording facts = %+v", r)
	}
	wantCh := []model.Chapter{
		{Title: "I", StartMS: 0, LengthMS: 2992000, AudioURL: "https://www.archive.org/download/metamorphosis_librivox/metamorphosis_1_kafka_64kb.mp3"},
		{Title: "II", StartMS: 2992000, LengthMS: 3231000, AudioURL: "https://www.archive.org/download/metamorphosis_librivox/metamorphosis_2_kafka_64kb.mp3"},
		{Title: "III", StartMS: 6223000, LengthMS: 3042000, AudioURL: "https://www.archive.org/download/metamorphosis_librivox/metamorphosis_3_kafka_64kb.mp3"},
	}
	if !reflect.DeepEqual(r.Chapters, wantCh) {
		t.Errorf("chapters = %+v", r.Chapters)
	}
	for addr, want := range map[string][2]int{
		personAddr("david-barnes"): {94, 0},
		personAddr("franz-kafka"):  {0, 100},
		personAddr("ian-johnston"): {0, 1019},
		personAddr("voltaire"):     {0, 4298},
		personAddr("william-coon"): {286, 0},
		personAddr("various"):      {0, 18},
	} {
		var p lvPerson
		readEntity(t, dataDir, addr, &p)
		if [2]int{p.ReaderID, p.AuthorID} != want {
			t.Errorf("%s ids = reader %d author %d, want %v", addr, p.ReaderID, p.AuthorID, want)
		}
	}

	// The collection: every reader once, in section order.
	recs := recSlugsOf(t, dataDir, "short-story-collection-vol-001")
	if len(recs) != 1 {
		t.Fatalf("collection recordings = %v", recs)
	}
	readEntity(t, dataDir, recAddr("short-story-collection-vol-001", recs[0]), &r)
	wantReaders := []string{"tom-yates", "catharine-eastman", "william-coon", "peter-eastman", "betsie-bush", "eric-s-piotrowski", "eugene-pinto", "don-morgan"}
	if !reflect.DeepEqual(r.Narrators, wantReaders) || len(r.Chapters) != 10 {
		t.Errorf("collection narrators = %v (%d chapters)", r.Narrators, len(r.Chapters))
	}
	readEntity(t, dataDir, workAddr("short-story-collection-vol-001"), &w)
	if !reflect.DeepEqual(w.Genres, []string{"short-stories"}) || !reflect.DeepEqual(w.Authors, []string{"various"}) {
		t.Errorf("collection work = %+v", w)
	}

	// The version marker names a project, not a work; the French reading is
	// its own work, in its own language.
	readEntity(t, dataDir, workAddr("candide"), &w)
	if w.Title != "Candide" || w.Language != "en" {
		t.Errorf("candide = %+v", w)
	}
	readEntity(t, dataDir, workAddr("candide-ou-loptimisme"), &w)
	if w.Language != "fr" || !reflect.DeepEqual(w.Authors, []string{"voltaire"}) {
		t.Errorf("candide ou l'optimisme = %+v", w)
	}
	// Candide (version 2) numbers its front matter as section 0: the timeline
	// still starts at 0 and runs in section order.
	readEntity(t, dataDir, recAddr("candide", "ted-delorme"), &r)
	if r.LibrivoxID != 801 || len(r.Chapters) != 31 || r.Chapters[0].Title != "Chapter 00" || r.Chapters[1].StartMS != 484000 {
		t.Errorf("candide v2 recording = %d, %d chapters, first %+v", r.LibrivoxID, len(r.Chapters), r.Chapters[:2])
	}

	// No description is stored anywhere (LICENSING.md, facts only).
	for rel, body := range testpack.Snapshot(t, dataDir) {
		if strings.Contains(body, `"description"`) {
			t.Errorf("%s stores a description", rel)
		}
	}
}

func TestLibrivoxRerunChangesNothing(t *testing.T) {
	dataDir := t.TempDir()
	runLibrivox(t, librivoxFixture, dataDir, allLibrivox)
	before := testpack.Snapshot(t, dataDir)
	sum := runLibrivox(t, librivoxFixture, dataDir, allLibrivox)
	if sum.NewWorks+sum.NewRecordings+sum.NewPeople+sum.LibrivoxIDsStamped != 0 || sum.Skipped != 4 {
		t.Errorf("rerun = %+v; want nothing new and 4 already present", sum)
	}
	if len(sum.Files) != 0 {
		t.Errorf("rerun rewrote %v", sum.Files)
	}
	if after := testpack.Snapshot(t, dataDir); !reflect.DeepEqual(before, after) {
		t.Error("rerun changed the tree")
	}
}

func TestLibrivoxSelectsIDs(t *testing.T) {
	dataDir := t.TempDir()
	sum := runLibrivox(t, librivoxFixture, dataDir, mustLibrivoxIDs("527,1000-2000"))
	if sum.LibrivoxProjects != 2 || sum.NewRecordings != 2 {
		t.Fatalf("selected %d projects, %d recordings; want 527 and 1870", sum.LibrivoxProjects, sum.NewRecordings)
	}
	if entryExists(t, dataDir, workAddr("candide")) || !entryExists(t, dataDir, workAddr("candide-ou-loptimisme")) {
		t.Error("--ids selected the wrong projects")
	}
}

// seedKafka is a catalogue already holding Metamorphosis by Franz Kafka (in
// English, from another source, with another narration).
func seedKafka(t *testing.T, dataDir, author, authorName string) {
	t.Helper()
	seedTree(t, dataDir, map[string]string{
		personAddr(author):             testpack.PersonJSON(t, author, authorName),
		personAddr("someone-else"):     testpack.PersonJSON(t, "someone-else", "Someone Else"),
		workAddr("metamorphosis"):      testpack.WorkJSON(t, "metamorphosis", "Metamorphosis", testpack.WithAuthors(author)),
		recAddr("metamorphosis", "se"): testpack.RecJSON(t, "se", "metamorphosis", testpack.WithNarrators("someone-else")),
	})
}

func TestLibrivoxAttachesToTheSameWork(t *testing.T) {
	dataDir := t.TempDir()
	seedKafka(t, dataDir, "franz-kafka", "Franz Kafka")
	sum := runLibrivox(t, librivoxFixture, dataDir, mustLibrivoxIDs("527"))
	if sum.NewWorks != 0 || sum.NewRecordings != 1 {
		t.Fatalf("works/recordings = %d/%d, want the recording attached to the catalogued work\nwarnings: %v", sum.NewWorks, sum.NewRecordings, sum.Warnings)
	}
	if got := recSlugsOf(t, dataDir, "metamorphosis"); !reflect.DeepEqual(got, []string{"david-barnes", "se"}) {
		t.Errorf("recordings = %v", got)
	}
	var p lvPerson
	readEntity(t, dataDir, personAddr("franz-kafka"), &p)
	if p.AuthorID != 100 {
		t.Errorf("the catalogued author was not keyed: %+v", p)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("tree failed validation:\n%v", res.Problems)
	}
}

func TestLibrivoxDoesNotAttachToAnotherAuthorsWork(t *testing.T) {
	dataDir := t.TempDir()
	seedKafka(t, dataDir, "ovid", "Ovid")
	sum := runLibrivox(t, librivoxFixture, dataDir, mustLibrivoxIDs("527"))
	if sum.NewWorks != 1 || sum.NewRecordings != 1 {
		t.Fatalf("works/recordings = %d/%d, want a new work beside Ovid's", sum.NewWorks, sum.NewRecordings)
	}
	if got := recSlugsOf(t, dataDir, "metamorphosis"); !reflect.DeepEqual(got, []string{"se"}) {
		t.Errorf("Ovid's work gained %v", got)
	}
}

func TestLibrivoxPeopleAreKeyedByID(t *testing.T) {
	// The catalogue knows reader 94 as "Dave Barnes": the project's "David
	// Barnes" is that person, not a new one.
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{
		personAddr("dave-barnes"): testpack.WithField(t, testpack.PersonJSON(t, "dave-barnes", "Dave Barnes"), "librivox_reader_id", 94),
	})
	sum := runLibrivox(t, librivoxFixture, dataDir, mustLibrivoxIDs("527"))
	var r lvRecording
	readEntity(t, dataDir, recAddr("metamorphosis", "dave-barnes"), &r)
	if !reflect.DeepEqual(r.Narrators, []string{"dave-barnes"}) || entryExists(t, dataDir, personAddr("david-barnes")) {
		t.Errorf("narrators = %v; reader 94 must stay one person", r.Narrators)
	}
	if sum.NewPeople != 2 { // Kafka and his translator
		t.Errorf("NewPeople = %d, want 2", sum.NewPeople)
	}
}

func TestLibrivoxSharedReaderIsOnePerson(t *testing.T) {
	// Two projects of one text by one reader: two recordings of one work, one
	// person, even when the second project spells the reader differently.
	solo := fixtureProject(t, 527)
	again := fixtureProject(t, 527)
	again.ID = 99001
	again.Title = "Metamorphosis (version 2)"
	again.Sections = append([]LibrivoxSection(nil), again.Sections...)
	for i := range again.Sections {
		again.Sections[i].Readers = []LibrivoxReader{{ReaderID: 94, DisplayName: "D. Barnes"}}
	}
	dataDir := t.TempDir()
	sum := runLibrivox(t, writeSnapshot(t, solo, again), dataDir, allLibrivox)
	if sum.NewWorks != 1 || sum.NewRecordings != 2 || sum.NewPeople != 3 {
		t.Fatalf("works/recordings/people = %d/%d/%d, want 1/2/3\nwarnings: %v", sum.NewWorks, sum.NewRecordings, sum.NewPeople, sum.Warnings)
	}
	if got := recSlugsOf(t, dataDir, "metamorphosis"); !reflect.DeepEqual(got, []string{"david-barnes", "david-barnes-2"}) {
		t.Errorf("recordings = %v", got)
	}
	var r lvRecording
	readEntity(t, dataDir, recAddr("metamorphosis", "david-barnes-2"), &r)
	if r.LibrivoxID != 99001 || !reflect.DeepEqual(r.Narrators, []string{"david-barnes"}) {
		t.Errorf("second project = %d %v", r.LibrivoxID, r.Narrators)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("tree failed validation:\n%v", res.Problems)
	}
}

func TestLibrivoxKeepsAPersonsOtherID(t *testing.T) {
	// David Barnes is already reader 999 here: reader 94 resolves to him by
	// name, but his id is not overwritten (two ids cannot share one field) and
	// the run says so.
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{
		personAddr("david-barnes"): testpack.WithField(t, testpack.PersonJSON(t, "david-barnes", "David Barnes"), "librivox_reader_id", 999),
	})
	sum := runLibrivox(t, librivoxFixture, dataDir, mustLibrivoxIDs("527"))
	var p lvPerson
	readEntity(t, dataDir, personAddr("david-barnes"), &p)
	if p.ReaderID != 999 {
		t.Errorf("reader id = %d, want the recorded 999 kept", p.ReaderID)
	}
	if !strings.Contains(strings.Join(sum.Warnings, "\n"), "already carries librivox_reader_id 999") {
		t.Errorf("no warning about the kept id: %v", sum.Warnings)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("tree failed validation:\n%v", res.Problems)
	}
}

func TestLibrivoxRowRefusals(t *testing.T) {
	unknown := fixtureProject(t, 527)
	unknown.ID, unknown.Language = 99002, "Klingon"
	gappy := fixtureProject(t, 527)
	gappy.ID, gappy.Title = 99003, "Gappy Book"
	gappy.Sections = append([]LibrivoxSection(nil), gappy.Sections...)
	gappy.Sections[1].Playtime = 0
	unread := fixtureProject(t, 527)
	unread.ID, unread.Title, unread.Sections = 99004, "Unread Book", nil

	dataDir := t.TempDir()
	sum := runLibrivox(t, writeSnapshot(t, unknown, gappy, unread), dataDir, allLibrivox)
	warnings := strings.Join(sum.Warnings, "\n")
	for _, want := range []string{
		`librivox 99002: unknown language "Klingon"; skipped`,
		"librivox 99003: chapter 2 has invalid offsets; chapters omitted",
		"librivox 99004: no narrator",
	} {
		if !strings.Contains(warnings, want) {
			t.Errorf("no warning %q in:\n%s", want, warnings)
		}
	}
	if sum.NewRecordings != 1 {
		t.Fatalf("NewRecordings = %d, want only the gappy book", sum.NewRecordings)
	}
	var r lvRecording
	readEntity(t, dataDir, recAddr("gappy-book", "david-barnes"), &r)
	if r.LibrivoxID != 99003 || r.Chapters != nil {
		t.Errorf("gappy book = %d with chapters %v; want the recording without a timeline", r.LibrivoxID, r.Chapters)
	}
}

func TestLibrivoxStampsASecondTranslationsTranslator(t *testing.T) {
	// Two translations of one work in one run: the second's translator is
	// credited on the work the first created (a role credit, not an author),
	// and is keyed by their LibriVox id like everyone else.
	first := fixtureProject(t, 527)
	second := fixtureProject(t, 527)
	second.ID, second.Title = 99200, "Metamorphosis (version 2)"
	second.Translators = []LibrivoxPerson{{ID: 2000, FirstName: "Willa", LastName: "Muir"}}
	dataDir := t.TempDir()
	runLibrivox(t, writeSnapshot(t, first, second), dataDir, allLibrivox)
	var w lvWork
	readEntity(t, dataDir, workAddr("metamorphosis"), &w)
	if !slices.Contains(w.Credits, model.Credit{Person: "willa-muir", Role: model.RoleTranslator}) {
		t.Fatalf("credits = %v; want the second translator credited", w.Credits)
	}
	var p lvPerson
	readEntity(t, dataDir, personAddr("willa-muir"), &p)
	if p.AuthorID != 2000 {
		t.Errorf("willa-muir = %+v; want librivox_author_id 2000", p)
	}
}
func TestHTTPSOrEmpty(t *testing.T) {
	for in, want := range map[string]string{
		" https://librivox.org/rss/527 ":              "https://librivox.org/rss/527",
		"https://archive.org/compress/x/a b&c=/x.zip": "https://archive.org/compress/x/a b&c=/x.zip",
		"http://librivox.org/rss/527":                 "",
		"https://archive.org/download/x/100%zz.mp3":   "",
		"https:///no-host":                            "",
		"":                                            "",
	} {
		if got := httpsOrEmpty(in); got != want {
			t.Errorf("httpsOrEmpty(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseLibrivoxIDs(t *testing.T) {
	ids := mustLibrivoxIDs("5, 10-20,100-")
	for id, want := range map[int]bool{1: false, 5: true, 6: false, 10: true, 20: true, 21: false, 99: false, 100: true, 1 << 30: true} {
		if ids.Has(id) != want {
			t.Errorf("Has(%d) = %v", id, !want)
		}
	}
	for _, bad := range []string{"", "0", "x", "20-10", "-5", "1,,2"} {
		if _, err := ParseLibrivoxIDs(bad); err == nil {
			t.Errorf("ParseLibrivoxIDs(%q) accepted", bad)
		}
	}
}

func TestLibrivoxTitleStripsTheVersionMarker(t *testing.T) {
	for in, want := range map[string]string{
		"Candide (version 2)":          "Candide",
		"Candide (Version 12) ":        "Candide",
		"Candide":                      "Candide",
		"Version 2":                    "Version 2",
		"Poems (version 2) of Despair": "Poems (version 2) of Despair",
	} {
		if got := librivoxTitle(in); got != want {
			t.Errorf("librivoxTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestArchiveIDOf(t *testing.T) {
	for in, want := range map[string]string{
		"https://www.archive.org/details/metamorphosis_librivox": "metamorphosis_librivox",
		"https://archive.org/details/x.y-z/files":                "x.y-z",
		"https://librivox.org/":                                  "",
		"":                                                       "",
	} {
		if got := archiveIDOf(in); got != want {
			t.Errorf("archiveIDOf(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestLibrivoxTablesStayInVocabulary pins both LibriVox tables to what the
// schema accepts: every language code is a schema language tag, every genre a
// vocabulary value, so a table entry can never write a record metacheck refuses.
func TestLibrivoxTablesStayInVocabulary(t *testing.T) {
	for name, code := range librivoxLanguages {
		if !model.ValidLanguageTag(code) || name != strings.ToLower(name) {
			t.Errorf("librivoxLanguages[%q] = %q", name, code)
		}
	}
	vocab := testpack.SchemaDefEnum(t, "genre")
	for name, g := range librivoxGenres {
		if !vocab[g] || name != strings.ToLower(name) {
			t.Errorf("librivoxGenres[%q] = %q", name, g)
		}
	}
	if code, ok := librivoxLanguage("German"); !ok || code != "de" {
		t.Errorf("the shared word table is not consulted: %q %v", code, ok)
	}
}

func TestOnlyALibrivoxRowWritesAChapterAudioURL(t *testing.T) {
	// An OpenAudible export whose chapter rows happen to carry an audio_url key
	// imports as ever, with no audio_url: pkg/check allows one on a LibriVox
	// recording alone.
	books := `[{"asin":"B0AUDIOURL","title_short":"Chaptered","author":"A","narrated_by":"Nadia Vox","language":"english","region":"US","release_date":"2023-05-01","seconds":600,
		"chapters":[{"start_offset_ms":0,"length_ms":1000,"title":"One","audio_url":"https://example.test/1.mp3"}]}]`
	sum, dataDir := runImport(t, books, false)
	if sum.NewRecordings != 1 {
		t.Fatalf("book should import: %+v", sum)
	}
	if raw := rawEntity(t, dataDir, "works/ch/chaptered/recordings/nadia-vox-2023.json"); strings.Contains(string(raw), "audio_url") {
		t.Errorf("a non-LibriVox recording carries audio_url: %s", raw)
	}
}

// TestArchiveIDPatternIsTheSchemas is the drift guard between archiveIDRE and
// recording.schema.json's archive_id pattern: an id the importer keeps must be
// one metacheck accepts.
func TestArchiveIDPatternIsTheSchemas(t *testing.T) {
	raw, err := meta.SchemaFS.ReadFile("schema/recording.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Properties map[string]struct {
			Pattern string `json:"pattern"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	want := doc.Properties["archive_id"].Pattern
	if want == "" {
		t.Fatal("schema archive_id states no pattern; the drift guard would pass vacuously")
	}
	if got := archiveIDRE.String(); got != want {
		t.Errorf("archiveIDRE = %q, the schema's = %q", got, want)
	}
}
