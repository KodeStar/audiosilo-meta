package importer

// The series grouping is transitive and a review is pairwise, so a spelling no
// review names can stand for both people of a reviewed pair: with a review of
// (alex-george, dr-alex-george), "A. George" is a spelling of each. The review is
// therefore a NEVER-JOIN constraint inside the grouping (personGroups), and a row
// reaches a group only if nobody in it is someone the review keeps apart from the
// row (seriesAuthors.sharedWith). The issue-form gates ask the same index.

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/check"
)

var (
	georgeReview = NewPeopleApart(peopleApart("alex-george", "dr-alex-george"), nil)
	otherReview  = NewPeopleApart(peopleApart("adam-brown", "mr-adam-brown"), nil)
)

// georgeForms are the three spellings under ap: the novelist, the doctor, and the
// initial that spells either.
func georgeForms(ap *PeopleApart) (novelist, doctor, initial personForm) {
	return ap.form("alex-george", "Alex George"), ap.form("dr-alex-george", "Dr. Alex George"), ap.form("a-george", "A. George")
}

// seriesOf is series evidence of one member per form, in order, grouped.
func seriesOf(forms ...personForm) *seriesAuthors {
	sa := &seriesAuthors{}
	for i, f := range forms {
		sa.add(fmt.Sprint("work-", i), []personForm{f}, nil)
	}
	sa.refresh()
	return sa
}

func TestSeriesGroupsNeverJoinAReviewedPair(t *testing.T) {
	novelist, doctor, initial := georgeForms(georgeReview)
	if !initial.same(novelist) || !initial.same(doctor) || novelist.same(doctor) {
		t.Fatalf("fixture: initial~novelist %v, initial~doctor %v, novelist~doctor %v",
			initial.same(novelist), initial.same(doctor), novelist.same(doctor))
	}
	for name, forms := range map[string][]personForm{
		"the initial last":    {novelist, doctor, initial},
		"the initial first":   {initial, doctor, novelist},
		"the initial between": {doctor, initial, novelist},
	} {
		t.Run(name, func(t *testing.T) {
			// A spelling of both is a person of its own, whatever the order.
			sa := seriesOf(forms...)
			if len(sa.credits) != 3 || sa.root("a-george") != "a-george" {
				t.Errorf("%d people, the initial in %s: want 3, the initial alone", len(sa.credits), sa.root("a-george"))
			}
			// A clone (what admits judges) keeps the policy.
			c := sa.clone()
			c.add("z", []personForm{doctor}, nil)
			c.refresh()
			if c.root("alex-george") == c.root("dr-alex-george") {
				t.Error("the clone joined the pair")
			}
		})
	}
	t.Run("with one of the pair it is his", func(t *testing.T) {
		for _, person := range []personForm{novelist, doctor} {
			if sa := seriesOf(person, initial); len(sa.credits) != 1 {
				t.Errorf("%s and the initial are %d people, want 1", person.slug, len(sa.credits))
			}
		}
	})
	t.Run("a chain is judged whole", func(t *testing.T) {
		// Alex George - Alex J. George - A.J. George - A.J. Georges: each only a
		// spelling of its neighbours, so nobody reaches both ends and the union has
		// to refuse by what the groups already hold.
		ap := NewPeopleApart(peopleApart("alex-george", "a-j-georges"), nil)
		a, x := ap.form("alex-george", "Alex George"), ap.form("alex-j-george", "Alex J. George")
		y, b := ap.form("a-j-george", "A.J. George"), ap.form("a-j-georges", "A.J. Georges")
		if !a.same(x) || !x.same(y) || !y.same(b) || a.same(y) || x.same(b) {
			t.Fatalf("fixture: a~x %v, x~y %v, y~b %v, a~y %v, x~b %v", a.same(x), x.same(y), y.same(b), a.same(y), x.same(b))
		}
		sa := seriesOf(a, x, y, b)
		if sa.root("a-j-georges") == sa.root("alex-george") {
			t.Error("the chain joined the reviewed pair")
		}
		if sa.root("alex-j-george") != "alex-george" || sa.root("a-j-george") != "alex-george" {
			t.Errorf("the chain's spellings are in %s and %s", sa.root("alex-j-george"), sa.root("a-j-george"))
		}
	})
	t.Run("no review of the pair", func(t *testing.T) {
		// The grouping is unchanged for everyone else: all three are one person.
		for _, ap := range []*PeopleApart{nil, otherReview} {
			n, d, i := georgeForms(ap)
			if sa := seriesOf(n, d, i); len(sa.credits) != 1 {
				t.Errorf("%d people, want 1", len(sa.credits))
			}
		}
	})
}

func TestSeriesFitAsksTheWholeGroup(t *testing.T) {
	novelist, doctor, initial := georgeForms(georgeReview)
	rowOf := func(forms ...personForm) *SeriesRow {
		return &SeriesRow{apart: georgeReview, prepared: true, forms: forms}
	}
	for name, tc := range map[string]struct {
		sa   *seriesAuthors
		row  *SeriesRow
		want seriesFit
	}{
		// The denied half: "A. George" in the novelist's series is no way in for
		// the doctor, in the doctor's for the novelist, nor for a cluster that
		// holds the novelist beside the initial. Nor for a row built without the
		// review: the group's own review keeps the veto.
		"doctor, novelist's series": {seriesOf(novelist, novelist, initial), rowOf(doctor), seriesClosed},
		"novelist, doctor's series": {seriesOf(doctor, initial, initial), rowOf(novelist), seriesClosed},
		"cluster, doctor's series":  {seriesOf(doctor, doctor), rowOf(initial, novelist), seriesClosed},
		"row without the review":    {seriesOf(novelist, novelist, initial), &SeriesRow{prepared: true, forms: []personForm{formOf(doctor.slug, "Dr. Alex George")}}, seriesClosed},
		// The allowed half: the review says nothing about a group holding neither,
		// the initial itself is still the novelist, and the novelist still meets
		// his own series.
		"doctor, the initial alone":  {seriesOf(initial, initial), rowOf(doctor), seriesShared},
		"initial, novelist's series": {seriesOf(novelist, novelist), rowOf(initial), seriesShared},
		"novelist, own series":       {seriesOf(novelist, initial), rowOf(novelist), seriesShared},
	} {
		t.Run(name, func(t *testing.T) {
			if got := tc.sa.fit(tc.row, nil); got != tc.want {
				t.Errorf("fit = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAuthorClustersNeverJoinAReviewedPair(t *testing.T) {
	x := seriesPerson{slug: "xan-writer", name: "Xan Writer"}
	novelist := seriesPerson{slug: "alex-george", name: "Alex George"}
	doctor := seriesPerson{slug: "dr-alex-george", name: "Dr. Alex George"}
	initial := seriesPerson{slug: "a-george", name: "A. George"}
	clusters := func(ap *PeopleApart, authors ...[]seriesPerson) [][]int {
		claims := make([]nameClaim, len(authors))
		idx := make([]int, len(authors))
		for i, a := range authors {
			claims[i] = nameClaim{row: &SeriesRow{authors: a, apart: ap}, order: fmt.Sprintf("%03d", i)}
			idx[i] = i
		}
		return authorClusters(claims, idx, idx)
	}
	for name, tc := range map[string]struct {
		ap      *PeopleApart
		authors [][]seriesPerson
		want    [][]int
	}{
		// A spelling of both is grouped with neither, whatever the order, and
		// its own repeated credits stay together.
		"a third spelling, reviewed":  {georgeReview, [][]seriesPerson{{initial}, {novelist}, {doctor}}, [][]int{{0}, {1}, {2}}},
		"a third spelling, row order": {georgeReview, [][]seriesPerson{{doctor}, {novelist}, {initial}}, [][]int{{0}, {1}, {2}}},
		"a third spelling, repeated":  {georgeReview, [][]seriesPerson{{initial}, {initial}, {novelist}, {doctor}}, [][]int{{0, 1}, {2}, {3}}},
		// Only when both people are in the claims: with one of them it is his.
		"a third spelling, one person": {georgeReview, [][]seriesPerson{{initial}, {novelist}}, [][]int{{0, 1}}},
		"the doctor alone":             {georgeReview, [][]seriesPerson{{initial}, {doctor}}, [][]int{{0, 1}}},
		// A chain of spellings: "Alex J. George" and the initial each bridge the pair.
		"two bridges":                 {georgeReview, [][]seriesPerson{{initial}, {{slug: "alex-j-george", name: "Alex J. George"}}, {novelist}, {doctor}}, [][]int{{0}, {1}, {2}, {3}}},
		"a third spelling, no review": {nil, [][]seriesPerson{{initial}, {novelist}, {doctor}}, [][]int{{0, 1, 2}}},
		"another pair reviewed":       {otherReview, [][]seriesPerson{{initial}, {novelist}, {doctor}}, [][]int{{0, 1, 2}}},
		"a co-author of both":         {georgeReview, [][]seriesPerson{{novelist, x}, {doctor, x}, {x}}, [][]int{{0, 2}, {1}}},
		"a co-author, no review":      {nil, [][]seriesPerson{{novelist, x}, {doctor, x}, {x}}, [][]int{{0, 1, 2}}},
		"one row crediting both":      {georgeReview, [][]seriesPerson{{novelist, doctor}, {novelist}}, [][]int{{0}, {1}}},
	} {
		t.Run(name, func(t *testing.T) {
			if got := clusters(tc.ap, tc.authors...); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("clusters = %v, want %v", got, tc.want)
			}
		})
	}
}

// mindMattersWith is a catalogued `mind-matters` holding works (slug -> author
// slug), in slug order, with the person records people (slug -> name).
func mindMattersWith(t *testing.T, works map[string]string, people map[string]string) string {
	t.Helper()
	files := map[string]string{personAddr("nate-narrator"): testpack.PersonJSON(t, "nate-narrator", "Nate Narrator")}
	var members []string
	for i, w := range slices.Sorted(maps.Keys(works)) {
		files[workAddr(w)] = testpack.WorkJSON(t, w, w, testpack.WithAuthors(works[w]))
		files[recAddr(w, "r1")] = testpack.RecJSON(t, "r1", w, testpack.WithNarrators("nate-narrator"))
		members = append(members, w+"@"+string(rune('1'+i)))
	}
	for slug, name := range people {
		files[personAddr(slug)] = testpack.PersonJSON(t, slug, name)
	}
	files[seriesAddr("mind-matters")] = testpack.SeriesJSON(t, "mind-matters", "Mind Matters", members...)
	return seedTombstoneTree(t, files, nil)
}

// The bridge end to end: the novelist's series credits one volume to "A. George",
// which is a spelling of the doctor's name too, and the doctor's row still does
// not join - while the initial's and the novelist's rows do. And a series of the
// initial alone holds nobody a review keeps from the doctor, so his row joins it.
func TestThirdSpellingDoesNotBridgeAReviewedPair(t *testing.T) {
	seed := map[string]string{"alex-george": "Alex George", "a-george": "A. George"}
	bridged := map[string]string{"a-good-american": "alex-george", "the-paris-hours": "alex-george", "the-third-hour": "a-george"}
	for name, tc := range map[string]struct {
		works      map[string]string
		people     map[string]string
		distinct   []check.ReviewedDistinct
		row        string // the credit on The Mind Manual, volume 4
		wantAuthor string
		wantJoined bool
	}{
		"doctor, reviewed":     {bridged, seed, peopleApart("alex-george", "dr-alex-george"), "Dr. Alex George", "dr-alex-george", false},
		"initial, reviewed":    {bridged, seed, peopleApart("alex-george", "dr-alex-george"), "A. George", "a-george", true},
		"novelist, reviewed":   {bridged, seed, peopleApart("alex-george", "dr-alex-george"), "Alex George", "alex-george", true},
		"doctor, other review": {bridged, seed, peopleApart("adam-brown", "mr-adam-brown"), "Dr. Alex George", "alex-george", true},
		"doctor, the initial alone": {
			map[string]string{"the-third-hour": "a-george", "the-fourth-hour": "a-george"},
			map[string]string{"a-george": "A. George"},
			peopleApart("alex-george", "dr-alex-george"), "Dr. Alex George", "dr-alex-george", true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			dataDir := mindMattersWith(t, tc.works, tc.people)
			sum := runLibexWith(t, dataDir, Options{Distinct: tc.distinct},
				tombRow("B0MINDMAN4", "The Mind Manual", tc.row, "Nate Narrator", 300, "Mind Matters", "4"))
			if got := workAuthorsOf(t, dataDir, "the-mind-manual"); !reflect.DeepEqual(got, []string{tc.wantAuthor}) {
				t.Fatalf("authors = %v, want [%s]", got, tc.wantAuthor)
			}
			_, joined := seriesWorks(t, dataDir, "mind-matters")["the-mind-manual"]
			if joined != tc.wantJoined {
				t.Errorf("joined mind-matters = %v, want %v\nwarnings: %v", joined, tc.wantJoined, sum.Warnings)
			}
			mustValidate(t, dataDir)
		})
	}
}

// The same bridge inside one batch, with no catalogued series to judge against: the
// largest cluster founds the series, the initial joins it and the other of the pair
// founds his own, rather than the initial carrying all three into one. The evidence
// decides whose the initial is, not the slug order that puts `dr-` first.
func TestBatchClusterDoesNotBridgeAReviewedPair(t *testing.T) {
	rowsOf := func(credits ...string) []string {
		var rows []string
		for i, c := range credits {
			n := []string{"One", "Two", "Three", "Four"}[i]
			rows = append(rows, tombRow(fmt.Sprint("B0CLUSTER", i), "Volume "+n, c, "Nate Narrator", 300, "New Saga", fmt.Sprint(i+1)))
		}
		return rows
	}
	review := peopleApart("alex-george", "dr-alex-george")
	for name, tc := range map[string]struct {
		distinct []check.ReviewedDistinct
		rows     []string
		want     map[string][]string
	}{
		"reviewed pair": {review, rowsOf("A. George", "Alex George", "Alex George", "Dr. Alex George"), map[string][]string{
			"new-saga": {"volume-one", "volume-three", "volume-two"}, "new-saga-2": {"volume-four"},
		}},
		"the evidence is the doctor's": {review, rowsOf("Dr. Alex George", "Dr. Alex George", "A. George", "Alex George"), map[string][]string{
			"new-saga": {"volume-one", "volume-three", "volume-two"}, "new-saga-2": {"volume-four"},
		}},
		"other pair": {peopleApart("adam-brown", "mr-adam-brown"), rowsOf("A. George", "Alex George", "Alex George", "Dr. Alex George"), map[string][]string{
			"new-saga": {"volume-four", "volume-one", "volume-three", "volume-two"},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			dataDir := seedTombstoneTree(t, map[string]string{personAddr("nate-narrator"): testpack.PersonJSON(t, "nate-narrator", "Nate Narrator")}, nil)
			runLibexWith(t, dataDir, Options{Distinct: tc.distinct}, tc.rows...)
			for slug, want := range tc.want {
				if got := slices.Sorted(maps.Keys(seriesWorks(t, dataDir, slug))); !reflect.DeepEqual(got, want) {
					t.Errorf("%s = %v, want %v", slug, got, want)
				}
			}
			mustValidate(t, dataDir)
		})
	}
}

// The pre-pass judges a landing before the batch's initials decision and the
// import after it. No spelling of "Dr. AB Kovacs" is committed, so the pre-pass
// judges its own `dr-ab-kovacs` against the bare name's committed `a-b-kovacs` -
// the pair the review names - and refuses the fold. The decision then moves it
// onto the batch's majority `dr-a-b-kovacs`, which the review does not name. The
// import must keep the refusal rather than fold the credit onto the record the
// review kept it from.
func TestImportKeepsAFoldThePrePassRefused(t *testing.T) {
	for name, tc := range reviewedCases([]string{"a-b-kovacs", "dr-ab-kovacs"}, "a-b-kovacs", "dr-a-b-kovacs") {
		t.Run(name, func(t *testing.T) {
			// a-b-kovacs only narrates, so "Dr. A.B. Kovacs" has no bare twin on the
			// author side and stays titled: the batch's other spelling of the group.
			dataDir := seedTombstoneTree(t, map[string]string{
				personAddr("a-b-kovacs"):   testpack.PersonJSON(t, "a-b-kovacs", "A.B. Kovacs"),
				personAddr("ann-author"):   testpack.PersonJSON(t, "ann-author", "Ann Author"),
				workAddr("the-novel"):      testpack.WorkJSON(t, "the-novel", "The Novel", testpack.WithAuthors("ann-author")),
				recAddr("the-novel", "r1"): testpack.RecJSON(t, "r1", "the-novel", testpack.WithNarrators("a-b-kovacs")),
			}, nil)
			sum := runLibexWith(t, dataDir, Options{Distinct: tc.distinct},
				tombRow("B0KOVACS01", "Healthy Habits", "Dr. AB Kovacs", "Bea Reader", 300, "", ""),
				tombRow("B0KOVACS02", "Second Opinion", "Dr. A.B. Kovacs", "Bea Reader", 300, "", ""),
				tombRow("B0KOVACS03", "Third Opinion", "Dr. A.B. Kovacs", "Bea Reader", 300, "", ""),
				tombRow("B0KOVACS04", "The Sequel", "AB Kovacs", "Bea Reader", 300, "", ""))
			if got := workAuthorsOf(t, dataDir, "healthy-habits"); !reflect.DeepEqual(got, []string{tc.want}) {
				t.Errorf("Healthy Habits authors = %v, want [%s]\nmerges: %v", got, tc.want, sum.HonorificMerges)
			}
			if got := workAuthorsOf(t, dataDir, "second-opinion"); !reflect.DeepEqual(got, []string{"dr-a-b-kovacs"}) {
				t.Errorf("Second Opinion authors = %v, want [dr-a-b-kovacs]", got)
			}
			if got := workAuthorsOf(t, dataDir, "the-sequel"); !reflect.DeepEqual(got, []string{"a-b-kovacs"}) {
				t.Errorf("The Sequel authors = %v, want [a-b-kovacs]", got)
			}
			if folded := slices.Contains(sum.HonorificMerges, "Dr. AB Kovacs -> AB Kovacs"); folded != (tc.want == "a-b-kovacs") {
				t.Errorf("HonorificMerges = %v, want the fold reported only when it happened", sum.HonorificMerges)
			}
			mustValidate(t, dataDir)
		})
	}
}

// Rule 1b asks the review by the records the two spellings LAND on: "Dr. AB Kovacs"
// lands on the committed `dr-a-b-kovacs` and "AB Kovacs" (a reader of the same
// project, which is what makes the initials group decide) on `a-b-kovacs`. Neither
// spelled slug is one the review names. Without the review the bare record's dates
// leave the id unsure, so it goes to the worklist; with it the bare record is not
// the question, and the credit lands on its own.
func TestLibrivoxFoldVerdictJudgesTheLandedRecords(t *testing.T) {
	for name, tc := range map[string]struct {
		distinct []check.ReviewedDistinct
		wantHeld bool
	}{
		"no review":     {wantHeld: true},
		"other pair":    {distinct: peopleApart("alex-george", "dr-alex-george"), wantHeld: true},
		"reviewed pair": {distinct: peopleApart("a-b-kovacs", "dr-a-b-kovacs")},
	} {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			files := map[string]string{
				personAddr("a-b-kovacs"):    personFiles(t, "a-b-kovacs", "A.B. Kovacs", nil),
				personAddr("dr-a-b-kovacs"): personFiles(t, "dr-a-b-kovacs", "Dr. A.B. Kovacs", nil),
			}
			retailWork(t, files, "a-modern-thriller", "A Modern Thriller", "a-b-kovacs", "")
			seedTree(t, dataDir, files)
			pr := withReader(byAuthors(t, 99300, "The Old Clinic", lvDated(5601, "Dr. AB", "Kovacs", "1800", "1870")), 7301, "AB Kovacs")
			pr.CopyrightYear = lvText("1850")
			sum := runLibrivoxWith(t, writeSnapshot(t, pr), dataDir, allLibrivox, Options{Distinct: tc.distinct})
			if tc.wantHeld {
				if len(sum.LibrivoxWorklist) != 1 || sum.LibrivoxWorklist[0].Reason != LibrivoxSameNameAuthor {
					t.Errorf("worklist = %v, want the id held as same-name-author", sum.LibrivoxWorklist)
				}
			} else {
				if len(sum.LibrivoxWorklist) != 0 {
					t.Errorf("worklist = %v, want none: a review already told the two apart", sum.LibrivoxWorklist)
				}
				if got := workAuthorsOf(t, dataDir, "the-old-clinic"); !reflect.DeepEqual(got, []string{"dr-a-b-kovacs"}) {
					t.Errorf("authors = %v, want [dr-a-b-kovacs]\nnotes: %v", got, sum.Notes)
				}
			}
			if p := personOf(t, dataDir, "a-b-kovacs"); p.AuthorID != 0 || p.DeathYear != 0 {
				t.Errorf("a-b-kovacs = %+v, want it unstamped", p)
			}
			mustValidate(t, dataDir)
		})
	}
}
