package importer

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/check"
)

// TestParseOpenAudibleDuration pins the duration spellings a real books.json
// carries. The current OpenAudible export writes ONLY "duration", as hours and
// minutes ("13:25" on a 13h25m book, "00:04" on a four-minute one - measured on
// two published exports against the books' known lengths), and older builds
// wrote "H:MM:SS" beside a seconds field. Anything that is not one of those two
// shapes is no runtime at all, never a guess.
func TestParseOpenAudibleDuration(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
		ok   bool
	}{
		{"13:25", 805, true},
		{"00:04", 4, true},
		{"01:07", 67, true},
		{"26:24", 1584, true},
		{" 9:17 ", 557, true},
		{"12:03:40", 724, true}, // seconds round half up
		{"12:03:29", 723, true},
		{"0:00:40", 1, true},
		{"", 0, false},
		{"00:00", 0, false},
		{"13", 0, false},
		{"13:75", 0, false},
		{"1:2:3:4", 0, false},
		{"ab:cd", 0, false},
		{"-1:30", 0, false},
		{"+1:30", 0, false},
	} {
		got, ok := parseOpenAudibleDuration(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("parseOpenAudibleDuration(%q) = %d,%v; want %d,%v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// TestOpenAudibleRuntimeRule pins THE runtime rule (openAudibleRuntime). The
// SAME cases are in site/src/lib/import-parse.test.ts ("the OpenAudible runtime
// rule"), because the /import preview states the rule too and the two must not
// disagree about which field wins.
func TestOpenAudibleRuntimeRule(t *testing.T) {
	for _, tc := range []struct {
		row  string
		want int
	}{
		{`{"seconds": 3630, "duration": "13:25"}`, 61}, // seconds wins when it is a runtime
		{`{"seconds": "3630"}`, 61},
		{`{"seconds": 10, "duration": "13:25"}`, 805}, // rounds to 0 minutes: not a runtime, so duration answers
		{`{"seconds": 0, "duration": "13:25"}`, 805},
		{`{"duration": "13:25"}`, 805},
		{`{"duration": "12:03:40"}`, 724},
		{`{"seconds": 10}`, 0},
		{`{"duration": "13:75"}`, 0},
		{`{}`, 0},
	} {
		entries, err := decodeEntries([]byte("["+tc.row+"]"), "t")
		if err != nil {
			t.Fatal(err)
		}
		if got := openAudibleRuntime(entries[0]); got != tc.want {
			t.Errorf("openAudibleRuntime(%s) = %d; want %d", tc.row, got, tc.want)
		}
	}
}

// TestOpenAudibleGenreLadderMapsThroughTheTable: the genre field is the book's
// category LADDER, and every level is claimed (as libex states every level), so
// the ladder resolves to the same set a node-carrying source would get. The
// paths are the point: "Contemporary", "Historical" and "Military" each mean
// something different under Romance than on their own, and only by_path can say
// so for a source that states names instead of nodes. The unmapped report names
// a ladder only when NO level of it mapped: the umbrella levels are unmapped on
// purpose and would otherwise be named on every run.
func TestOpenAudibleGenreLadderMapsThroughTheTable(t *testing.T) {
	table := audibleGenreTable()
	for _, tc := range []struct {
		ladder       string
		want         []string
		wantUnmapped []string
	}{
		{"Romance:Contemporary", []string{"contemporary-romance", "romance"}, nil},
		{"Romance: Contemporary ", []string{"contemporary-romance", "romance"}, nil},
		{"Teen & Young Adult:Romance:Contemporary", []string{"contemporary-romance", "romance", "young-adult"}, nil},
		// The leaf NAME "Historical" alone is historical-fiction; under Romance it
		// is historical romance.
		{"Romance:Historical", []string{"historical-romance", "romance"}, nil},
		// The leaf NAME "Military" alone is military history; under Science
		// Fiction it is military SF - and under Romance it is a romance.
		{"Science Fiction & Fantasy:Science Fiction:Military", []string{"military-science-fiction", "science-fiction"}, nil},
		{"Romance:Military", []string{"romance"}, nil},
		{"Science Fiction & Fantasy:Fantasy:Epic", []string{"epic-fantasy", "fantasy"}, nil},
		{"Science Fiction & Fantasy:Fantasy:Paranormal & Urban:Urban", []string{"fantasy", "urban-fantasy"}, nil},
		{"Literature & Fiction:Genre Fiction:Literary Fiction", []string{"literary-fiction"}, nil},
		// No level maps: the WHOLE ladder is reported, once.
		{"Literature & Fiction:Genre Fiction:Small Town & Rural", nil,
			[]string{"Literature & Fiction:Genre Fiction:Small Town & Rural"}},
		// Not an Audible category at all (seen on a sideloaded file): dropped and
		// reported, never stored.
		{"Audiobook", nil, []string{"Audiobook"}},
		{"", nil, nil},
		{" : : ", nil, nil},
	} {
		unmapped := map[string]bool{}
		got := table.mapGenres(pathGenreClaims(tc.ladder, "us"), unmapped)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("ladder %q -> %v; want %v", tc.ladder, got, tc.want)
		}
		var gotUnmapped []string
		for _, want := range tc.wantUnmapped {
			if !unmapped[want] {
				t.Errorf("ladder %q: unmapped report %v lacks %q", tc.ladder, unmapped, want)
			}
		}
		for k := range unmapped {
			gotUnmapped = append(gotUnmapped, k)
		}
		if len(gotUnmapped) != len(tc.wantUnmapped) {
			t.Errorf("ladder %q: unmapped report %v; want exactly %v", tc.ladder, gotUnmapped, tc.wantUnmapped)
		}
	}
}

// TestOpenAudibleImportStatesGenresAndDuration is the shape of a CURRENT
// OpenAudible export (issue #2337's library): no seconds field, a duration in
// hours and minutes, and the genre ladder. The created work carries the mapped
// genres and the recording its runtime - both of which that import lost.
func TestOpenAudibleImportStatesGenresAndDuration(t *testing.T) {
	sum, dataDir := runImport(t, `[{
	  "asin": "B0OAGENRE1",
	  "title": "Our Own Way",
	  "title_short": "Our Own Way",
	  "author": "Misty Vixen",
	  "narrated_by": "Jane Reader",
	  "language": "english",
	  "region": "US",
	  "duration": "09:17",
	  "genre": "Romance:Contemporary"
	}]`, false)
	if sum.NewWorks != 1 || sum.NewRecordings != 1 {
		t.Fatalf("summary = %+v, want one new work and recording", sum)
	}
	var work enrichedWork
	readEntity(t, dataDir, "works/ou/our-own-way/work.json", &work)
	if want := []string{"contemporary-romance", "romance"}; !reflect.DeepEqual(work.Genres, want) {
		t.Errorf("work genres = %v; want %v", work.Genres, want)
	}
	var rec recordingFile
	readEntity(t, dataDir, "works/ou/our-own-way/recordings/jane-reader.json", &rec)
	if rec.RuntimeMin != 557 {
		t.Errorf("runtime_min = %d; want 557 (from duration 09:17)", rec.RuntimeMin)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("imported tree failed validation:\n%v", res.Problems)
	}
}

// The trust-tier genre fixture: a mirror-only work whose libex set a user's
// one-ladder statement must ADD to, never replace, with a second mirror-only
// recording (its own ASIN) so two exact-ASIN rows can meet one work in one run.
const (
	tierMirrorGenres = `"genres":["fantasy","post-apocalyptic","romance","science-fiction","urban-fantasy"],`
	//nolint:lll // one record per line reads as the record it is
	tierMirrorWork = `{"added_at":"2026-01-05","authors":["ada-mapmaker"],` + tierMirrorGenres +
		`"id":"the-lost-cartographer","language":"en","license":"CC0-1.0","sources":[{"imported_at":"2026-01-05","ref":"B0LIBEX001","type":"libex-import"}],"title":"The Lost Cartographer"}`
	//nolint:lll // one record per line reads as the record it is
	tierRecording2 = `{"added_at":"2026-01-05","asin":[{"asin":"B0LIBEX002","region":"us"}],"id":"bea-reader-2021","language":"en","license":"CC0-1.0","narrators":["bea-reader"],"release_date":"2021","runtime_min":600,"sources":[{"imported_at":"2026-01-05","ref":"B0LIBEX002","type":"libex-import"}],"work":"the-lost-cartographer"}`
	tierRec2Rel    = "works/th/the-lost-cartographer/recordings/bea-reader-2021.json"
)

var tierMirrorGenreSet = []string{"fantasy", "post-apocalyptic", "romance", "science-fiction", "urban-fantasy"}

// tierGenreRow is one exact-ASIN OpenAudible row for the seeded book.
func tierGenreRow(asin, genre string) string {
	return `{"asin":"` + asin + `","title_short":"The Lost Cartographer","author":"Ada Mapmaker","narrated_by":"Bea Reader",` +
		`"language":"english","region":"us","genre":"` + genre + `"}`
}

// TestUserImportGenresAreAdditive is the trust-tier rule for the one fact a
// user export states PARTIALLY: an OpenAudible genre field is ONE ladder (the
// book's primary category) where the mirror states every ladder, so a user row's
// mapped genres are UNIONED into the work's set - never replacing or removing
// one, on a mirror-only work and on an already-attested one alike - and a row
// whose ladder maps to nothing changes nothing (silence is not an assertion).
func TestUserImportGenresAreAdditive(t *testing.T) {
	t.Run("a stated genre joins the mirror's set", func(t *testing.T) {
		dataDir := seedTierTree(t, map[string]string{tierWorkRel: tierMirrorWork})
		sum := runUserImport(t, dataDir, "["+tierGenreRow("B0LIBEX001", "Romance:Contemporary")+"]")
		if sum.AttestedWorks != 1 {
			t.Errorf("AttestedWorks = %d, want 1", sum.AttestedWorks)
		}
		var work enrichedWork
		readEntity(t, dataDir, tierWorkRel, &work)
		want := []string{"contemporary-romance", "fantasy", "post-apocalyptic", "romance", "science-fiction", "urban-fantasy"}
		if !reflect.DeepEqual(work.Genres, want) {
			t.Errorf("genres = %v; want the mirror's set plus the user's %v", work.Genres, want)
		}
		if res := check.Load(dataDir); !res.OK() {
			t.Fatalf("attested tree failed validation:\n%v", res.Problems)
		}
	})

	t.Run("an unmappable ladder keeps the mirror's set", func(t *testing.T) {
		dataDir := seedTierTree(t, map[string]string{tierWorkRel: tierMirrorWork})
		runUserImport(t, dataDir, "["+tierGenreRow("B0LIBEX001", "Audiobook")+"]")
		var work enrichedWork
		readEntity(t, dataDir, tierWorkRel, &work)
		if !reflect.DeepEqual(work.Genres, tierMirrorGenreSet) {
			t.Errorf("genres = %v; want the mirror's %v untouched", work.Genres, tierMirrorGenreSet)
		}
	})

	// An attested work keeps everything else it records (first writer wins),
	// but a later user's genres still JOIN its set: attestation is not what
	// decides genres, so it cannot be what blocks them.
	t.Run("a later user's genres join an attested work, and nothing else is written", func(t *testing.T) {
		dataDir := seedTierTree(t, map[string]string{
			tierWorkRel: tierWorkAttested,
			tierRecRel:  tierRecordingAttested,
		})
		recBefore := readRaw(t, dataDir, tierRecRel)
		sum := runUserImport(t, dataDir, "["+tierGenreRow("B0LIBEX001", "Romance:Contemporary")+"]")
		if sum.AttestedWorks != 0 || sum.GenreWorks != 1 {
			t.Errorf("AttestedWorks/GenreWorks = %d/%d, want 0/1", sum.AttestedWorks, sum.GenreWorks)
		}
		var work enrichedWork
		readEntity(t, dataDir, tierWorkRel, &work)
		if want := []string{"contemporary-romance", "romance"}; !reflect.DeepEqual(work.Genres, want) {
			t.Errorf("genres = %v; want %v", work.Genres, want)
		}
		if got := readRaw(t, dataDir, tierRecRel); got != recBefore {
			t.Errorf("the attested recording was rewritten:\n before %s\n after  %s", recBefore, got)
		}
	})

	// The ASIN-MERGE path (a regional re-release ASIN folded into the recording
	// by title + author + narrator) attests the RECORDING only, never the work:
	// the work was matched by name, not by identifier (attestOnMerge). So a
	// merged row's genre ladder does not reach the work - the shape most of
	// issue #2337's rows took (AU ASINs merging into US recordings). Pinned so a
	// change to that policy is a deliberate one.
	t.Run("an ASIN merge does not reach the work's genres", func(t *testing.T) {
		dataDir := seedTierTree(t, map[string]string{tierWorkRel: tierMirrorWork})
		merged := `[{"asin":"B0LIBEXAU1","title_short":"The Lost Cartographer","author":"Ada Mapmaker","narrated_by":"Bea Reader",` +
			`"language":"english","region":"au","genre":"Romance:Contemporary"}]`
		sum := runUserImport(t, dataDir, merged)
		if sum.MergedASINs != 1 || sum.AttestedWorks != 0 {
			t.Fatalf("MergedASINs/AttestedWorks = %d/%d, want 1/0", sum.MergedASINs, sum.AttestedWorks)
		}
		var work enrichedWork
		readEntity(t, dataDir, tierWorkRel, &work)
		if !reflect.DeepEqual(work.Genres, tierMirrorGenreSet) {
			t.Errorf("genres = %v; want the mirror's %v untouched by a merge", work.Genres, tierMirrorGenreSet)
		}
	})
}

// TestUserImportGenresDoNotDependOnRowOrder: two exact-ASIN rows of one book in
// one run - the first stating a ladder that maps to nothing (it still attests
// the work), the second a real one - must land the same catalogue in either
// order. Under the old overwrite rule the first row's attestation refused the
// second row's genres, so swapping two lines of an export changed the data.
func TestUserImportGenresDoNotDependOnRowOrder(t *testing.T) {
	rowA := tierGenreRow("B0LIBEX001", "Audiobook")
	rowB := tierGenreRow("B0LIBEX002", "Romance:Contemporary")
	rowC := tierGenreRow("B0LIBEX002", "Science Fiction & Fantasy:Fantasy:Epic")
	type outcome struct {
		genres  []string
		sources []string
		recs    [2]string
	}
	run := func(rows ...string) outcome {
		dataDir := seedTierTree(t, map[string]string{tierWorkRel: tierMirrorWork, tierRec2Rel: tierRecording2})
		books := "["
		for i, r := range rows {
			if i > 0 {
				books += ","
			}
			books += r
		}
		runUserImport(t, dataDir, books+"]")
		var work enrichedWork
		readEntity(t, dataDir, tierWorkRel, &work)
		var o outcome
		o.genres = work.Genres
		// Provenance entries are appended in row order; as a SET they must agree.
		for _, s := range work.Sources {
			b, _ := json.Marshal(s)
			o.sources = append(o.sources, string(b))
		}
		sort.Strings(o.sources)
		o.recs = [2]string{readRaw(t, dataDir, tierRecRel), readRaw(t, dataDir, tierRec2Rel)}
		if res := check.Load(dataDir); !res.OK() {
			t.Fatalf("tree failed validation:\n%v", res.Problems)
		}
		return o
	}
	ab, ba := run(rowA, rowB), run(rowB, rowA)
	if !reflect.DeepEqual(ab, ba) {
		t.Errorf("row order changed the catalogue:\n A,B: %+v\n B,A: %+v", ab, ba)
	}
	want := []string{"contemporary-romance", "fantasy", "post-apocalyptic", "romance", "science-fiction", "urban-fantasy"}
	if !reflect.DeepEqual(ab.genres, want) {
		t.Errorf("genres = %v; want %v", ab.genres, want)
	}
	// Two mapped ladders for one work in one run ACCRETE.
	if got := run(rowC, rowA, rowB).genres; !reflect.DeepEqual(got,
		[]string{"contemporary-romance", "epic-fantasy", "fantasy", "post-apocalyptic", "romance", "science-fiction", "urban-fantasy"}) {
		t.Errorf("genres = %v; want both user ladders unioned into the mirror's set", got)
	}
}

// TestCreatedWorkGenresAccreteWithinARun: a work THIS RUN created takes the
// genres every later row of the run states for it (a second ASIN of the same
// book), not only the first row's - the create path's twin of addRunCredits.
func TestCreatedWorkGenresAccreteWithinARun(t *testing.T) {
	_, dataDir := runImport(t, `[
	  {"asin":"B0OAACC001","title_short":"Our Own Way","author":"Misty Vixen","narrated_by":"Jane Reader",
	   "language":"english","region":"us","duration":"09:17","genre":"Romance:Contemporary"},
	  {"asin":"B0OAACC002","title_short":"Our Own Way","author":"Misty Vixen","narrated_by":"Jane Reader",
	   "language":"english","region":"uk","duration":"09:17","genre":"Science Fiction & Fantasy:Fantasy:Epic"}
	]`, false)
	var work enrichedWork
	readEntity(t, dataDir, "works/ou/our-own-way/work.json", &work)
	if want := []string{"contemporary-romance", "epic-fantasy", "fantasy", "romance"}; !reflect.DeepEqual(work.Genres, want) {
		t.Errorf("genres = %v; want both rows' ladders %v", work.Genres, want)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("tree failed validation:\n%v", res.Problems)
	}
}
