package importer

// The series same-person rule honours a REVIEW (PeopleApart): a merge-people
// rejection recording "Dr. Alex George" the TV doctor and Alex George the
// novelist as two people keeps them two AUTHORS too, so the doctor's book never
// joins the novelist's series by a rule meant to keep one author's spellings
// together (the series-hijack pattern).

import (
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

func TestPeopleApartSamePerson(t *testing.T) {
	reds := model.Redirects{model.RedirectPeople: {"alex-george-novelist": "alex-george"}}
	apart := NewPeopleApart(peopleApart("alex-george-novelist", "dr-alex-george"), reds)
	for _, tc := range []struct {
		name          string
		ap            *PeopleApart
		slugA, nameA  string
		slugB, nameB  string
		want, wantHas bool
	}{
		// Without a review the courtesy title is stripped and the two are one.
		{"no review", nil, "dr-alex-george", "Dr. Alex George", "alex-george", "Alex George", true, false},
		// The review names the retired id; its survivor is kept apart.
		{"reviewed pair", apart, "dr-alex-george", "Dr. Alex George", "alex-george", "Alex George", false, true},
		{"reviewed pair, retired spelling", apart, "alex-george-novelist", "Alex George", "dr-alex-george", "Dr. Alex George", false, true},
		// An unreviewed pair still meets by every rung.
		{"other pair", apart, "dr-samuel-li", "Dr. Samuel Li", "samuel-li", "Samuel Li", true, false},
		{"initials", apart, "a-b-kovacs", "A.B. Kovacs", "ab-kovacs", "AB Kovacs", true, false},
		// One slug is one person whatever a review says (a review never names a
		// slug twice).
		{"one slug", apart, "alex-george", "Alex George", "alex-george", "Alex George", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.ap.SamePerson(tc.slugA, tc.nameA, tc.slugB, tc.nameB); got != tc.want {
				t.Errorf("SamePerson = %v, want %v", got, tc.want)
			}
			if got := tc.ap.SamePerson(tc.slugB, tc.nameB, tc.slugA, tc.nameA); got != tc.want {
				t.Errorf("SamePerson reversed = %v, want %v", got, tc.want)
			}
			if got := tc.ap.Has(tc.slugA, tc.slugB); got != tc.wantHas {
				t.Errorf("Has = %v, want %v", got, tc.wantHas)
			}
			// A form carrying the review meets one built without it under the
			// review: either side is enough.
			plain, reviewed := formOf(tc.slugB, tc.nameB), tc.ap.form(tc.slugA, tc.nameA)
			if reviewed.same(plain) != tc.want || plain.same(reviewed) != tc.want {
				t.Errorf("a one-sided review: same = %v / %v, want %v", reviewed.same(plain), plain.same(reviewed), tc.want)
			}
		})
	}
	if NewPeopleApart(nil, nil) != nil || NewPeopleApart([]check.ReviewedDistinct{{Kind: model.RedirectWorks, IDs: []string{"a", "b"}}}, nil) != nil {
		t.Error("no people decision must build no review")
	}
}

// mindMattersTree is the novelist Alex George's two-volume `mind-matters`.
func mindMattersTree(t *testing.T) string {
	t.Helper()
	files := map[string]string{
		personAddr("alex-george"):   testpack.PersonJSON(t, "alex-george", "Alex George"),
		personAddr("nate-narrator"): testpack.PersonJSON(t, "nate-narrator", "Nate Narrator"),
		seriesAddr("mind-matters"):  testpack.SeriesJSON(t, "mind-matters", "Mind Matters", "a-good-american@1", "the-paris-hours@2"),
	}
	for _, w := range []string{"a-good-american", "the-paris-hours"} {
		files[workAddr(w)] = testpack.WorkJSON(t, w, w, testpack.WithAuthors("alex-george"))
		files[recAddr(w, "r1")] = testpack.RecJSON(t, "r1", w, testpack.WithNarrators("nate-narrator"))
	}
	return seedTombstoneTree(t, files, nil)
}

// The doctor's "Mind Matters" book does not join the novelist's series once a
// review keeps the two apart: the honorific fold already leaves the credit on
// dr-alex-george, and the series rule no longer strips the title to make him
// the series' own author, so the claim starts a series of its own down the
// chain. Without a review of this pair the fold makes the credit the
// novelist's, and the book joins - the rule doing its job.
func TestReviewedPairIsNotTheSeriesAuthor(t *testing.T) {
	for name, tc := range map[string]struct {
		distinct    []check.ReviewedDistinct
		wantAuthor  string
		wantJoined  bool
		wantNewName string
	}{
		"reviewed pair": {distinct: peopleApart("alex-george", "dr-alex-george"), wantAuthor: "dr-alex-george", wantNewName: "mind-matters-2"},
		"other pair":    {distinct: peopleApart("adam-brown", "mr-adam-brown"), wantAuthor: "alex-george", wantJoined: true},
	} {
		t.Run(name, func(t *testing.T) {
			dataDir := mindMattersTree(t)
			sum := runLibexWith(t, dataDir, Options{Distinct: tc.distinct},
				tombRow("B0MINDMAN3", "The Mind Manual", "Dr. Alex George", "Nate Narrator", 300, "Mind Matters", "3"))
			if got := workAuthorsOf(t, dataDir, "the-mind-manual"); len(got) != 1 || got[0] != tc.wantAuthor {
				t.Fatalf("authors = %v, want [%s]", got, tc.wantAuthor)
			}
			_, joined := seriesWorks(t, dataDir, "mind-matters")["the-mind-manual"]
			if joined != tc.wantJoined {
				t.Errorf("the book joined mind-matters = %v, want %v\nwarnings: %v", joined, tc.wantJoined, sum.Warnings)
			}
			if tc.wantNewName != "" && seriesWorks(t, dataDir, tc.wantNewName)["the-mind-manual"] != "3" {
				t.Errorf("the book was not placed at 3 in %s", tc.wantNewName)
			}
			mustValidate(t, dataDir)
		})
	}
}
