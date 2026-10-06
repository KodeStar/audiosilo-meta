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
