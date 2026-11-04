package importer

// The honorific fold honours a REVIEW (Options.Distinct): a merge-people
// rejection in internal/audit/reviewed.json is how a maintainer records "two
// people, not one" for an honorific pair, and no fold may re-merge it - on the
// plain importer's titled-onto-bare fold, nor on the LibriVox rule 1 paths in
// either direction. Each test keeps an unreviewed pair beside the reviewed one,
// so the fold is shown still doing its job.

import (
	"reflect"
	"slices"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// peopleApart is one reviewed merge-people rejection over ids.
func peopleApart(ids ...string) []check.ReviewedDistinct {
	return []check.ReviewedDistinct{{Kind: model.RedirectPeople, IDs: ids}}
}

func TestHonorificFoldHonoursAReviewedRejection(t *testing.T) {
	for name, tc := range map[string]struct {
		distinct []check.ReviewedDistinct
		reds     model.Redirects
		want     string // the author of Live Well Every Day
	}{
		// Without the review the census folds the doctor onto the novelist:
		// the defect the review records.
		"no review folds": {want: "alex-george"},
		"reviewed pair":   {distinct: peopleApart("alex-george", "dr-alex-george"), want: "dr-alex-george"},
		// A decision taken before a repair retired one of its ids still names
		// that id's survivor.
		"retired id": {
			distinct: peopleApart("alex-george-novelist", "dr-alex-george"),
			reds:     model.Redirects{model.RedirectPeople: {"alex-george-novelist": "alex-george"}},
			want:     "dr-alex-george",
		},
		// A rejection of another pair says nothing about this one.
		"other pair": {distinct: peopleApart("adam-brown", "mr-adam-brown"), want: "alex-george"},
	} {
		t.Run(name, func(t *testing.T) {
			dataDir := seedTombstoneTree(t, map[string]string{
				personAddr("alex-george"):        testpack.PersonJSON(t, "alex-george", "Alex George"),
				personAddr("arthur-conan-doyle"): testpack.PersonJSON(t, "arthur-conan-doyle", "Arthur Conan Doyle"),
				workAddr("a-good-american"):      testpack.WorkJSON(t, "a-good-american", "A Good American", testpack.WithAuthors("alex-george")),
				recAddr("a-good-american", "r1"): testpack.RecJSON(t, "r1", "a-good-american", testpack.WithNarrators("bea-reader")),
				workAddr("a-study-in-scarlet"):   testpack.WorkJSON(t, "a-study-in-scarlet", "A Study In Scarlet", testpack.WithAuthors("arthur-conan-doyle")),
				recAddr("a-study-in-scarlet", "r1"): testpack.RecJSON(t, "r1", "a-study-in-scarlet",
					testpack.WithNarrators("bea-reader")),
			}, tc.reds)

			sum := runLibexWith(t, dataDir, Options{Distinct: tc.distinct},
				tombRow("B0REVIEW01", "Live Well Every Day", "Dr. Alex George", "Bea Reader", 300, "", ""),
				tombRow("B0REVIEW02", "The Sign Of Four", "Sir Arthur Conan Doyle", "Bea Reader", 300, "", ""))

			if got := workAuthorsOf(t, dataDir, "live-well-every-day"); !reflect.DeepEqual(got, []string{tc.want}) {
				t.Errorf("Live Well Every Day authors = %v, want [%s]\nmerges: %v", got, tc.want, sum.HonorificMerges)
			}
			if got := workAuthorsOf(t, dataDir, "the-sign-of-four"); !reflect.DeepEqual(got, []string{"arthur-conan-doyle"}) {
				t.Errorf("the unreviewed pair no longer folds: The Sign Of Four authors = %v", got)
			}
			folded := slices.Contains(sum.HonorificMerges, "Dr. Alex George -> Alex George")
			if folded != (tc.want == "alex-george") {
				t.Errorf("HonorificMerges = %v, want the doctor's fold reported only when it happened", sum.HonorificMerges)
			}
			if !slices.Contains(sum.HonorificMerges, "Sir Arthur Conan Doyle -> Arthur Conan Doyle") {
				t.Errorf("HonorificMerges = %v, want the unreviewed fold reported", sum.HonorificMerges)
			}
			mustValidate(t, dataDir)
		})
	}
}

// reviewedCase is one run of a reviewed-rejection table: the decisions given
// and the person the credit must land on.
type reviewedCase struct {
	distinct []check.ReviewedDistinct
	want     string
}

// reviewedCases is the table both LibriVox tests run: no review (the rule
// fires and lands on fired), a review of this very pair (the credit stays on
// apart) and a review of another pair (which changes nothing).
func reviewedCases(pair []string, fired, apart string) map[string]reviewedCase {
	return map[string]reviewedCase{
		"no review":     {want: fired},
		"reviewed pair": {distinct: peopleApart(pair...), want: apart},
		"other pair":    {distinct: peopleApart("alex-george", "dr-alex-george"), want: fired},
	}
}

func TestLibrivoxTitledFoldHonoursAReviewedRejection(t *testing.T) {
	// Rule 1b's own case (TestLibrivoxGenuineCourtesyTitleStillFolds, a dated
	// work): "Sir Arthur Conan Doyle" folds onto the catalogued
	// arthur-conan-doyle, whose work is dated within his life - unless a review
	// recorded two people.
	for name, tc := range reviewedCases([]string{"arthur-conan-doyle", "sir-arthur-conan-doyle"}, "arthur-conan-doyle", "sir-arthur-conan-doyle") {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			files := map[string]string{personAddr("arthur-conan-doyle"): personFiles(t, "arthur-conan-doyle", "Arthur Conan Doyle", nil)}
			retailWork(t, files, "retail", "The Land of Mist", "arthur-conan-doyle", "1926")
			seedTree(t, dataDir, files)
			snap := writeSnapshot(t, byAuthors(t, 99003, "The Poison Belt", lvDated(69, "Sir Arthur Conan", "Doyle", "1859", "1930")))
			sum := runLibrivoxWith(t, snap, dataDir, allLibrivox, Options{Distinct: tc.distinct})
			if got := workAuthorsOf(t, dataDir, "the-poison-belt"); !reflect.DeepEqual(got, []string{tc.want}) {
				t.Errorf("authors = %v, want [%s]\nnotes: %v", got, tc.want, sum.Notes)
			}
			if p := personOf(t, dataDir, tc.want); p.AuthorID != 69 {
				t.Errorf("%s = %+v, want the author id stamped there\nnotes: %v", tc.want, p, sum.Notes)
			}
			if tc.want == "sir-arthur-conan-doyle" {
				if p := personOf(t, dataDir, "arthur-conan-doyle"); p.AuthorID != 0 || p.DeathYear != 0 {
					t.Errorf("the reviewed-apart bare record was stamped: %+v", p)
				}
				if len(sum.HonorificMerges) != 0 {
					t.Errorf("HonorificMerges = %v, want none", sum.HonorificMerges)
				}
			}
			mustValidate(t, dataDir)
		})
	}
}

func TestLibrivoxTitledTwinHonoursAReviewedRejection(t *testing.T) {
	// Rule 1c's own case (TestLibrivoxBareAuthorResolvesOntoATitledTwin): a
	// bare "Frederick Treves" resolves onto the catalogued sir-frederick-treves
	// - unless a review recorded two people, when the bare name is minted.
	for name, tc := range reviewedCases([]string{"frederick-treves", "sir-frederick-treves"}, "sir-frederick-treves", "frederick-treves") {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			files := map[string]string{personAddr("sir-frederick-treves"): personFiles(t, "sir-frederick-treves", "Sir Frederick Treves", nil)}
			retailWork(t, files, "a-retail-book", "A Retail Book", "sir-frederick-treves", "")
			seedTree(t, dataDir, files)
			snap := writeSnapshot(t, byAuthors(t, 99001, "Elephant Man and Other Reminiscences", lvDated(14047, "Frederick", "Treves", "1853", "1923")))
			sum := runLibrivoxWith(t, snap, dataDir, allLibrivox, Options{Distinct: tc.distinct})
			if got := workAuthorsOf(t, dataDir, "elephant-man-and-other-reminiscences"); !reflect.DeepEqual(got, []string{tc.want}) {
				t.Fatalf("authors = %v, want [%s]\nnotes: %v", got, tc.want, sum.Notes)
			}
			if tc.want == "frederick-treves" {
				if p := personOf(t, dataDir, "sir-frederick-treves"); p.AuthorID != 0 || p.DeathYear != 0 {
					t.Errorf("the reviewed-apart titled record was stamped: %+v", p)
				}
			}
			mustValidate(t, dataDir)
		})
	}
}

// The check judges the record a credit LANDS on, not only the slug it spells: by
// the initials merge "Dr. AB Kovacs" is the committed `dr-a-b-kovacs` and "AB
// Kovacs" the committed `a-b-kovacs`, so a review of THAT pair keeps the titled
// credit off the bare record even though neither spelled slug is one the review
// names. The titled credit then lands on its own committed record, as the
// initials merge would put it.
func TestHonorificFoldJudgesTheLandingRecord(t *testing.T) {
	for name, tc := range reviewedCases([]string{"a-b-kovacs", "dr-a-b-kovacs"}, "a-b-kovacs", "dr-a-b-kovacs") {
		t.Run(name, func(t *testing.T) {
			dataDir := seedTombstoneTree(t, map[string]string{
				personAddr("a-b-kovacs"):    testpack.PersonJSON(t, "a-b-kovacs", "A.B. Kovacs"),
				personAddr("dr-a-b-kovacs"): testpack.PersonJSON(t, "dr-a-b-kovacs", "Dr. A.B. Kovacs"),
				workAddr("the-novel"):       testpack.WorkJSON(t, "the-novel", "The Novel", testpack.WithAuthors("a-b-kovacs")),
				recAddr("the-novel", "r1"):  testpack.RecJSON(t, "r1", "the-novel", testpack.WithNarrators("bea-reader")),
				workAddr("the-clinic"):      testpack.WorkJSON(t, "the-clinic", "The Clinic", testpack.WithAuthors("dr-a-b-kovacs")),
				recAddr("the-clinic", "r1"): testpack.RecJSON(t, "r1", "the-clinic", testpack.WithNarrators("bea-reader")),
			}, nil)
			sum := runLibexWith(t, dataDir, Options{Distinct: tc.distinct},
				tombRow("B0KOVACS01", "Healthy Habits", "Dr. AB Kovacs", "Bea Reader", 300, "", ""),
				tombRow("B0KOVACS02", "The Sequel", "AB Kovacs", "Bea Reader", 300, "", ""))
			if got := workAuthorsOf(t, dataDir, "healthy-habits"); !reflect.DeepEqual(got, []string{tc.want}) {
				t.Errorf("Healthy Habits authors = %v, want [%s]\nmerges: %v", got, tc.want, sum.HonorificMerges)
			}
			if got := workAuthorsOf(t, dataDir, "the-sequel"); !reflect.DeepEqual(got, []string{"a-b-kovacs"}) {
				t.Errorf("the bare credit no longer meets its record: The Sequel authors = %v", got)
			}
			mustValidate(t, dataDir)
		})
	}
}

// The credential fold honours the same review: "Michael Salla PhD" folds onto
// the bare `michael-salla` the census holds - unless a review recorded the two
// records as two people, when the credit stays on its own committed record. Both
// spellings of the credential are covered: in the name, and as a ", PhD" list
// piece put back on it (peelSuffixChunk).
func TestCredentialFoldHonoursAReviewedRejection(t *testing.T) {
	for name, tc := range reviewedCases([]string{"michael-salla", "michael-salla-phd"}, "michael-salla", "michael-salla-phd") {
		t.Run(name, func(t *testing.T) {
			dataDir := seedTombstoneTree(t, map[string]string{
				personAddr("michael-salla"):     testpack.PersonJSON(t, "michael-salla", "Michael Salla"),
				personAddr("michael-salla-phd"): testpack.PersonJSON(t, "michael-salla-phd", "Michael Salla PhD"),
				workAddr("the-novel"):           testpack.WorkJSON(t, "the-novel", "The Novel", testpack.WithAuthors("michael-salla")),
				recAddr("the-novel", "r1"):      testpack.RecJSON(t, "r1", "the-novel", testpack.WithNarrators("bea-reader")),
			}, nil)
			sum := runLibexWith(t, dataDir, Options{Distinct: tc.distinct},
				tombRow("B0SALLA001", "Exopolitics", "Michael Salla PhD", "Bea Reader", 300, "", ""),
				tombRow("B0SALLA002", "Galactic Diplomacy", "Michael Salla, PhD", "Bea Reader", 300, "", ""))
			for _, w := range []string{"exopolitics", "galactic-diplomacy"} {
				if got := workAuthorsOf(t, dataDir, w); !reflect.DeepEqual(got, []string{tc.want}) {
					t.Errorf("%s authors = %v, want [%s]\nmerges: %v", w, got, tc.want, sum.CredentialMerges)
				}
			}
			if folded := len(sum.CredentialMerges) > 0; folded != (tc.want == "michael-salla") {
				t.Errorf("CredentialMerges = %v, want a fold reported only when one happened", sum.CredentialMerges)
			}
			mustValidate(t, dataDir)
		})
	}
}

// The unfolded census turns off the honorific fold ALONE: a LibriVox credit
// "Jane Doe PhD" folds onto the catalogued jane-doe by the credential fold in
// both readings, so noteLibrivoxFolds sees no courtesy title and reports the
// merge in the credential stream only - not again as an honorific one.
func TestLibrivoxCredentialFoldIsNoHonorificMerge(t *testing.T) {
	dataDir := t.TempDir()
	files := map[string]string{personAddr("jane-doe"): personFiles(t, "jane-doe", "Jane Doe", nil)}
	retailWork(t, files, "a-retail-book", "A Retail Book", "jane-doe", "1900")
	seedTree(t, dataDir, files)
	snap := writeSnapshot(t, byAuthors(t, 99011, "The Credential Book", lvDated(5151, "Jane", "Doe PhD", "1850", "1920")))
	sum := runLibrivoxWith(t, snap, dataDir, allLibrivox, Options{})
	if got := workAuthorsOf(t, dataDir, "the-credential-book"); !reflect.DeepEqual(got, []string{"jane-doe"}) {
		t.Fatalf("authors = %v, want [jane-doe]", got)
	}
	if len(sum.HonorificMerges) != 0 {
		t.Errorf("HonorificMerges = %v, want none: the fold is a credential's", sum.HonorificMerges)
	}
	if !reflect.DeepEqual(sum.CredentialMerges, []string{"Jane Doe PhD -> Jane Doe"}) {
		t.Errorf("CredentialMerges = %v, want the credential fold reported", sum.CredentialMerges)
	}
	mustValidate(t, dataDir)
}
