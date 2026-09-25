package importer

import (
	"reflect"
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

// TestOpenAudibleGenreLadderMapsThroughTheTable: the genre field is the book's
// category LADDER, and every level is claimed (as libex states every level), so
// the ladder resolves to the same set a node-carrying source would get. The
// paths are the point: "Contemporary", "Historical" and "Military" each mean
// something different under Romance than on their own, and only by_path can say
// so for a source that states names instead of nodes.
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
		{"Science Fiction & Fantasy:Science Fiction:Military", []string{"military-science-fiction", "science-fiction"},
			[]string{"Science Fiction & Fantasy"}},
		{"Romance:Military", []string{"romance"}, nil},
		{"Science Fiction & Fantasy:Fantasy:Epic", []string{"epic-fantasy", "fantasy"}, []string{"Science Fiction & Fantasy"}},
		{"Science Fiction & Fantasy:Fantasy:Paranormal & Urban:Urban", []string{"fantasy", "urban-fantasy"}, []string{"Science Fiction & Fantasy"}},
		{"Literature & Fiction:Genre Fiction:Literary Fiction", []string{"literary-fiction"},
			[]string{"Literature & Fiction", "Literature & Fiction:Genre Fiction"}},
		// Not an Audible category at all (seen on a sideloaded file): dropped and
		// reported, never stored.
		{"Audiobook", nil, []string{"Audiobook"}},
		{"", nil, nil},
		{" : : ", nil, nil},
	} {
		unmapped := map[string]bool{}
		got := table.mapGenres(pathGenreClaims(tc.ladder), unmapped)
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

// TestUserImportGenreReplacesMirrorGenres is the trust-tier rule applied to the
// field OpenAudible now states: a user-library row matching a
// bulk-mirror-only work by ASIN OVERWRITES its genres with the set the export
// states (a set, wholesale - never unioned with the mirror's), while a row whose
// ladder maps to nothing leaves the mirror's set alone (silence is not an
// assertion), and an already-attested work is not rewritten at all.
func TestUserImportGenreReplacesMirrorGenres(t *testing.T) {
	const mirrorGenres = `"genres":["fantasy","post-apocalyptic","romance","science-fiction","urban-fantasy"],`
	mirrorWork := `{"added_at":"2026-01-05","authors":["ada-mapmaker"],` + mirrorGenres +
		`"id":"the-lost-cartographer","language":"en","license":"CC0-1.0","sources":[{"imported_at":"2026-01-05","ref":"B0LIBEX001","type":"libex-import"}],"title":"The Lost Cartographer"}`
	row := func(genre string) string {
		return `[{"asin":"B0LIBEX001","title_short":"The Lost Cartographer","author":"Ada Mapmaker","narrated_by":"Bea Reader",` +
			`"language":"english","region":"us","genre":"` + genre + `"}]`
	}

	t.Run("stated genre replaces the mirror's set", func(t *testing.T) {
		dataDir := seedTierTree(t, map[string]string{tierWorkRel: mirrorWork})
		sum := runUserImport(t, dataDir, row("Romance:Contemporary"))
		if sum.AttestedWorks != 1 {
			t.Errorf("AttestedWorks = %d, want 1", sum.AttestedWorks)
		}
		var work enrichedWork
		readEntity(t, dataDir, tierWorkRel, &work)
		if want := []string{"contemporary-romance", "romance"}; !reflect.DeepEqual(work.Genres, want) {
			t.Errorf("genres = %v; want the user's %v", work.Genres, want)
		}
		if res := check.Load(dataDir); !res.OK() {
			t.Fatalf("attested tree failed validation:\n%v", res.Problems)
		}
	})

	t.Run("an unmappable ladder keeps the mirror's set", func(t *testing.T) {
		dataDir := seedTierTree(t, map[string]string{tierWorkRel: mirrorWork})
		runUserImport(t, dataDir, row("Audiobook"))
		var work enrichedWork
		readEntity(t, dataDir, tierWorkRel, &work)
		if want := []string{"fantasy", "post-apocalyptic", "romance", "science-fiction", "urban-fantasy"}; !reflect.DeepEqual(work.Genres, want) {
			t.Errorf("genres = %v; want the mirror's %v untouched", work.Genres, want)
		}
	})

	t.Run("an attested work is not rewritten", func(t *testing.T) {
		dataDir := seedTierTree(t, map[string]string{
			tierWorkRel: tierWorkAttested,
			tierRecRel:  tierRecordingAttested,
		})
		before := snapshotTree(t, dataDir)
		runUserImport(t, dataDir, row("Romance:Contemporary"))
		assertTreeUnchanged(t, dataDir, before)
	})

	// The ASIN-MERGE path (a regional re-release ASIN folded into the recording
	// by title + author + narrator) attests the RECORDING only, never the work:
	// the work was matched by name, not by identifier (attestOnMerge). So a
	// merged row's genre ladder does not touch the work's genres - which is the
	// shape most of issue #2337's rows took (AU ASINs merging into US
	// recordings). Pinned so a change to that policy is a deliberate one.
	t.Run("an ASIN merge does not rewrite the work's genres", func(t *testing.T) {
		dataDir := seedTierTree(t, map[string]string{tierWorkRel: mirrorWork})
		merged := `[{"asin":"B0LIBEXAU1","title_short":"The Lost Cartographer","author":"Ada Mapmaker","narrated_by":"Bea Reader",` +
			`"language":"english","region":"au","genre":"Romance:Contemporary"}]`
		sum := runUserImport(t, dataDir, merged)
		if sum.MergedASINs != 1 || sum.AttestedWorks != 0 {
			t.Fatalf("MergedASINs/AttestedWorks = %d/%d, want 1/0", sum.MergedASINs, sum.AttestedWorks)
		}
		var work enrichedWork
		readEntity(t, dataDir, tierWorkRel, &work)
		if want := []string{"fantasy", "post-apocalyptic", "romance", "science-fiction", "urban-fantasy"}; !reflect.DeepEqual(work.Genres, want) {
			t.Errorf("genres = %v; want the mirror's %v untouched by a merge", work.Genres, want)
		}
	})
}
