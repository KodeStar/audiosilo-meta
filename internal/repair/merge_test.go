package repair

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// The calibration merge, end to end: two records of one book, each with a recording
// under the same key, one carrying characters and the other recaps. Everything survives
// on the modeled work, the duplicate's slug is tombstoned, and the tree is left green.
func TestMergeWorksKeepsEverythingAndTombstonesTheSlug(t *testing.T) {
	files := hammeredCluster(t)
	files["works/ha/hammered/characters.json"] = charactersJSON(t, "hammered", "atticus")
	files["works/ha/hammered-book-3/recaps.json"] = recapsJSON(t, "hammered-book-3")
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
	recs, err := workEntry(t, data, "hammered").recordings()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("recordings = %v, want the one merged production", sortedKeys(recs))
	}
	rec := recs["luke-daniels-2011"]
	var asins []string
	for _, a := range rec.asins() {
		asins = append(asins, a.ASIN)
	}
	slices.Sort(asins)
	if want := []string{"B0KEEPER01", "B0LOSER001"}; !reflect.DeepEqual(asins, want) {
		t.Errorf("merged ASINs = %v, want %v", asins, want)
	}
	if got := rec.str("work"); got != "hammered" {
		t.Errorf("merged recording work backref = %q, want %q", got, "hammered")
	}
	// The keeper's own facts win; the loser only fills gaps.
	if n, _ := rec.intAt("runtime_min"); n != 576 {
		t.Errorf("runtime_min = %d, want the keeper's 576", n)
	}
	if len(rec.sources()) != 2 {
		t.Errorf("sources = %v, want both records' provenance", rec.sources())
	}

	// The work's own fields: genres unioned and sorted, the loser's subtitle filling
	// a gap, the title left alone.
	w := workEntry(t, data, "hammered")
	if want := []string{"action-adventure", "fantasy"}; !reflect.DeepEqual(w.strs("genres"), want) {
		t.Errorf("genres = %v, want %v", w.strs("genres"), want)
	}
	if got := w.str("subtitle"); got != "An Iron Druid Adventure" {
		t.Errorf("subtitle = %q, want the loser's to have filled the gap", got)
	}
	if got := w.str("title"); got != "Hammered" {
		t.Errorf("title = %q, want the canonical record's own", got)
	}

	// Both sidecar members now hang off the surviving work, and the loser's entry is
	// gone.
	side := readEntry(t, data, pack.FamilyWorksCommunity, "hammered")
	if !side.has("characters") || !side.has("recaps") {
		t.Errorf("works-community entry members = %v, want both", sortedKeys(side))
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
	recs, err := workEntry(t, data, "hammered").recordings()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"luke-daniels-2011", "luke-daniels-2011-2"}
	if !reflect.DeepEqual(sortedKeys(recs), want) {
		t.Fatalf("recordings = %v, want %v", sortedKeys(recs), want)
	}
	moved := recs["luke-daniels-2011-2"]
	if got := moved.str("id"); got != "luke-daniels-2011-2" {
		t.Errorf("re-keyed recording id = %q, want it to match its map key", got)
	}
	if got := moved.strs("narrators"); !reflect.DeepEqual(got, []string{"other-narrator"}) {
		t.Errorf("re-keyed recording narrators = %v, want the mover's own", got)
	}
	if !noteMentions(rep.Applied[0].Notes, "re-keyed") {
		t.Errorf("notes do not say the recording was re-keyed: %v", rep.Applied[0].Notes)
	}
}

// Same narrators but a runtime gap over 10% is the importer's own "different
// production" bar, so it re-keys too rather than fusing two productions.
func TestMergeWorksReKeysOnARuntimeGap(t *testing.T) {
	data := seedTree(t, hammeredCluster(t, withRuntime(700)))
	rep := run(t, Options{DataDir: data, Ops: []string{"merge-works"}, Write: true})
	if len(rep.Applied) != 1 {
		t.Fatalf("applied %d, refused %+v", len(rep.Applied), rep.Refused)
	}
	recs, err := workEntry(t, data, "hammered").recordings()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Errorf("recordings = %v, want both kept (700 vs 576 is more than 10%%)", sortedKeys(recs))
	}
}

// A known-abridged recording never fuses with an unabridged one, even at the same
// runtime and with the same narrator.
func TestMergeWorksReKeysOnAnAbridgedContradiction(t *testing.T) {
	files := hammeredCluster(t, withRuntime(576), withAbridged(true))
	files["works/ha/hammered/recordings/luke-daniels-2011.json"] = recJSON(t, "luke-daniels-2011", "hammered",
		withNarrators("luke-daniels"), withRuntime(576), withASIN("B0KEEPER01"), withAbridged(false))
	data := seedTree(t, files)

	rep := run(t, Options{DataDir: data, Ops: []string{"merge-works"}, Write: true})
	if len(rep.Applied) != 1 {
		t.Fatalf("applied %d, refused %+v", len(rep.Applied), rep.Refused)
	}
	recs, err := workEntry(t, data, "hammered").recordings()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Errorf("recordings = %v, want both kept: one states abridged and the other does not", sortedKeys(recs))
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
	if len(rep.Refused) != 1 || rep.Refused[0].Category != catSidecarCollision {
		t.Fatalf("refusals = %+v, want one %s", rep.Refused, catSidecarCollision)
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
	assertRefusal(t, err, catPositionConflict, "different volumes")
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
	if got := e.seriesWorks(); !reflect.DeepEqual(got, want) {
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
	assertRefusal(t, err, catRetired, "retired by an earlier proposal")
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
	recs, err := e.recordings()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Errorf("the plan holds %d recordings for the target, want the one it started with", len(recs))
	}
}

// The unaddressable-title veto: two different books whose cleaned titles reduce to the
// same comparison key only because the key folds away everything non-ASCII.
func TestMergeWorksRefusesTitlesThatCarryNoIdentity(t *testing.T) {
	data := seedTree(t, hammeredCluster(t))
	rn, tx := planFixture(t, data)
	fd := mergeFinding("hammered", "hammered-book-3")
	fd.Works[0].Cleaned = "Грани безумия. Том 1"
	fd.Works[1].Cleaned = "Том 1"
	err := rn.mergeWorks(tx, fd)
	assertRefusal(t, err, catUnaddressableTitle, "meeting on a number")
}

// ...and the same regime with IDENTICAL cleaned titles is a real duplicate ("1984"
// against "1984"), so it is not refused.
func TestMergeWorksAllowsIdenticalTitlesThatCarryNoIdentity(t *testing.T) {
	data := seedTree(t, hammeredCluster(t))
	rn, tx := planFixture(t, data)
	fd := mergeFinding("hammered", "hammered-book-3")
	fd.Works[0].Cleaned = "1984"
	fd.Works[1].Cleaned = "1984"
	if err := rn.mergeWorks(tx, fd); err != nil {
		t.Fatalf("a real duplicate whose title is a number was refused: %v", err)
	}
}

// The union properties, field by field: two records that each state a different fact
// end up stating both, and a fact only the loser states fills the gap.
func TestMergeWorksUnionsEveryListAndFillsEveryGap(t *testing.T) {
	files := hammeredCluster(t, withISBN("9780000000012"))
	files["works/ha/hammered/work.json"] = workJSON(t, "hammered", "Hammered",
		withGenres("fantasy"), withCredits("jane-doe", "editor"), withWorkXref("9780000000001"))
	files["works/ha/hammered/recordings/luke-daniels-2011.json"] = recJSON(t, "luke-daniels-2011", "hammered",
		withNarrators("luke-daniels"), withRuntime(576), withASIN("B0KEEPER01"),
		withISBN("9780000000011"), withoutPublisher())
	files["works/ha/hammered-book-3/work.json"] = workJSON(t, "hammered-book-3", "Hammered: The Druid Tales, Book 3",
		withGenres("action-adventure"), withCredits("john-smith", "contributor", "luke-daniels", "translator"), withWorkXref("9780000000002"),
		withAuthors("jane-doe", "john-smith"))
	files["people/jo/john-smith.json"] = personJSON(t, "john-smith", "John Smith")
	data := seedTree(t, files)

	rep := run(t, Options{DataDir: data, Ops: []string{"merge-works"}, Write: true})
	if len(rep.Applied) != 1 {
		t.Fatalf("applied %+v, refused %+v", rep.Applied, rep.Refused)
	}
	w := workEntry(t, data, "hammered")

	// authors: the identity rule matches NESTED author sets, so a loser may credit
	// somebody the target does not - and that credit has to survive.
	if want := []string{"jane-doe", "john-smith"}; !reflect.DeepEqual(w.strs("authors"), want) {
		t.Errorf("authors = %v, want %v (the target's order first)", w.strs("authors"), want)
	}
	// credits: unioned on (person, role) and in the importer's canonical order.
	wantCredits := []model.Credit{
		{Person: "jane-doe", Role: "editor"},
		{Person: "john-smith", Role: "contributor"},
		{Person: "luke-daniels", Role: "translator"},
	}
	if got := w.credits(); !reflect.DeepEqual(got, wantCredits) {
		t.Errorf("credits = %+v, want %+v", got, wantCredits)
	}
	// xref: the print-ISBN list unions, member by member.
	xref, err := decodeEntry(w["xref"])
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"9780000000001", "9780000000002"}; !reflect.DeepEqual(xref.strs("isbn"), want) {
		t.Errorf("xref.isbn = %v, want %v", xref.strs("isbn"), want)
	}

	recs, err := w.recordings()
	if err != nil {
		t.Fatal(err)
	}
	rec := recs["luke-daniels-2011"]
	var isbns []string
	for _, r := range rec.isbns() {
		isbns = append(isbns, r.ISBN)
	}
	slices.Sort(isbns)
	if want := []string{"9780000000011", "9780000000012"}; !reflect.DeepEqual(isbns, want) {
		t.Errorf("recording ISBNs = %v, want %v", isbns, want)
	}
	// A field only the mover states fills the keeper's gap.
	if got := rec.str("publisher"); got != "Fixture Audio" {
		t.Errorf("publisher = %q, want the mover's to have filled the gap", got)
	}
	// added_at is never touched, on either record kind: it says when THIS record
	// entered the database, and the mover's provenance survives in sources[].
	if got := w.str("added_at"); got != "2026-01-01" {
		t.Errorf("work added_at = %q, want the survivor's own, untouched", got)
	}
}

func noteMentions(notes []string, want string) bool {
	for _, n := range notes {
		if strings.Contains(n, want) {
			return true
		}
	}
	return false
}
