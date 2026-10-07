package importer

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/check"
)

func TestVoteGenres(t *testing.T) {
	cases := []struct {
		name   string
		recs   [][]string
		want   []string
		stated bool
	}{
		{"no recordings", nil, nil, false},
		{"one recording is its own set", [][]string{{"mystery", "westerns"}}, []string{"mystery", "westerns"}, true},
		{"two recordings are a union", [][]string{{"mystery"}, {"westerns"}}, []string{"mystery", "westerns"}, true},
		{
			"three recordings keep what two state",
			[][]string{{"mystery", "westerns"}, {"mystery", "thriller-suspense"}, {"mystery", "thriller-suspense"}},
			[]string{"mystery", "thriller-suspense"}, true,
		},
		{
			"a genre-less recording does not vote, so two bearers stay a union",
			[][]string{{"mystery"}, {"westerns"}, nil, {}},
			[]string{"mystery", "westerns"}, true,
		},
		{
			"a recording stating a genre twice is still one vote",
			[][]string{{"westerns", "westerns", "mystery"}, {"mystery"}, {"mystery"}},
			[]string{"mystery"}, true,
		},
		{
			"three recordings agreeing on nothing state nothing, and hand back the union",
			[][]string{{"b"}, {"a"}, {"c"}}, []string{"a", "b", "c"}, false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, stated := VoteGenres(tc.recs)
			if !reflect.DeepEqual(got, tc.want) || stated != tc.stated {
				t.Errorf("VoteGenres(%v) = %v, %v; want %v, %v", tc.recs, got, stated, tc.want, tc.stated)
			}
		})
	}
}

// libexVoteRow is one libex row of the book "Five Little Pigs" by Agatha
// Christie, read by narrator, with the named genre claims.
func libexVoteRow(asin, region, narrator string, minutes int, genres ...string) string {
	var gs []string
	for _, g := range genres {
		gs = append(gs, fmt.Sprintf(`{"name":%q}`, g))
	}
	return fmt.Sprintf(`{"asin":%q,"title":"Five Little Pigs","region":%q,"language":"english",`+
		`"authors":[{"name":"Agatha Christie"}],"narrators":[{"name":%q}],"lengthMinutes":%d,"genres":[%s]}`,
		asin, region, narrator, minutes, strings.Join(gs, ","))
}

// voteRows are four rows of one work over three recordings: the Moffatt
// production in two marketplaces (ONE vote, however many rows), a full cast and
// Hugh Fraser. Only the Moffatt production states Westerns.
var voteRows = []string{
	libexVoteRow("B0VOTE0001", "us", "John Moffatt", 120, "Mystery", "Westerns"),
	libexVoteRow("B0VOTE0002", "uk", "John Moffatt", 120, "Mystery", "Westerns"),
	libexVoteRow("B0VOTE0003", "us", "Full Cast", 90, "Mystery", "Thriller & Suspense"),
	libexVoteRow("B0VOTE0004", "us", "Hugh Fraser", 420, "Mystery", "Thriller & Suspense"),
}

func readVoteWork(t *testing.T, dataDir string) enrichedWork {
	t.Helper()
	var work enrichedWork
	readEntity(t, dataDir, "works/fi/five-little-pigs/work.json", &work)
	return work
}

// TestLibexCreateVotesGenresAcrossRecordings is the create path's half of the
// recording vote: a work a libex run creates gets the vote over its recordings,
// so the one production that states Westerns - in two marketplaces, which is
// still one vote - does not reach the work, and the outcome does not depend on
// the order the rows arrive in.
func TestLibexCreateVotesGenresAcrossRecordings(t *testing.T) {
	want := []string{"mystery", "thriller-suspense"}
	for _, order := range [][]string{voteRows, reversed(voteRows)} {
		dataDir := t.TempDir()
		sum := runLibexWith(t, dataDir, Options{}, order...)
		if sum.NewWorks != 1 || sum.NewRecordings != 3 || sum.MergedASINs != 1 {
			t.Fatalf("summary = %+v, want one work over three recordings and one merged regional ASIN", sum)
		}
		if got := readVoteWork(t, dataDir).Genres; !reflect.DeepEqual(got, want) {
			t.Errorf("genres = %v, want the vote %v", got, want)
		}
		if res := check.Load(dataDir); !res.OK() {
			t.Fatalf("tree failed validation: %v", res.Problems)
		}
	}
}

// TestLibexCreateUnionsBelowTheQuorum: two genre-bearing recordings cannot
// outvote each other, and a third recording whose rows map nothing is not a
// voter, so the union stands.
func TestLibexCreateUnionsBelowTheQuorum(t *testing.T) {
	dataDir := t.TempDir()
	runLibexWith(t, dataDir, Options{},
		libexVoteRow("B0VOTE0001", "us", "John Moffatt", 120, "Westerns"),
		libexVoteRow("B0VOTE0003", "us", "Full Cast", 90, "Mystery"),
		libexVoteRow("B0VOTE0004", "us", "Hugh Fraser", 420, "Nothing That Maps"),
	)
	if got, want := readVoteWork(t, dataDir).Genres, []string{"mystery", "westerns"}; !reflect.DeepEqual(got, want) {
		t.Errorf("genres = %v, want the union %v", got, want)
	}
}

// TestUserImportKeepsTheUnion: the vote is the MIRROR's rule. A user-library
// create run keeps LICENSING.md's rule 5 - its genres are added, never trimmed -
// so three recordings that agree on nothing keep everything they state.
func TestUserImportKeepsTheUnion(t *testing.T) {
	_, dataDir := runImport(t, `[
	  {"asin":"B0OAVOT001","title_short":"Our Own Way","author":"Misty Vixen","narrated_by":"Jane Reader",
	   "language":"english","region":"us","duration":"09:17","genre":"Romance:Contemporary"},
	  {"asin":"B0OAVOT002","title_short":"Our Own Way","author":"Misty Vixen","narrated_by":"Ann Other",
	   "language":"english","region":"us","duration":"09:17","genre":"Science Fiction & Fantasy:Fantasy:Epic"},
	  {"asin":"B0OAVOT003","title_short":"Our Own Way","author":"Misty Vixen","narrated_by":"Third Voice",
	   "language":"english","region":"us","duration":"09:17","genre":"Mystery, Thriller & Suspense:Mystery"}
	]`, false)
	var work enrichedWork
	readEntity(t, dataDir, "works/ou/our-own-way/work.json", &work)
	for _, g := range []string{"contemporary-romance", "epic-fantasy", "mystery"} {
		if !slices.Contains(work.Genres, g) {
			t.Errorf("genres = %v, want every row's genres (the union), including %s", work.Genres, g)
		}
	}
}

// TestEnrichFillVotesGenres is enrichment's half of the recording vote: a
// genre-less work it fills gets the vote over the recordings its rows matched
// (a recording's regional ASINs one vote), two recordings keep their union, and
// a work that carried a set at load is never touched - in either row order.
func TestEnrichFillVotesGenres(t *testing.T) {
	rows := []string{
		regenRow("B0VOTEA001", "Mystery", "Westerns"),
		regenRow("B0VOTEA002", "Westerns"), // recording a's second marketplace: still one vote
		regenRow("B0VOTEB001", "Mystery", "Thriller & Suspense"),
		regenRow("B0VOTEC001", "Mystery", "Thriller & Suspense"),
		regenRow("B0PAIRA001", "Mystery"),
		regenRow("B0PAIRB001", "Westerns"),
		regenRow("B0KEPT0001", "Mystery"),
		regenRow("B0KEPT0002", "Fantasy"),
		regenRow("B0KEPT0003", "Fantasy"),
	}
	for _, order := range [][]string{rows, reversed(rows)} {
		dataDir := seedRegen(t, map[string]string{
			"works/tr/trio/work.json":         regenWorkJSON("trio", nil, "libex-import"),
			"works/tr/trio/recordings/a.json": regenRecJSON("trio", "a", "B0VOTEA001", "B0VOTEA002"),
			"works/tr/trio/recordings/b.json": regenRecJSON("trio", "b", "B0VOTEB001"),
			"works/tr/trio/recordings/c.json": regenRecJSON("trio", "c", "B0VOTEC001"),
			"works/pa/pair/work.json":         regenWorkJSON("pair", nil, "libex-import"),
			"works/pa/pair/recordings/a.json": regenRecJSON("pair", "a", "B0PAIRA001"),
			"works/pa/pair/recordings/b.json": regenRecJSON("pair", "b", "B0PAIRB001"),
			"works/ke/kept/work.json":         regenWorkJSON("kept", []string{"romance"}, "libex-import"),
			"works/ke/kept/recordings/a.json": regenRecJSON("kept", "a", "B0KEPT0001"),
			"works/ke/kept/recordings/b.json": regenRecJSON("kept", "b", "B0KEPT0002"),
			"works/ke/kept/recordings/c.json": regenRecJSON("kept", "c", "B0KEPT0003"),
		})
		kept := readRaw(t, dataDir, "works/ke/kept/work.json")
		runLibexWith(t, dataDir, Options{Mode: ModeEnrich}, order...)
		for slug, want := range map[string][]string{
			"trio": {"mystery", "thriller-suspense"},
			"pair": {"mystery", "westerns"},
		} {
			if got := regenGenres(t, dataDir, slug); !reflect.DeepEqual(got, want) {
				t.Errorf("%s genres = %v, want %v", slug, got, want)
			}
		}
		if got := readRaw(t, dataDir, "works/ke/kept/work.json"); got != kept {
			t.Errorf("a work with a recorded genre set was rewritten:\n%s\nwant\n%s", got, kept)
		}
		var trio enrichedWork
		readEntity(t, dataDir, "works/tr/trio/work.json", &trio)
		if len(trio.Sources) < 2 {
			t.Errorf("trio sources = %+v, want the enrichment's provenance stamped", trio.Sources)
		}
		if res := check.Load(dataDir); !res.OK() {
			t.Fatalf("tree failed validation: %v", res.Problems)
		}
	}
}

// TestAnUnlandedRowStillVotes: a row that lands on no recording - the shape the
// row creating a work would have if addRecording wrote nothing - casts a vote of
// its own, so the next row's vote does not silently replace the genres it
// seeded.
func TestAnUnlandedRowStillVotes(t *testing.T) {
	p := &planner{}
	ws := &workState{runGenresOwned: true, runGenres: []string{"mystery"}, runRecGenres: map[string][]string{}}
	if got := p.accrueRunGenres(ws, "", []string{"mystery"}); got != nil {
		t.Errorf("the creating row's own vote changed the set to %v", got)
	}
	got := p.accrueRunGenres(ws, "bea-reader-2024", []string{"fantasy"})
	if want := []string{"fantasy", "mystery"}; !reflect.DeepEqual(got, want) {
		t.Errorf("after a landed row = %v, want %v (two voters: the union)", got, want)
	}
}

// runParsedLibex parses libex rows as RunLibex does, lets edit change what the
// parse layer would have refused (an unknown region, no ASIN - shapes a libex
// row never reaches the planner with, but the create path's vote must still
// handle), and runs the create path over them.
func runParsedLibex(t *testing.T, dataDir string, edit func([]sourceBook), rows ...string) {
	t.Helper()
	entries, err := decodeLibexEntries([]byte(strings.Join(rows, "\n") + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	books := parseLibexEntries(entries).books
	edit(books)
	if _, err := runBooks(books, sourceLibex, Options{DataDir: dataDir, ImportDate: testImportDate}, nil); err != nil {
		t.Fatalf("import run: %v", err)
	}
}

// TestAMatchedRowVotesWithItsProduction: a row matched to ONE existing production
// whose ASIN cannot be recorded (here an unknown region) is that production's
// tagging, so its genres join that recording's vote - the stray Westerns the
// Moffatt production states twice is still one vote and loses.
func TestAMatchedRowVotesWithItsProduction(t *testing.T) {
	dataDir := t.TempDir()
	runParsedLibex(t, dataDir, func(books []sourceBook) {
		books[1].raw["region"] = "zz"
	},
		libexVoteRow("B0VOTE0001", "us", "John Moffatt", 120, "Mystery", "Westerns"),
		libexVoteRow("B0VOTE0002", "us", "John Moffatt", 120, "Westerns"),
		libexVoteRow("B0VOTE0003", "us", "Full Cast", 90, "Mystery", "Thriller & Suspense"),
		libexVoteRow("B0VOTE0004", "us", "Hugh Fraser", 420, "Mystery", "Thriller & Suspense"),
	)
	if got, want := readVoteWork(t, dataDir).Genres, []string{"mystery", "thriller-suspense"}; !reflect.DeepEqual(got, want) {
		t.Errorf("genres = %v, want %v (the unrecorded regional row joins its production's vote)", got, want)
	}
}

// TestARowMatchingSeveralProductionsVotesAlone: a row with no ASIN that matches
// SEVERAL same-narrator productions cannot say which one it is, so it casts a
// vote of its own - here the third voter, under which its Westerns loses where
// two voters would have kept the union.
func TestARowMatchingSeveralProductionsVotesAlone(t *testing.T) {
	dataDir := t.TempDir()
	runParsedLibex(t, dataDir, func(books []sourceBook) {
		books[2].raw["asin"] = ""
	},
		libexVoteRow("B0VOTE0001", "us", "John Moffatt", 120, "Mystery"),
		libexVoteRow("B0VOTE0002", "us", "John Moffatt", 420, "Mystery"), // another runtime: a second production
		libexVoteRow("B0VOTE0003", "us", "John Moffatt", 120, "Westerns"),
	)
	if got, want := readVoteWork(t, dataDir).Genres, []string{"mystery"}; !reflect.DeepEqual(got, want) {
		t.Errorf("genres = %v, want %v (the unmatched row is a third voter)", got, want)
	}
}
