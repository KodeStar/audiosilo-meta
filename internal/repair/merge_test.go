package repair

import (
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/rawentry"
	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// The calibration merge, end to end: two records of one book, each with a recording
// under the same key, one carrying characters and the other recaps. Everything survives
// on the modeled work, the duplicate's slug is tombstoned, and the tree is left green.
func TestMergeWorksKeepsEverythingAndTombstonesTheSlug(t *testing.T) {
	files := hammeredCluster(t)
	files["works/ha/hammered/characters.json"] = charactersJSON(t, "hammered", "atticus")
	files["works/ha/hammered-book-3/recaps.json"] = recapsJSON(t, "hammered-book-3", 3)
	data := seedTree(t, files)
	before := takeCensus(t, data)

	rep := run(t, Options{DataDir: data, Ops: []string{"merge-works"}, Write: true})
	if len(rep.Applied) != 1 || len(rep.Refused) != 0 {
		t.Fatalf("applied %d, refused %d: %+v / %+v", len(rep.Applied), len(rep.Refused), rep.Applied, rep.Refused)
	}
	if got := rep.Applied[0].Target; got != "hammered" {
		t.Errorf("target = %q, want the modeled work", got)
	}

	// NOTHING IS LOST: the same recordings (one fewer entry, because the two
	// recordings of one production merged), every ASIN, every sidecar entry.
	after := takeCensus(t, data)
	if after.recordings != before.recordings-1 {
		t.Errorf("recordings = %d, want %d (the colliding pair merges into one)", after.recordings, before.recordings-1)
	}
	if !reflect.DeepEqual(after.asins, before.asins) {
		t.Errorf("ASINs changed: %v -> %v", before.asins, after.asins)
	}
	if !reflect.DeepEqual(after.characters, before.characters) || !reflect.DeepEqual(after.recaps, before.recaps) {
		t.Errorf("sidecar content changed: %v/%v -> %v/%v", before.characters, before.recaps, after.characters, after.recaps)
	}

	// The surviving work holds the merged recording with both ASINs.
	recs, err := workEntry(t, data, "hammered").Recordings()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("recordings = %v, want the one merged production", rawentry.SortedKeys(recs))
	}
	rec := recs["luke-daniels-2011"]
	var asins []string
	for _, a := range rec.ASINs() {
		asins = append(asins, a.ASIN)
	}
	slices.Sort(asins)
	if want := []string{"B0KEEPER01", "B0LOSER001"}; !reflect.DeepEqual(asins, want) {
		t.Errorf("merged ASINs = %v, want %v", asins, want)
	}
	if got := rec.Str("work"); got != "hammered" {
		t.Errorf("merged recording work backref = %q, want %q", got, "hammered")
	}
	// The keeper's own facts win; the loser only fills gaps.
	if n, _ := rec.IntAt("runtime_min"); n != 576 {
		t.Errorf("runtime_min = %d, want the keeper's 576", n)
	}
	if len(rec.Sources()) != 2 {
		t.Errorf("sources = %v, want both records' provenance", rec.Sources())
	}

	// The work's own fields: genres unioned and sorted, the loser's subtitle filling
	// a gap, the title left alone.
	w := workEntry(t, data, "hammered")
	if want := []string{"action-adventure", "fantasy"}; !reflect.DeepEqual(w.Strs("genres"), want) {
		t.Errorf("genres = %v, want %v", w.Strs("genres"), want)
	}
	if got := w.Str("subtitle"); got != "An Iron Druid Adventure" {
		t.Errorf("subtitle = %q, want the loser's to have filled the gap", got)
	}
	if got := w.Str("title"); got != "Hammered" {
		t.Errorf("title = %q, want the canonical record's own", got)
	}

	// Both sidecar members now hang off the surviving work, and the loser's entry is
	// gone.
	side := readEntry(t, data, pack.FamilyWorksCommunity, "hammered")
	if !side.Has("characters") || !side.Has("recaps") {
		t.Errorf("works-community entry members = %v, want both", rawentry.SortedKeys(side))
	}
	if entryExists(t, data, pack.FamilyWorksCommunity, "hammered-book-3") {
		t.Error("the loser's works-community entry is still there")
	}
	if entryExists(t, data, pack.FamilyWorks, "hammered-book-3") {
		t.Error("the loser's work entry is still there")
	}

	// The retired slug keeps resolving.
	if got := loadRedirects(t, data)[model.RedirectWorks]["hammered-book-3"]; got != "hammered" {
		t.Errorf("redirect for the retired slug = %q, want %q", got, "hammered")
	}
	if len(rep.PostProblems) != 0 {
		t.Errorf("post-write validation reported problems: %v", rep.PostProblems)
	}
}

// A colliding recording key whose narrators differ is a different production, so it is
// re-keyed through the project's numbered-slug chain and moved intact - never merged,
// and never dropped.
func TestMergeWorksReKeysADifferentProduction(t *testing.T) {
	data := seedTree(t, hammeredCluster(t, withNarrators("other-narrator"), withRuntime(600)))
	before := takeCensus(t, data)

	rep := run(t, Options{DataDir: data, Ops: []string{"merge-works"}, Write: true})
	if len(rep.Applied) != 1 {
		t.Fatalf("applied %d, refused %+v", len(rep.Applied), rep.Refused)
	}
	after := takeCensus(t, data)
	if after.recordings != before.recordings {
		t.Errorf("recordings = %d, want all %d kept", after.recordings, before.recordings)
	}
	recs, err := workEntry(t, data, "hammered").Recordings()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"luke-daniels-2011", "luke-daniels-2011-2"}
	if !reflect.DeepEqual(rawentry.SortedKeys(recs), want) {
		t.Fatalf("recordings = %v, want %v", rawentry.SortedKeys(recs), want)
	}
	moved := recs["luke-daniels-2011-2"]
	if got := moved.Str("id"); got != "luke-daniels-2011-2" {
		t.Errorf("re-keyed recording id = %q, want it to match its map key", got)
	}
	if got := moved.Strs("narrators"); !reflect.DeepEqual(got, []string{"other-narrator"}) {
		t.Errorf("re-keyed recording narrators = %v, want the mover's own", got)
	}
	if !noteMentions(rep.Applied[0].Notes, "re-keyed") {
		t.Errorf("notes do not say the recording was re-keyed: %v", rep.Applied[0].Notes)
	}
}

// Same narrators but a runtime gap over 10% is the importer's own "different production"
// bar, so the colliding key re-keys rather than fusing two productions.
//
// The loser STATES an abridgement, which is what gets the cluster past the audit's
// unexplained-gap veto (an abridgement beside its unabridged twin is one work with two
// productions). The recording pair is then judged here, and the RUNTIME is what decides it:
// sameProduction asks the runtime before the abridged flag.
func TestMergeWorksReKeysOnARuntimeGap(t *testing.T) {
	files := hammeredCluster(t, withRuntime(700), withAbridged(true))
	files["works/ha/hammered/recordings/luke-daniels-2011.json"] = recJSON(t, "luke-daniels-2011", "hammered",
		withNarrators("luke-daniels"), withRuntime(576), withASIN("B0KEEPER01"), withAbridged(false))
	data := seedTree(t, files)
	rep := run(t, Options{DataDir: data, Ops: []string{"merge-works"}, Write: true})
	if len(rep.Applied) != 1 {
		t.Fatalf("applied %d, refused %+v", len(rep.Applied), rep.Refused)
	}
	recs, err := workEntry(t, data, "hammered").Recordings()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Errorf("recordings = %v, want both kept (700 vs 576 is more than 10%%)", rawentry.SortedKeys(recs))
	}
	if !noteMentions(rep.Applied[0].Notes, "more than 10%") {
		t.Errorf("notes do not name the runtime as the reason: %v", rep.Applied[0].Notes)
	}
}

// The BOUNDARY of the runtime rule, which is the importer's own and not a restatement
// of it: within 10 percent OF THE LARGER merges, a hair past it does not. The first
// draft spelled the same rule as "the larger is at most 1.1x the smaller", which admits
// pairs this one refuses - so the boundary is pinned rather than left to the prose.
func TestSameProductionUsesTheImportersRuntimeBoundary(t *testing.T) {
	rec := func(runtime int, opts ...recOpt) entry {
		t.Helper()
		e, err := rawentry.Decode([]byte(recJSON(t, "luke-daniels-2011", "hammered",
			append([]recOpt{withNarrators("luke-daniels"), withRuntime(runtime)}, opts...)...)))
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	for _, tc := range []struct {
		name  string
		a, b  int
		merge bool
	}{
		{"exactly 10% of the larger", 1000, 900, true},
		{"a minute past it", 1000, 899, false},
		// The discriminating pair: within 10% of the LARGER (95 <= 100) but more than
		// 1.1x the SMALLER (995.5 < 1000), which the first draft's spelling re-keyed.
		{"inside the importer's window, outside the first draft's", 1000, 905, true},
		{"one runtime unstated", 0, 900, true},
		{"neither stated", 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			why, same := sameProduction(rec(tc.a), rec(tc.b))
			if same != tc.merge {
				t.Errorf("sameProduction(%d, %d) = %v (%s), want %v", tc.a, tc.b, same, why, tc.merge)
			}
		})
	}
}

// A known-abridged recording never fuses with one that is unabridged OR UNSTATED: the
// importer reads an absent flag as unabridged, and a rule that needed both sides to
// state it would fold an abridgement into an unstated recording.
func TestMergeWorksReKeysOnAnAbridgedContradiction(t *testing.T) {
	for _, tc := range []struct {
		name  string
		keeps []recOpt
	}{
		{"keeper states unabridged", []recOpt{withAbridged(false)}},
		{"keeper states nothing", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := hammeredCluster(t, withRuntime(576), withAbridged(true))
			files["works/ha/hammered/recordings/luke-daniels-2011.json"] = recJSON(t, "luke-daniels-2011", "hammered",
				append([]recOpt{withNarrators("luke-daniels"), withRuntime(576), withASIN("B0KEEPER01")}, tc.keeps...)...)
			data := seedTree(t, files)

			rep := run(t, Options{DataDir: data, Ops: []string{"merge-works"}, Write: true})
			if len(rep.Applied) != 1 {
				t.Fatalf("applied %d, refused %+v", len(rep.Applied), rep.Refused)
			}
			recs, err := workEntry(t, data, "hammered").Recordings()
			if err != nil {
				t.Fatal(err)
			}
			if len(recs) != 2 {
				t.Errorf("recordings = %v, want both kept: an abridgement never folds into a recording that is not one",
					rawentry.SortedKeys(recs))
			}
		})
	}
}

// THE refusal this package exists to make: both halves of a duplicate carry the same
// sidecar member. Nothing is written, and the reason names the collision.
func TestMergeWorksRefusesASidecarMemberCollision(t *testing.T) {
	files := hammeredCluster(t)
	files["works/ha/hammered/characters.json"] = charactersJSON(t, "hammered", "atticus")
	files["works/ha/hammered-book-3/characters.json"] = charactersJSON(t, "hammered-book-3", "atticus-onagain")
	data := seedTree(t, files)
	before := takeCensus(t, data)

	rep := run(t, Options{DataDir: data, Ops: []string{"merge-works"}, Write: true})
	if len(rep.Applied) != 0 {
		t.Fatalf("a cluster with two characters sidecars was merged: %+v", rep.Applied)
	}
	if len(rep.Refused) != 1 || rep.Refused[0].Category != CatSidecarCollision {
		t.Fatalf("refusals = %+v, want one %s", rep.Refused, CatSidecarCollision)
	}
	if !entryExists(t, data, pack.FamilyWorks, "hammered-book-3") {
		t.Error("the refused cluster's loser was deleted anyway")
	}
	if got := takeCensus(t, data); !reflect.DeepEqual(got, before) {
		t.Error("the refused run changed the catalogue")
	}
	if loadRedirects(t, data).Len() != 0 {
		t.Error("the refused run recorded a tombstone")
	}
}

// Two records of one book at different positions in one series are different volumes,
// and the catalogue itself is what says so. The audit vetoes this; the check has to
// exist here too, because a run's own earlier proposals can create the state.
func TestMergeWorksRefusesAPositionConflict(t *testing.T) {
	files := hammeredCluster(t)
	files["series/dr/druid-tales.json"] = seriesJSON(t, "druid-tales", "The Druid Tales",
		"hammered@3", "hammered-book-3@4")
	data := seedTree(t, files)

	// The audit refuses to propose this at all (it is one of its vetoes), so the
	// planner is exercised directly - which is the only way to prove the apply-time
	// check is real rather than unreachable.
	rn, tx := planFixture(t, data)
	err := rn.mergeWorks(tx, mergeFinding("hammered", "hammered-book-3"))
	assertRefusal(t, err, CatPositionConflict, "different volumes")
}

// Two memberships that say the same thing collapse into one when the loser is
// re-pointed, rather than duplicating the target in the list.
func TestMergeWorksDedupesIdenticalMemberships(t *testing.T) {
	files := hammeredCluster(t)
	files["series/dr/druid-tales.json"] = seriesJSON(t, "druid-tales", "The Druid Tales",
		"hammered@3", "hammered-book-3@03")
	data := seedTree(t, files)

	rn, tx := planFixture(t, data)
	if err := rn.mergeWorks(tx, mergeFinding("hammered", "hammered-book-3")); err != nil {
		t.Fatalf("the two spellings of one position were not treated as one slot: %v", err)
	}
	if err := tx.commit("test"); err != nil {
		t.Fatal(err)
	}
	e, ok, err := rn.plan.series.get("druid-tales")
	if err != nil || !ok {
		t.Fatalf("series missing from the plan: %v", err)
	}
	want := []model.SeriesWork{{Work: "hammered", Position: "3"}}
	if got := e.SeriesWorks(); !reflect.DeepEqual(got, want) {
		t.Errorf("memberships = %+v, want %+v", got, want)
	}
}

// A merge onto a work an earlier proposal retired is refused: the record the proposal
// was written against is gone, so nothing about it can be trusted.
func TestASecondProposalNamingARetiredWorkIsRefused(t *testing.T) {
	data := seedTree(t, hammeredCluster(t))
	rn, tx := planFixture(t, data)
	if err := rn.mergeWorks(tx, mergeFinding("hammered", "hammered-book-3")); err != nil {
		t.Fatal(err)
	}
	if err := tx.commit("first"); err != nil {
		t.Fatal(err)
	}
	next := rn.plan.begin()
	err := rn.mergeWorks(next, mergeFinding("hammered-book-3", "hammered"))
	assertRefusal(t, err, CatRetired, "retired by an earlier proposal")
}

// The tombstone table is a one-hop map, so a canonical work that is itself later
// merged repoints the tombstones that named it (pkg/redirects.Add's job, exercised
// through two real merges in one run).
func TestRedirectChainsCollapseWhenACanonicalIsLaterMerged(t *testing.T) {
	data := seedTree(t, hammeredCluster(t))
	rn, tx := planFixture(t, data)
	if err := rn.mergeWorks(tx, mergeFinding("hammered", "hammered-book-3")); err != nil {
		t.Fatal(err)
	}
	if err := tx.commit("first"); err != nil {
		t.Fatal(err)
	}
	// A later wave merges the survivor onto a third record. The tombstone that
	// pointed at it must follow, or resolving the first slug would need two lookups.
	tx2 := rn.plan.begin()
	tx2.redirect(model.RedirectWorks, "hammered", "hammered-omnibus")
	if err := tx2.commit("second"); err != nil {
		t.Fatal(err)
	}
	table := rn.plan.redirects[model.RedirectWorks]
	if table["hammered-book-3"] != "hammered-omnibus" || table["hammered"] != "hammered-omnibus" {
		t.Errorf("tombstones = %v, want both pointing at the final slug", table)
	}
}

// A txn that is DISCARDED leaves the plan exactly as it found it - including the
// derived series index, which is what the proposal after it reads to find out who is in
// which series. An index updated as the plan was composed would survive a refusal and
// hide a membership from the next proposal.
func TestADiscardedTxnLeavesThePlanUntouched(t *testing.T) {
	data := seedTree(t, hammeredCluster(t))
	rn, tx := planFixture(t, data)
	before := rn.plan.seriesNaming("hammered")

	if err := rn.mergeWorks(tx, mergeFinding("hammered", "hammered-book-3")); err != nil {
		t.Fatal(err)
	}
	// The txn is dropped, as a refusal drops it.
	if got := rn.plan.seriesNaming("hammered"); !reflect.DeepEqual(got, before) {
		t.Errorf("series index = %v after a discarded txn, want %v", got, before)
	}
	if _, ok := rn.plan.retiredBy(pack.FamilyWorks, "hammered-book-3"); ok {
		t.Error("a discarded txn retired a record")
	}
	if rn.plan.redirects.Len() != 0 {
		t.Error("a discarded txn recorded a tombstone")
	}
	e, ok, err := rn.plan.works.get("hammered")
	if err != nil || !ok {
		t.Fatalf("the work vanished from the plan: %v", err)
	}
	recs, err := e.Recordings()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Errorf("the plan holds %d recordings for the target, want the one it started with", len(recs))
	}
}

// The union properties, field by field: two records that each state a different fact end
// up stating both, and a fact only the loser states fills the gap.
func TestMergeWorksUnionsEveryListAndFillsEveryGap(t *testing.T) {
	files := hammeredCluster(t, withISBN("9780000000012"))
	files["works/ha/hammered/work.json"] = workJSON(t, "hammered", "Hammered",
		withGenres("fantasy"), withCredits("jane-doe", "editor"), withWorkXref("9780000000001"))
	files["works/ha/hammered/recordings/luke-daniels-2011.json"] = recJSON(t, "luke-daniels-2011", "hammered",
		withNarrators("luke-daniels"), withRuntime(576), withASIN("B0KEEPER01"),
		withISBN("9780000000011"), withoutPublisher())
	files["works/ha/hammered-book-3/work.json"] = workJSON(t, "hammered-book-3", "Hammered: The Druid Tales, Book 3",
		withGenres("action-adventure"), withCredits("john-smith", "contributor", "luke-daniels", "translator"),
		withWorkXref("9780000000002"), withAuthors("jane-doe", "john-smith"))
	files["people/jo/john-smith.json"] = personJSON(t, "john-smith", "John Smith")
	data := seedTree(t, files)

	rep := run(t, Options{DataDir: data, Ops: []string{"merge-works"}, Write: true})
	if len(rep.Applied) != 1 {
		t.Fatalf("applied %+v, refused %+v", rep.Applied, rep.Refused)
	}
	w := workEntry(t, data, "hammered")

	// authors: the identity rule matches NESTED author sets, so a loser may credit
	// somebody the target does not - and that credit has to survive.
	if want := []string{"jane-doe", "john-smith"}; !reflect.DeepEqual(w.Strs("authors"), want) {
		t.Errorf("authors = %v, want %v (the target's order first)", w.Strs("authors"), want)
	}
	// credits: unioned on (person, role) and in the importer's canonical order.
	wantCredits := []model.Credit{
		{Person: "jane-doe", Role: "editor"},
		{Person: "john-smith", Role: "contributor"},
		{Person: "luke-daniels", Role: "translator"},
	}
	if got := w.Credits(); !reflect.DeepEqual(got, wantCredits) {
		t.Errorf("credits = %+v, want %+v", got, wantCredits)
	}
	// xref: the print-ISBN list unions, member by member.
	xref, err := rawentry.Decode(w["xref"])
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"9780000000001", "9780000000002"}; !reflect.DeepEqual(xref.Strs("isbn"), want) {
		t.Errorf("xref.isbn = %v, want %v", xref.Strs("isbn"), want)
	}

	recs, err := w.Recordings()
	if err != nil {
		t.Fatal(err)
	}
	rec := recs["luke-daniels-2011"]
	var isbns []string
	for _, r := range rec.ISBNs() {
		isbns = append(isbns, r.ISBN)
	}
	slices.Sort(isbns)
	if want := []string{"9780000000011", "9780000000012"}; !reflect.DeepEqual(isbns, want) {
		t.Errorf("recording ISBNs = %v, want %v", isbns, want)
	}
	// A field only the mover states fills the keeper's gap.
	if got := rec.Str("publisher"); got != "Fixture Audio" {
		t.Errorf("publisher = %q, want the mover's to have filled the gap", got)
	}
	// added_at is never touched, on either record kind: it says when THIS record entered
	// the database, and the mover's provenance survives in sources[].
	if got := w.Str("added_at"); got != "2026-01-01" {
		t.Errorf("work added_at = %q, want the survivor's own, untouched", got)
	}
}

// One identifier stated in TWO marketplaces is two facts, and a merge keeps both. The
// union keys on the (region, ASIN) pair - exactly pkg/check's own ASIN uniqueness key - so
// it can neither mint a duplicate nor drop a stated region, which a by-value fold did.
func TestMergeWorksKeepsTheSameASINUnderTwoRegions(t *testing.T) {
	files := hammeredCluster(t, withRuntime(600))
	files["works/ha/hammered/recordings/luke-daniels-2011.json"] = testpack.WithField(t,
		recJSON(t, "luke-daniels-2011", "hammered", withNarrators("luke-daniels"), withRuntime(576)),
		"asin", []map[string]string{{"region": "us", "asin": "B0SHARED01"}})
	files["works/ha/hammered-book-3/recordings/luke-daniels-2011.json"] = testpack.WithField(t,
		recJSON(t, "luke-daniels-2011", "hammered-book-3", withNarrators("luke-daniels"), withRuntime(600)),
		"asin", []map[string]string{{"region": "uk", "asin": "B0SHARED01"}})
	data := seedTree(t, files)

	rep := run(t, Options{DataDir: data, Ops: []string{"merge-works"}, Write: true})
	if len(rep.Applied) != 1 {
		t.Fatalf("applied %+v, refused %+v", rep.Applied, rep.Refused)
	}
	recs, err := workEntry(t, data, "hammered").Recordings()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("recordings = %v, want the merged production", rawentry.SortedKeys(recs))
	}
	var pairs []string
	for _, a := range recs["luke-daniels-2011"].ASINs() {
		pairs = append(pairs, a.Region+"/"+a.ASIN)
	}
	slices.Sort(pairs)
	if want := []string{"uk/B0SHARED01", "us/B0SHARED01"}; !reflect.DeepEqual(pairs, want) {
		t.Errorf("ASIN pairs = %v, want %v: a stated region is a fact a merge may not drop", pairs, want)
	}
}

// A chapter list is chosen by CONTENT, not by which side happened to be the keeper: both
// recordings describe the same production, so the longer timeline is strictly more of the
// same truth. Measured over a real wave, 4 of 40 dropped lists were richer than the kept one.
func TestMergeRecordingsKeepsTheRicherChapterList(t *testing.T) {
	for _, tc := range []struct {
		name         string
		keeper, mvr  int
		wantChapters int
	}{
		{name: "the mover is richer", keeper: 3, mvr: 42, wantChapters: 42},
		{name: "the keeper is richer", keeper: 42, mvr: 3, wantChapters: 42},
		{name: "only the mover has any", keeper: 0, mvr: 7, wantChapters: 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := hammeredCluster(t, withRuntime(600))
			keeper := recJSON(t, "luke-daniels-2011", "hammered",
				withNarrators("luke-daniels"), withRuntime(576), withASIN("B0KEEPER01"))
			if tc.keeper > 0 {
				keeper = testpack.WithField(t, keeper, "chapters", chapterList(tc.keeper))
			}
			files["works/ha/hammered/recordings/luke-daniels-2011.json"] = keeper
			files["works/ha/hammered-book-3/recordings/luke-daniels-2011.json"] = testpack.WithField(t,
				recJSON(t, "luke-daniels-2011", "hammered-book-3",
					withNarrators("luke-daniels"), withRuntime(600), withASIN("B0LOSER001")),
				"chapters", chapterList(tc.mvr))
			data := seedTree(t, files)

			rep := run(t, Options{DataDir: data, Ops: []string{"merge-works"}, Write: true})
			if len(rep.Applied) != 1 {
				t.Fatalf("applied %+v, refused %+v", rep.Applied, rep.Refused)
			}
			recs, err := workEntry(t, data, "hammered").Recordings()
			if err != nil {
				t.Fatal(err)
			}
			if got := len(recs["luke-daniels-2011"].Chapters()); got != tc.wantChapters {
				t.Errorf("kept %d chapters, want %d (the richer list)", got, tc.wantChapters)
			}
			// A choice between two lists is reported; taking a list nobody else had is not a
			// choice and needs no note.
			noted := noteMentions(rep.Applied[0].Notes, "chapters: kept")
			if want := tc.keeper > 0; noted != want {
				t.Errorf("chapters note = %v, want %v: %v", noted, want, rep.Applied[0].Notes)
			}
		})
	}
}

// EVERY value a merge could not keep both of is named in the applied record, because that
// record is the audit trail for a pass that deletes records. A wave that silently dropped 48
// cover URLs, 39 publishers and 39 release dates is what this pins.
func TestMergeNotesEveryFactItChoseAway(t *testing.T) {
	files := hammeredCluster(t, withRuntime(600))
	files["works/ha/hammered/recordings/luke-daniels-2011.json"] = testpack.WithField(t,
		recJSON(t, "luke-daniels-2011", "hammered", withNarrators("luke-daniels"), withRuntime(576), withASIN("B0KEEPER01")),
		"cover_url", "https://example.com/keeper.jpg")
	// The mover states a different publisher, release date and cover, and a different QID.
	mover := recJSON(t, "luke-daniels-2011", "hammered-book-3",
		withNarrators("luke-daniels"), withRuntime(600), withASIN("B0LOSER001"))
	mover = testpack.WithField(t, mover, "publisher", "Other Audio")
	mover = testpack.WithField(t, mover, "release_date", "2011-06-01")
	mover = testpack.WithField(t, mover, "cover_url", "https://example.com/other.jpg")
	files["works/ha/hammered-book-3/recordings/luke-daniels-2011.json"] = mover
	files["works/ha/hammered/work.json"] = testpack.WithField(t,
		workJSON(t, "hammered", "Hammered"), "xref", map[string]any{"wikidata": "Q111"})
	files["works/ha/hammered-book-3/work.json"] = testpack.WithField(t,
		workJSON(t, "hammered-book-3", "Hammered: The Druid Tales, Book 3"), "xref", map[string]any{"wikidata": "Q222"})
	data := seedTree(t, files)

	rep := run(t, Options{DataDir: data, Ops: []string{"merge-works"}, Write: true})
	if len(rep.Applied) != 1 {
		t.Fatalf("applied %+v, refused %+v", rep.Applied, rep.Refused)
	}
	notes := strings.Join(rep.Applied[0].Notes, "\n")
	for _, want := range []string{
		`publisher: kept "Fixture Audio", dropped "Other Audio"`,
		`release_date: kept "2020-01-01", dropped "2011-06-01"`,
		`cover_url: kept "https://example.com/keeper.jpg", dropped "https://example.com/other.jpg"`,
		`xref.wikidata: kept "Q111", dropped "Q222"`,
	} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes do not report %q:\n%s", want, notes)
		}
	}
	// The surviving record still states the reviewed record's own values.
	recs, err := workEntry(t, data, "hammered").Recordings()
	if err != nil {
		t.Fatal(err)
	}
	if got := recs["luke-daniels-2011"].Str("publisher"); got != "Fixture Audio" {
		t.Errorf("publisher = %q, want the keeper's", got)
	}
}

// THE SPLIT PAIR MERGES EXACTLY AS THE WHOLE-DATABASE TREE DOES, and this is the
// property the community-repo cutover has to keep: one book, one merge, whichever
// of the two shapes the database is stored in.
//
// The fixture is one cluster whose two halves carry DISJOINT sidecars (characters
// on one, recaps on the other) - the shape a merge is allowed to fold. Run twice:
// as one tree under the default profile, and as a core root plus a read-only
// community root under `core` + --community. The reports must name the same merge
// and the two databases must hold the same records, which for the pair means read
// the way a release reads it (check.LoadComposed, inside takeComposedCensus).
//
// The composed read is the load-bearing half. Under the split the sidecars are NOT
// moved - they are another repository's - so they keep the key this merge just
// retired, and the only reason the pair still composes is the tombstone the merge
// wrote plus the compose-time re-key. If either were missing, this test would fail
// with the pair failing to compose, which is exactly how the failure would reach a
// release.
func TestMergeWorksOverTheSplitPairMatchesTheWholeTree(t *testing.T) {
	fixture := func(t testing.TB) map[string]string {
		files := hammeredCluster(t)
		files["works/ha/hammered/characters.json"] = charactersJSON(t, "hammered", "atticus")
		files["works/ha/hammered-book-3/recaps.json"] = recapsJSON(t, "hammered-book-3", 3)
		return files
	}

	whole := seedTree(t, fixture(t))
	wholeRep := run(t, Options{DataDir: whole, Ops: []string{"merge-works"}, Write: true})

	core, community := seedSplit(t, fixture(t))
	splitRep := run(t, Options{
		DataDir: core, CommunityDir: community, Profile: pack.ProfileCore,
		Ops: []string{"merge-works"}, Write: true,
	})

	for name, rep := range map[string]*Report{"whole tree": wholeRep, "split pair": splitRep} {
		if len(rep.Applied) != 1 || len(rep.Refused) != 0 {
			t.Fatalf("%s: applied %d, refused %d: %+v / %+v", name, len(rep.Applied), len(rep.Refused), rep.Applied, rep.Refused)
		}
	}
	// The DECISION must match. Notes are compared separately below: they are
	// deliberately different, because under the split the sidecars did not move.
	got, want := splitRep.Applied[0], wholeRep.Applied[0]
	got.Notes, want.Notes = nil, nil
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the two shapes applied different changes:\nwhole: %+v\nsplit: %+v", want, got)
	}
	if !noteMentions(splitRep.Applied[0].Notes, "ride the slug redirect") {
		t.Errorf("the split run does not say the sidecars stayed put: %v", splitRep.Applied[0].Notes)
	}
	if noteMentions(wholeRep.Applied[0].Notes, "ride the slug redirect") {
		t.Errorf("the whole-tree run claims sidecars stayed put, but it moved them: %v", wholeRep.Applied[0].Notes)
	}

	// The DATABASES must match, read whole either way.
	if a, b := takeCensus(t, whole), takeComposedCensus(t, core, community); !reflect.DeepEqual(a, b) {
		t.Errorf("the two shapes left different databases:\nwhole: %+v\nsplit: %+v", a, b)
	}
	// And the community checkout is untouched: this pass may not write another
	// repository, whatever its plan staged in order to answer questions.
	if entryExists(t, core, pack.FamilyWorks, "hammered-book-3") {
		t.Error("the core merge did not retire the loser")
	}
	if !communityHolds(t, community, "hammered-book-3") {
		t.Error("the community entry was rewritten by a core-side repair")
	}
}

// WITHOUT --community, A CORE RUN REFUSES EVERY MERGE. It cannot see the sidecars,
// so it cannot run the collision guard, so it does not merge - the refusal is
// unconditional and names the flag, because "either side might carry a member" is
// true of every cluster and inferring anything from silence is what made the guard
// blind in the first place.
func TestMergeWorksUnderCoreRefusesWithoutTheCommunityRoot(t *testing.T) {
	files := hammeredCluster(t)
	files["works/ha/hammered/characters.json"] = charactersJSON(t, "hammered", "atticus")
	core, community := seedSplit(t, files)
	before := takeComposedCensus(t, core, community)

	rep := run(t, Options{DataDir: core, Profile: pack.ProfileCore, Ops: []string{"merge-works"}, Write: true})
	if len(rep.Applied) != 0 {
		t.Fatalf("a merge was applied without the community layer in view: %+v", rep.Applied)
	}
	if len(rep.Refused) != 1 || rep.Refused[0].Category != CatCommunityRequired {
		t.Fatalf("refusals = %+v, want one %s", rep.Refused, CatCommunityRequired)
	}
	if !strings.Contains(rep.Refused[0].Reason, "--community") {
		t.Errorf("the refusal does not name the flag that fixes it: %q", rep.Refused[0].Reason)
	}
	if !entryExists(t, core, pack.FamilyWorks, "hammered-book-3") {
		t.Error("the refused cluster's loser was deleted anyway")
	}
	if loadRedirects(t, core).Len() != 0 {
		t.Error("the refused run recorded a tombstone")
	}
	if got := takeComposedCensus(t, core, community); !reflect.DeepEqual(got, before) {
		t.Error("the refused run changed the database")
	}
}

// WITH --community, the collision refusal works exactly as it did before the split:
// both halves carrying a `characters` member is a human decision, and the run says
// so instead of folding one away. This is the whole point of the flag - the
// previous test's refusal is safety, this one is the guard actually doing its job.
func TestMergeWorksUnderCoreRefusesTheCollisionThroughTheCommunityRoot(t *testing.T) {
	files := hammeredCluster(t)
	files["works/ha/hammered/characters.json"] = charactersJSON(t, "hammered", "atticus")
	files["works/ha/hammered-book-3/characters.json"] = charactersJSON(t, "hammered-book-3", "atticus-onagain")
	core, community := seedSplit(t, files)
	before := takeComposedCensus(t, core, community)

	rep := run(t, Options{
		DataDir: core, CommunityDir: community, Profile: pack.ProfileCore,
		Ops: []string{"merge-works"}, Write: true,
	})
	if len(rep.Applied) != 0 {
		t.Fatalf("a cluster with two characters sidecars was merged: %+v", rep.Applied)
	}
	if len(rep.Refused) != 1 || rep.Refused[0].Category != CatSidecarCollision {
		t.Fatalf("refusals = %+v, want one %s", rep.Refused, CatSidecarCollision)
	}
	if !entryExists(t, core, pack.FamilyWorks, "hammered-book-3") {
		t.Error("the refused cluster's loser was deleted anyway")
	}
	if got := takeComposedCensus(t, core, community); !reflect.DeepEqual(got, before) {
		t.Error("the refused run changed the database")
	}
}

// A --community root that is not a community checkout is refused at the door, not
// read as "no sidecars anywhere". Pointing the flag at the community repository's
// top level rather than its data/ is the mistake it invites, and answering every
// cluster "nothing to collide with" would be the original blindness wearing the
// flag that was supposed to end it.
func TestCommunityRootMustHoldTheLayer(t *testing.T) {
	files := hammeredCluster(t)
	files["works/ha/hammered/characters.json"] = charactersJSON(t, "hammered", "atticus")
	core, community := seedSplit(t, files)

	_, err := Run(Options{
		DataDir: core, CommunityDir: filepath.Dir(community), Profile: pack.ProfileCore,
		Ops: []string{"merge-works"},
	})
	if err == nil {
		t.Fatal("a --community root holding no works-community packs was accepted")
	}
	if !strings.Contains(err.Error(), "no works-community packs") {
		t.Errorf("the error does not name the problem: %v", err)
	}
}

// --community over a tree that ALREADY holds the family is two answers to one
// question, which is not a mode this pass has.
func TestCommunityRootIsRefusedOnAWholeDatabaseTree(t *testing.T) {
	files := hammeredCluster(t)
	files["works/ha/hammered/characters.json"] = charactersJSON(t, "hammered", "atticus")
	core, community := seedSplit(t, files)

	_, err := Run(Options{DataDir: core, CommunityDir: community, Ops: []string{"merge-works"}})
	if err == nil {
		t.Fatal("--community was accepted under the default (whole-database) profile")
	}
	if !strings.Contains(err.Error(), "already holds the works-community family") {
		t.Errorf("the error does not name the conflict: %v", err)
	}
}

// chapterList renders n chapters, monotonic from zero as pkg/check requires.
func chapterList(n int) []map[string]any {
	out := make([]map[string]any, 0, n)
	for i := range n {
		out = append(out, map[string]any{
			"title": fmt.Sprintf("Chapter %d", i+1), "start_ms": i * 600000, "length_ms": 600000,
		})
	}
	return out
}

func noteMentions(notes []string, want string) bool {
	for _, n := range notes {
		if strings.Contains(n, want) {
			return true
		}
	}
	return false
}
