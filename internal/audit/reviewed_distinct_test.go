package audit

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/canonical"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// reviewed_distinct_test.go pins ReviewedDistinct, the reader cmd/metacheck hands the
// advisory census: exactly the merge rejections, in their own namespace, target and
// others together - and that what it returns is what pkg/check's load acts on.

// mustParse canonicalizes and parses a reviewed list, failing on any defect.
func mustParse(t *testing.T, raw string) []reviewedDecision {
	t.Helper()
	b, err := canonical.Format([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	rs, err := parseReviewed(b)
	if err != nil {
		t.Fatalf("fixture list refused: %v", err)
	}
	return rs
}

func TestReviewedDistinctReadsOnlyMergeRejections(t *testing.T) {
	rs := mustParse(t, `[`+
		`{"decision":"reject","op":"add-work-link","field":"translation_of","target":"b-german","to":"b","reason":"a link, not a merge"},`+
		`{"decision":"reject","op":"merge-series","target":"saga","others":["saga-abridged","saga-two"],"reason":"three series"},`+
		`{"decision":"accept","op":"merge-works","target":"a","others":["a-decorated"],"reason":"one book"},`+
		`{"decision":"assert","op":"merge-works","target":"c","others":["c-decorated"],"reason":"one book"},`+
		`{"decision":"reject","op":"merge-works","target":"d","others":["d-sequel"],"reason":"two books"}`+
		`]`)
	want := []check.ReviewedDistinct{
		{Kind: model.RedirectSeries, IDs: []string{"saga", "saga-abridged", "saga-two"}},
		{Kind: model.RedirectWorks, IDs: []string{"d", "d-sequel"}},
	}
	if got := distinctOf(rs); !reflect.DeepEqual(got, want) {
		t.Errorf("distinctOf = %+v, want %+v", got, want)
	}
}

// Every decision the embedded list hands the census names a pair at least.
func TestReviewedDistinctOverTheEmbeddedList(t *testing.T) {
	for _, d := range ReviewedDistinct() {
		if len(d.IDs) < 2 {
			t.Errorf("decision %+v names fewer than two records", d)
		}
	}
}

// End to end: a rejection written the way a census triage writes it - target and
// others the group's ids - takes the group out of pkg/check's census.
func TestReviewedDistinctSuppressesTheCensusGroup(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	testpack.Seed(t, data, map[string]string{
		"people/ja/jane-doe.json":         testpack.PersonJSON(t, "jane-doe", "Jane Doe"),
		"works/ho/hollow-crown/work.json": testpack.WorkJSON(t, "hollow-crown", "Hollow Crown"),
		"works/ho/hollow-crown-a-dark-fantasy-adventure/work.json": testpack.WorkJSON(t,
			"hollow-crown-a-dark-fantasy-adventure", "Hollow Crown: A Dark Fantasy Adventure"),
	})
	census := func(ds []check.ReviewedDistinct) check.Result {
		t.Helper()
		res := check.LoadProfileWith(data, pack.ProfileAll, check.LoadOptions{Distinct: ds})
		if !res.OK() {
			t.Fatalf("fixture does not validate: %v", res.Problems)
		}
		return res
	}
	const marker = "under two spellings of its title"
	count := func(res check.Result) (n int) {
		for _, w := range res.Warnings {
			if strings.Contains(w.Msg, marker) {
				n++
			}
		}
		return n
	}
	if res := census(nil); count(res) != 1 {
		t.Fatalf("unreviewed census found %d groups, want 1: %v", count(res), res.Warnings)
	}
	rejection := review(Proposal{Op: OpMergeWorks, Target: "hollow-crown",
		Others: []string{"hollow-crown-a-dark-fantasy-adventure"}}, "reject")
	res := census(distinctOf([]reviewedDecision{rejection}))
	if count(res) != 0 || res.Suppressed != 1 {
		t.Errorf("reviewed census: %d groups, %d suppressed; want 0 and 1", count(res), res.Suppressed)
	}
}

// A `merge-people` decision is a reject naming a target and distinct others -
// nothing else parses - and it reads into the people namespace. No detector
// proposes the op, so the audit reports it census-only, never stale.
func TestReviewedMergePeopleRejection(t *testing.T) {
	rs := mustParse(t, `[{"decision":"reject","op":"merge-people","others":["sir-richard-burton"],`+
		`"reason":"the Victorian translator and the actor are two people","target":"richard-burton"}]`)
	want := []check.ReviewedDistinct{{Kind: model.RedirectPeople, IDs: []string{"richard-burton", "sir-richard-burton"}}}
	if got := distinctOf(rs); !reflect.DeepEqual(got, want) {
		t.Errorf("distinctOf = %+v, want %+v", got, want)
	}
	for name, entry := range map[string]string{
		"accept":    `{"decision":"accept","op":"merge-people","others":["b"],"reason":"r","target":"a"}`,
		"assert":    `{"decision":"assert","op":"merge-people","others":["b"],"reason":"r","target":"a"}`,
		"no target": `{"decision":"reject","op":"merge-people","others":["b"],"reason":"r"}`,
		"no others": `{"decision":"reject","op":"merge-people","reason":"r","target":"a"}`,
		"a field":   `{"decision":"reject","field":"name","op":"merge-people","others":["b"],"reason":"r","target":"a"}`,
		"self":      `{"decision":"reject","op":"merge-people","others":["a"],"reason":"r","target":"a"}`,
	} {
		raw, err := canonical.Format([]byte("[" + entry + "]"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := parseReviewed(raw); err == nil {
			t.Errorf("%s: parsed, want a refusal", name)
		}
	}

	rep := runFixtureRejecting(t, map[string]string{
		"people/ja/jane-doe.json":         testpack.PersonJSON(t, "jane-doe", "Jane Doe"),
		"works/ho/hollow-crown/work.json": testpack.WorkJSON(t, "hollow-crown", "Hollow Crown"),
	}, rs[0])
	if all := rep.Reviewed.All; len(all) != 1 || all[0].Status != statusCensus {
		t.Errorf("tally = %+v, want the person rejection census-only", rep.Reviewed.All)
	}
}
