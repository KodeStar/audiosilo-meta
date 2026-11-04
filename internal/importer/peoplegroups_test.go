package importer

// The series grouping is transitive and a review is pairwise, so a spelling no
// review names can stand for both people of a reviewed pair: with a review of
// (alex-george, dr-alex-george), "A. George" is a spelling of each. The review is
// therefore a NEVER-JOIN constraint inside the grouping (personGroups), and a row
// reaches a group only if nobody in it is someone the review keeps apart from the
// row (seriesAuthors.sharedWith). The issue-form gates ask the same index.

import (
	"fmt"
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

func TestSeriesGroupsNeverJoinAReviewedPair(t *testing.T) {
	novelist, doctor, initial := georgeForms(georgeReview)
	if !initial.same(novelist) || !initial.same(doctor) || novelist.same(doctor) {
		t.Fatalf("fixture: initial~novelist %v, initial~doctor %v, novelist~doctor %v",
			initial.same(novelist), initial.same(doctor), novelist.same(doctor))
	}
	people := func(sa *seriesAuthors) int {
		sa.refresh()
		return len(sa.credits)
	}
	for name, order := range map[string][]personForm{
		"the initial last":    {novelist, doctor, initial},
		"the initial first":   {initial, doctor, novelist},
		"the initial between": {doctor, initial, novelist},
	} {
		t.Run(name, func(t *testing.T) {
			sa := &seriesAuthors{}
			for i, f := range order {
				sa.add(string(rune('a'+i)), []personForm{f}, nil)
			}
			if got := people(sa); got != 2 {
				t.Errorf("%d people, want 2: the initial joins one of the pair", got)
			}
			if sa.root("alex-george") == sa.root("dr-alex-george") {
				t.Error("the reviewed pair is one group")
			}
			// The first person the initial met keeps it, whatever came next.
			first := order[0].slug
			if order[0].slug == "a-george" {
				first = order[1].slug
			}
			if sa.root("a-george") != sa.root(first) {
				t.Errorf("the initial joined %s, want %s", sa.root("a-george"), first)
			}
			// A clone (what admits judges) keeps the constraint.
			c := sa.clone()
			c.add("z", []personForm{doctor}, nil)
			if c.root("alex-george") == c.root("dr-alex-george") {
				t.Error("the clone lost the constraint")
			}
		})
	}
	t.Run("a group is judged whole", func(t *testing.T) {
		// The doctor meets the novelist's group through its middle-name spelling,
		// which no review names and which he is pairwise a spelling of.
		middle := georgeReview.form("alex-j-george", "Alex J. George")
		if !middle.same(doctor) {
			t.Fatal("fixture: the middle-name spelling is not a spelling of the doctor")
		}
		sa := &seriesAuthors{}
		sa.add("a", []personForm{novelist}, nil)
		sa.add("b", []personForm{middle}, nil)
		sa.add("c", []personForm{doctor}, nil)
		sa.add("d", []personForm{initial}, nil)
		if sa.root("alex-j-george") != "alex-george" || sa.root("a-george") != "alex-george" {
			t.Errorf("the novelist's spellings are %s and %s", sa.root("alex-j-george"), sa.root("a-george"))
		}
		if sa.root("dr-alex-george") == "alex-george" {
			t.Error("the doctor joined the novelist's group")
		}
	})
	t.Run("no review of the pair", func(t *testing.T) {
		// The grouping is unchanged for everyone else: all three are one person.
		for _, ap := range []*PeopleApart{nil, otherReview} {
			n, d, i := georgeForms(ap)
			sa := &seriesAuthors{}
			sa.add("a", []personForm{n}, nil)
			sa.add("b", []personForm{d}, nil)
			sa.add("c", []personForm{i}, nil)
			if got := people(sa); got != 1 {
				t.Errorf("%d people, want 1", got)
			}
		}
	})
}

func TestSeriesFitAsksTheWholeGroup(t *testing.T) {
	novelist, doctor, initial := georgeForms(georgeReview)
	series := func(forms ...personForm) *seriesAuthors {
		sa := &seriesAuthors{}
		for i, f := range forms {
			sa.add(string(rune('a'+i)), []personForm{f}, nil)
		}
		return sa
	}
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
		// holds the novelist beside the initial.
		"doctor, novelist's series": {series(novelist, novelist, initial), rowOf(doctor), seriesClosed},
		"novelist, doctor's series": {series(doctor, initial, initial), rowOf(novelist), seriesClosed},
		"cluster, doctor's series":  {series(doctor, doctor), rowOf(initial, novelist), seriesClosed},
		// The allowed half: the review says nothing about a group holding neither,
		// the initial itself is still the novelist, and the novelist still meets
		// his own series.
		"doctor, the initial alone":  {series(initial, initial), rowOf(doctor), seriesShared},
		"initial, novelist's series": {series(novelist, novelist), rowOf(initial), seriesShared},
		"novelist, own series":       {series(novelist, initial), rowOf(novelist), seriesShared},
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
	claim := func(ap *PeopleApart, order int, authors ...seriesPerson) nameClaim {
		return nameClaim{row: &SeriesRow{authors: authors, apart: ap}, order: fmt.Sprintf("%03d", order)}
	}
	clusters := func(ap *PeopleApart, authors ...[]seriesPerson) [][]int {
		claims := make([]nameClaim, len(authors))
		idx := make([]int, len(authors))
		for i, a := range authors {
			claims[i], idx[i] = claim(ap, i, a...), i
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
	titles := make([]string, 0, len(works))
	for w := range works {
		titles = append(titles, w)
	}
	slices.Sort(titles)
	for i, w := range titles {
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

// The same bridge inside one batch, with no catalogued series to judge against:
// the novelist's rows found the series, the initial joins them and the doctor founds
// his own, rather than the initial carrying all three into one. The evidence decides
// whose the initial is, not the slug order that puts `dr-` first: with more rows the
// doctor founds and the initial joins him (TestBatchBridgeFollowsTheEvidence).
func TestBatchClusterDoesNotBridgeAReviewedPair(t *testing.T) {
	rows := []string{
		tombRow("B0CLUSTER1", "Volume One", "A. George", "Nate Narrator", 300, "New Saga", "1"),
		tombRow("B0CLUSTER2", "Volume Two", "Alex George", "Nate Narrator", 300, "New Saga", "2"),
		tombRow("B0CLUSTER3", "Volume Three", "Alex George", "Nate Narrator", 300, "New Saga", "3"),
		tombRow("B0CLUSTER4", "Volume Four", "Dr. Alex George", "Nate Narrator", 300, "New Saga", "4"),
	}
	for name, tc := range map[string]struct {
		distinct []check.ReviewedDistinct
		want     map[string][]string
	}{
		"reviewed pair": {peopleApart("alex-george", "dr-alex-george"), map[string][]string{
			"new-saga": {"volume-one", "volume-three", "volume-two"}, "new-saga-2": {"volume-four"},
		}},
		"other pair": {peopleApart("adam-brown", "mr-adam-brown"), map[string][]string{
			"new-saga": {"volume-four", "volume-one", "volume-three", "volume-two"},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			dataDir := seedTombstoneTree(t, map[string]string{personAddr("nate-narrator"): testpack.PersonJSON(t, "nate-narrator", "Nate Narrator")}, nil)
			runLibexWith(t, dataDir, Options{Distinct: tc.distinct}, rows...)
			for slug, want := range tc.want {
				got := make([]string, 0, len(want))
				for w := range seriesWorks(t, dataDir, slug) {
					got = append(got, w)
				}
				slices.Sort(got)
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s = %v, want %v", slug, got, want)
				}
			}
			mustValidate(t, dataDir)
		})
	}
}

func TestBatchBridgeFollowsTheEvidence(t *testing.T) {
	dataDir := seedTombstoneTree(t, map[string]string{personAddr("nate-narrator"): testpack.PersonJSON(t, "nate-narrator", "Nate Narrator")}, nil)
	runLibexWith(t, dataDir, Options{Distinct: peopleApart("alex-george", "dr-alex-george")},
		tombRow("B0CLUSTER1", "Volume One", "Dr. Alex George", "Nate Narrator", 300, "New Saga", "1"),
		tombRow("B0CLUSTER2", "Volume Two", "Dr. Alex George", "Nate Narrator", 300, "New Saga", "2"),
		tombRow("B0CLUSTER3", "Volume Three", "A. George", "Nate Narrator", 300, "New Saga", "3"),
		tombRow("B0CLUSTER4", "Volume Four", "Alex George", "Nate Narrator", 300, "New Saga", "4"))
	saga := seriesWorks(t, dataDir, "new-saga")
	for _, w := range []string{"volume-one", "volume-two", "volume-three"} {
		if _, ok := saga[w]; !ok {
			t.Errorf("new-saga = %v, want %s in it", saga, w)
		}
	}
	if _, ok := seriesWorks(t, dataDir, "new-saga-2")["volume-four"]; !ok {
		t.Errorf("the novelist's volume is not in new-saga-2; new-saga = %v", saga)
	}
	mustValidate(t, dataDir)
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
				if p := personOf(t, dataDir, "a-b-kovacs"); p.AuthorID != 0 || p.DeathYear != 0 {
					t.Errorf("the reviewed-apart bare record was stamped: %+v", p)
				}
			}
			if p := personOf(t, dataDir, "a-b-kovacs"); p.AuthorID != 0 {
				t.Errorf("a-b-kovacs = %+v, want no author id", p)
			}
			mustValidate(t, dataDir)
		})
	}
}
