package check

import (
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// reviewed_test.go pins how the duplicate advisories honour a reviewed "not one
// record" decision (LoadOptions.Distinct): the pair it covers is gone, everything
// beside it is not, and the census says how many findings the review withheld.

// reviewedLoad loads files with the given decisions, failing on any problem.
func reviewedLoad(t *testing.T, files map[string]string, ds ...ReviewedDistinct) Result {
	t.Helper()
	dir := t.TempDir()
	writeEntities(t, dir, files)
	res := LoadProfileWith(dir, pack.ProfileAll, LoadOptions{Distinct: ds})
	if !res.OK() {
		t.Fatalf("a reviewed fixture must still validate: %v", res.Problems)
	}
	return res
}

func distinctWorks(ids ...string) ReviewedDistinct {
	return ReviewedDistinct{Kind: model.RedirectWorks, IDs: ids}
}

// identityTriple is baseValid with two more "Book One" records, each listing
// author-one plus one more person: both pair with book-one as an author subset,
// and not with each other (their lists only overlap).
func identityTriple() map[string]string {
	files := baseValid()
	for _, id := range []string{"translator-one", "translator-two"} {
		files["people/tr/"+id+".json"] = `{"id":"` + id + `","license":"CC0-1.0",` +
			`"name":"` + strings.ToUpper(id[:1]) + strings.ReplaceAll(id[1:], "-", " ") + `","sources":[{"type":"user"}]}`
	}
	for id, extra := range map[string]string{"book-one-author-one": "translator-one", "book-one-translator-two": "translator-two"} {
		files["works/bo/"+id+"/work.json"] = `{"authors":["author-one","` + extra + `"],"id":"` + id + `",` +
			`"language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Book One"}`
	}
	return files
}

// A rejected identity-equal pair is not reported; the other pair of the same title
// group still is, and the census counts the one it withheld.
func TestReviewedIdentityEqualPairIsWithheld(t *testing.T) {
	const marker = "one book under two ids"
	if got := advisoryMatching(problemLines(reviewedLoad(t, identityTriple()).Warnings), marker); len(got) != 2 {
		t.Fatalf("unreviewed: identity-equal advisories = %v, want both subset pairs", got)
	}

	res := reviewedLoad(t, identityTriple(), distinctWorks("book-one-author-one", "book-one"))
	got := advisoryMatching(problemLines(res.Warnings), marker)
	if len(got) != 1 || !strings.Contains(got[0], "book-one-translator-two") || strings.Contains(got[0], "book-one-author-one") {
		t.Errorf("reviewed: identity-equal advisories = %v, want only the unreviewed pair", got)
	}
	if res.Suppressed != 1 {
		t.Errorf("Suppressed = %d, want 1", res.Suppressed)
	}
}

// normalizedTriple is three spellings of one book, every pair of them a
// normalized-identity collision.
func normalizedTriple() map[string]string {
	files := dupBase("Hollow Crown")
	for id, title := range map[string]string{
		"hollow-crown-a-dark-fantasy-adventure": "Hollow Crown: A Dark Fantasy Adventure",
		"hollow-crown-unabridged":               "Hollow Crown (Unabridged)",
	} {
		files["works/"+id[:2]+"/"+id+"/work.json"] = `{"authors":["author-one"],"id":"` + id + `",` +
			`"language":"en","license":"CC0-1.0","sources":[{"type":"libex-import"}],"title":"` + title + `"}`
	}
	return files
}

// A group drops a pair, not a member: a member still pairing with someone stays in
// the group, a member left pairing with nobody drops out, and a group left with
// fewer than two members is no finding at all.
func TestReviewedNormalizedGroupDropsOnlyTheReviewedPairs(t *testing.T) {
	const (
		adventure  = "hollow-crown-a-dark-fantasy-adventure"
		unabridged = "hollow-crown-unabridged"
	)
	group := func(res Result) []string {
		t.Helper()
		return advisoryMatching(problemLines(res.Warnings), normalizedDupMarker)
	}
	if got := group(reviewedLoad(t, normalizedTriple())); len(got) != 1 ||
		!strings.Contains(got[0], adventure) || !strings.Contains(got[0], unabridged) {
		t.Fatalf("unreviewed: %v, want one group of all three", got)
	}

	// One pair reviewed: both of its members still pair with the third, so the group
	// is unchanged - the review spoke about that pair only.
	res := reviewedLoad(t, normalizedTriple(), distinctWorks(adventure, unabridged))
	if got := group(res); len(got) != 1 || !strings.Contains(got[0], adventure) || !strings.Contains(got[0], unabridged) {
		t.Errorf("one pair reviewed: %v, want the group of all three", got)
	}
	if res.Suppressed != 1 {
		t.Errorf("one pair reviewed: Suppressed = %d, want 1", res.Suppressed)
	}

	// Every pair naming the adventure reviewed: it pairs with nobody and drops out,
	// while book-one and the unabridged spelling remain one finding.
	res = reviewedLoad(t, normalizedTriple(), distinctWorks("book-one", adventure), distinctWorks(adventure, unabridged))
	got := group(res)
	if len(got) != 1 || strings.Contains(got[0], adventure) || !strings.Contains(got[0], unabridged) {
		t.Errorf("one member reviewed away: %v, want book-one and %s alone", got, unabridged)
	}
	if res.Suppressed != 2 {
		t.Errorf("one member reviewed away: Suppressed = %d, want 2", res.Suppressed)
	}

	// The whole group reviewed in one decision: nothing is left to report.
	res = reviewedLoad(t, normalizedTriple(), distinctWorks(unabridged, "book-one", adventure))
	if got := group(res); len(got) != 0 {
		t.Errorf("whole group reviewed: %v, want nothing", got)
	}
	if res.Suppressed != 3 {
		t.Errorf("whole group reviewed: Suppressed = %d, want 3 (every pair)", res.Suppressed)
	}
}

// A decision naming a slug a later merge RETIRED still covers the pair: the load
// reads the review through the tree's own tombstone table, so a repair wave does
// not resurrect a finding a maintainer already ruled on.
func TestReviewedDecisionFollowsARetiredSlug(t *testing.T) {
	files := normalizedTriple()
	delete(files, "works/ho/hollow-crown-unabridged/work.json")
	files["redirects.json"] = `{"people":{},"series":{},"works":{"hollow-crown-old":"hollow-crown-a-dark-fantasy-adventure"}}`

	res := reviewedLoad(t, files, distinctWorks("book-one", "hollow-crown-old"))
	if got := advisoryMatching(problemLines(res.Warnings), normalizedDupMarker); len(got) != 0 {
		t.Errorf("a review naming the retired slug did not cover its survivor: %v", got)
	}
	if res.Suppressed != 1 {
		t.Errorf("Suppressed = %d, want 1", res.Suppressed)
	}

	// Two members of one review since merged into one record are one record, not a
	// pair - and a decision naming nothing live covers nothing else.
	res = reviewedLoad(t, files, distinctWorks("hollow-crown-old", "hollow-crown-a-dark-fantasy-adventure"))
	if got := advisoryMatching(problemLines(res.Warnings), normalizedDupMarker); len(got) != 1 {
		t.Errorf("a review collapsing onto one record withheld %v", got)
	}
}

// A decision is read in its OWN namespace: a series rejection never covers two works
// of the same slugs, and a `merge-series` rejection withholds a duplicate series.
func TestReviewedDuplicateSeriesIsWithheld(t *testing.T) {
	files := seriesFixture(map[string]string{
		"series-one-abridged": seriesBody("series-one-abridged", "Series One (abridged)", "", "book-one@1", "book-two@2"),
	})
	res := reviewedLoad(t, files, distinctWorks("series-one", "series-one-abridged"))
	if got := advisoryMatching(problemLines(res.Warnings), duplicateSeriesMarker); len(got) != 1 || res.Suppressed != 0 {
		t.Errorf("a works decision withheld a series finding: %v (suppressed %d)", got, res.Suppressed)
	}

	res = reviewedLoad(t, files, ReviewedDistinct{Kind: model.RedirectSeries, IDs: []string{"series-one-abridged", "series-one"}})
	if got := advisoryMatching(problemLines(res.Warnings), duplicateSeriesMarker); len(got) != 0 {
		t.Errorf("a reviewed duplicate series was still reported: %v", got)
	}
	if res.Suppressed != 1 {
		t.Errorf("Suppressed = %d, want 1", res.Suppressed)
	}
	// The recording-qualifier advisory has no pair to review: it stays.
	if got := advisoryMatching(problemLines(res.Warnings), recordingQualifierMarker); len(got) != 1 {
		t.Errorf("series-recording-qualifier = %v, want it untouched by a review", got)
	}
}

// The census line says how many findings a review withheld, so a smaller count is
// never a silent one.
func TestPrintAdvisoriesStatesTheSuppressedCount(t *testing.T) {
	var b strings.Builder
	PrintAdvisories(&b, reviewedLoad(t, identityTriple(), distinctWorks("book-one", "book-one-author-one")))
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	census := lines[len(lines)-1]
	if !strings.HasPrefix(census, "advisory classes: ") || !strings.HasSuffix(census, "(1 reviewed and suppressed)") {
		t.Errorf("census line = %q, want the suppressed count at its end", census)
	}

	b.Reset()
	PrintAdvisories(&b, Result{Suppressed: 2})
	if got := strings.TrimSpace(b.String()); got != "advisory classes: none (2 reviewed and suppressed)" {
		t.Errorf("a census with every finding reviewed away = %q", got)
	}

	b.Reset()
	PrintAdvisories(&b, reviewedLoad(t, identityTriple()))
	if strings.Contains(b.String(), "suppressed") {
		t.Errorf("a load withholding nothing mentioned suppression:\n%s", b.String())
	}
}

// AdvisoryKey drops the pack file and keeps the family, the entry and the message,
// so a record moving between packs keeps its key.
func TestAdvisoryKeyIgnoresThePackFile(t *testing.T) {
	msg := `work "a" and "b" have the same title and the same identity authors: one book under two ids`
	a := AdvisoryKey(Problem{Path: "works/0/0.json: entry a", Msg: msg})
	b := AdvisoryKey(Problem{Path: "works/m/moved.json: entry a", Msg: msg})
	if a != b {
		t.Errorf("a pack move changed the key: %q vs %q", a, b)
	}
	if want := "identity-equal-works\tworks: entry a: " + msg; a != want {
		t.Errorf("key = %q, want %q", a, want)
	}
	if got := AdvisoryKey(Problem{Path: "series/0.json: entry a", Msg: msg}); got == a {
		t.Errorf("two families shared a key: %q", got)
	}
	got := AdvisoryKey(Problem{Path: "people/0.json: entry p", Msg: "an orphan\trecord\nsplit"})
	if strings.Count(got, "\t") != 1 || strings.Contains(got, "\n") {
		t.Errorf("a key must be one line with one tab: %q", got)
	}
}

// A `merge-people` rejection withholds an honorific pair - two different people
// whose ids read as a duplicate - and is read in the people namespace alone.
func TestReviewedHonorificPairIsWithheld(t *testing.T) {
	const marker = "only by a courtesy title"
	files := baseValid()
	for id, name := range map[string]string{
		"dr-arthur-conan-doyle": "Dr. Arthur Conan Doyle", "arthur-conan-doyle": "Arthur Conan Doyle",
		"sir-richard-burton": "Sir Richard Burton", "richard-burton": "Richard Burton",
	} {
		files["people/"+id[:2]+"/"+id+".json"] = `{"id":"` + id + `","license":"CC0-1.0","name":"` + name + `","sources":[{"type":"user"}]}`
	}
	people := func(ids ...string) ReviewedDistinct { return ReviewedDistinct{Kind: model.RedirectPeople, IDs: ids} }

	res := reviewedLoad(t, files, distinctWorks("sir-richard-burton", "richard-burton"))
	if got := advisoryMatching(problemLines(res.Warnings), marker); len(got) != 2 || res.Suppressed != 0 {
		t.Errorf("a works decision withheld a person pair: %v (suppressed %d)", got, res.Suppressed)
	}
	res = reviewedLoad(t, files, people("richard-burton", "sir-richard-burton"))
	got := advisoryMatching(problemLines(res.Warnings), marker)
	if len(got) != 1 || !strings.Contains(got[0], "dr-arthur-conan-doyle") || res.Suppressed != 1 {
		t.Errorf("reviewed person pair: %v (suppressed %d), want only the unreviewed pair and 1 suppressed", got, res.Suppressed)
	}
}
