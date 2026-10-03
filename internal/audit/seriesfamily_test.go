package audit

import (
	"slices"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
)

// withOrdering states an ordering on a rendered series that is no variant.
func withOrdering(t testing.TB, series, ordering string) string {
	t.Helper()
	return withField(t, series, "ordering", ordering)
}

// dragonFamily is an ordering family over four works: the publication-order primary
// (decorated with the qualifier its own field states) and a chronological variant.
func dragonFamily(t testing.TB) map[string]string {
	t.Helper()
	return map[string]string{
		"series/dh/dh-pub.json": withOrdering(t, seriesJSON(t, "dh-pub", "Dragon Heart (Publication Order)",
			"one@1", "two@2", "three@3", "four@4"), "publication"),
		"series/dh/dh-chrono.json": orderingVariant(t, seriesJSON(t, "dh-chrono", "Dragon Heart (Chronological Order)",
			"three@1", "one@2", "two@3", "four@4"), "chronological", "dh-pub"),
	}
}

// familyRecords is every SER-DUP record of one family subclass naming loser.
func familyRecords(t testing.TB, rep *Report, subclass, loser string) []Finding {
	t.Helper()
	var out []Finding
	for _, fd := range subclassOf(t, rep, ClassSeriesDup, subclass) {
		if slices.Equal(fd.Propose.Others, []string{loser}) {
			out = append(out, fd)
		}
	}
	return out
}

// oneFamilyRecord is the one record of subclass naming loser.
func oneFamilyRecord(t testing.TB, rep *Report, subclass, loser string) Finding {
	t.Helper()
	got := familyRecords(t, rep, subclass, loser)
	if len(got) != 1 {
		t.Fatalf("want one %s record for %s, got %d: %+v", subclass, loser, len(got), classOf(t, rep, ClassSeriesDup))
	}
	return got[0]
}

// A plain spelling repeating the PRIMARY's list slot for slot folds onto it mechanically,
// inside a group the ordering family keeps review-only as a whole. The primary's
// "(Publication Order)" says nothing its own ordering field does not, so the decoration
// rule stands down for the pair; the whole group's record is untouched.
func TestFamilySpellingFoldsPlainListOntoMatchingOrdering(t *testing.T) {
	rep := runFixture(t, seriesFixture(t, []string{"one", "two", "three", "four"}, mergeFiles(dragonFamily(t), map[string]string{
		"series/dh/dh.json": seriesJSON(t, "dh", "Dragon Heart", "one@1", "two@2", "three@3", "four@4"),
	})))
	assertProposalsConsistent(t, rep)
	assertVetoed(t, serDupMerge(t, rep), "two orderings of the franchise")
	fd := oneFamilyRecord(t, rep, SubclassFamilySpelling, "dh")
	assertMechanical(t, fd)
	if fd.Propose.Target != "dh-pub" || fd.Key != "dragonheart/dh" {
		t.Errorf("record = %s %+v, want dh folded onto the primary dh-pub under dragonheart/dh", fd.Key, fd.Propose)
	}
}

// Onto a VARIANT the same fold is advisory: the importer reaches a variant only under its
// stated ordering, so the retired plain name would reach it only through the tombstone.
func TestFamilySpellingOntoAVariantIsAdvisory(t *testing.T) {
	rep := runFixture(t, seriesFixture(t, []string{"one", "two", "three", "four"}, mergeFiles(dragonFamily(t), map[string]string{
		"series/dh/dh.json": seriesJSON(t, "dh", "Dragon Heart", "three@1", "one@2", "two@3", "four@4"),
	})))
	assertProposalsConsistent(t, rep)
	fd := oneFamilyRecord(t, rep, SubclassFamilySpelling, "dh")
	if fd.Propose.Op != OpMergeSeries || fd.Propose.Target != "dh-chrono" {
		t.Fatalf("proposal = %+v, want dh folded onto the variant dh-chrono", fd.Propose)
	}
	assertVetoed(t, fd, "reading-order VARIANT")
	assertVetoed(t, fd, "slug tombstone")
}

// Before the work merges land, the two lists disagree at a slot a W-DUP cluster holds:
// the member sharing the most same-slot works is named in a REVIEW carrying the veto and
// the cluster, never a mechanical fold in the same run as the merge that would unblock it.
func TestFamilySpellingIsReviewWhileAWorkDuplicateHoldsTheSlot(t *testing.T) {
	files := seriesFixture(t, []string{"one", "three", "four"}, map[string]string{
		"works/dr/dragonfire/work.json":                    workJSON(t, "dragonfire", "Dragonfire"),
		"works/dr/dragonfire/recordings/d.json":            recJSON(t, "d", "dragonfire"),
		"works/dr/dragonfire-unabridged/work.json":         workJSON(t, "dragonfire-unabridged", "Dragonfire (Unabridged)"),
		"works/dr/dragonfire-unabridged/recordings/u.json": recJSON(t, "u", "dragonfire-unabridged"),
		"series/dh/dh-pub.json": withOrdering(t, seriesJSON(t, "dh-pub", "Dragon Heart (Publication Order)",
			"one@1", "dragonfire@2", "three@3", "four@4"), "publication"),
		"series/dh/dh-chrono.json": orderingVariant(t, seriesJSON(t, "dh-chrono", "Dragon Heart (Chronological Order)",
			"three@1", "one@2", "dragonfire@3", "four@4"), "chronological", "dh-pub"),
		"series/dh/dh.json": seriesJSON(t, "dh", "Dragon Heart", "one@1", "dragonfire-unabridged@2", "three@3", "four@4"),
	})
	rep := runFixture(t, files)
	assertProposalsConsistent(t, rep)
	fd := oneFamilyRecord(t, rep, SubclassFamilySpelling, "dh")
	if fd.Propose.Op != OpReview || fd.Propose.Target != "dh-pub" {
		t.Fatalf("proposal = %+v, want a review naming dh-pub", fd.Propose)
	}
	assertVetoed(t, fd, "contradict each other")
	var dupKey string
	for _, w := range subclassOf(t, rep, ClassWorkDup, dupTitleAuthor) {
		if slices.Contains(workIDs(w), "dragonfire-unabridged") {
			dupKey = w.Key
		}
	}
	if dupKey == "" {
		t.Fatal("the fixture's duplicate work is not a W-DUP cluster")
	}
	assertVetoed(t, fd, "W-DUP "+dupKey)
	if !strings.Contains(strings.Join(fd.Notes, "\n"), "dragonfire at 2 in dh-pub, dragonfire-unabridged at 2 here") {
		t.Errorf("notes = %v, want the conflicting slot named", fd.Notes)
	}
}

// An ABRIDGED spelling holding part of the chronological variant's list folds onto it
// (advisory: a variant), through the abridged exemption the whole-group rule already has
// - and not onto the primary, whose different decoration still tells them apart.
func TestFamilySpellingFoldsAbridgedSubsetOntoUndecoratedVariant(t *testing.T) {
	files := seriesFixture(t, []string{"one", "two", "three", "four"}, mergeFiles(map[string]string{
		"series/kb/kb-pub.json": withOrdering(t, seriesJSON(t, "kb-pub", "Kingsbridge (Publication Order)",
			"two@2", "three@3", "one@4"), "publication"),
		"series/kb/kb.json": orderingVariant(t, seriesJSON(t, "kb", "Kingsbridge",
			"one@0", "two@2", "three@3"), "chronological", "kb-pub"),
		"series/kb/kb-abridged.json": seriesJSON(t, "kb-abridged", "The Kingsbridge Novels (abridged)", "two@2", "three@3"),
	}, abridgedRecordings(t, true, "two", "three")))
	rep := runFixture(t, files)
	assertProposalsConsistent(t, rep)
	fd := oneFamilyRecord(t, rep, SubclassFamilySpelling, "kb-abridged")
	if fd.Propose.Op != OpMergeSeries || fd.Propose.Target != "kb" || !fd.Propose.Advisory {
		t.Fatalf("proposal = %+v, want an advisory fold onto the variant kb", fd.Propose)
	}
}

// Two family members holding a spelling's list alike are an ambiguity a CONTAINING
// spelling settles: the spelling closes onto the member its container resolves to. With
// no container, which order the plain name means is a review.
func TestFamilySpellingClosesToTheRoot(t *testing.T) {
	family := func(t testing.TB) map[string]string {
		return map[string]string{
			"series/dh/dh-pub.json": withOrdering(t, seriesJSON(t, "dh-pub", "Dragon Heart",
				"two@2", "three@3", "one@4"), "publication"),
			"series/dh/dh-chrono.json": orderingVariant(t, seriesJSON(t, "dh-chrono", "Dragon Heart Series",
				"one@1", "two@2", "three@3"), "chronological", "dh-pub"),
			"series/dh/dh-novels.json": seriesJSON(t, "dh-novels", "The Dragon Heart Novels", "two@2", "three@3"),
		}
	}
	t.Run("a container settles it", func(t *testing.T) {
		rep := runFixture(t, seriesFixture(t, []string{"one", "two", "three", "four"}, mergeFiles(family(t), map[string]string{
			"series/dh/dh-books.json": seriesJSON(t, "dh-books", "Dragon Heart Books", "one@1", "two@2", "three@3", "four@4"),
		})))
		assertProposalsConsistent(t, rep)
		fd := oneFamilyRecord(t, rep, SubclassFamilySpelling, "dh-novels")
		if fd.Propose.Op != OpMergeSeries || fd.Propose.Target != "dh-chrono" {
			t.Fatalf("proposal = %+v, want dh-novels closed onto dh-chrono, the root dh-books resolves to", fd.Propose)
		}
		assertVetoed(t, fd, "resolves to dh-chrono")
		// The container itself adds a membership, so it is only ever a review.
		if c := oneFamilyRecord(t, rep, SubclassFamilySpelling, "dh-books"); c.Propose.Op != OpReview || c.Propose.Target != "dh-chrono" {
			t.Errorf("container = %+v, want a review naming dh-chrono", c.Propose)
		}
	})
	t.Run("no container is a review", func(t *testing.T) {
		rep := runFixture(t, seriesFixture(t, []string{"one", "two", "three"}, family(t)))
		fd := oneFamilyRecord(t, rep, SubclassFamilySpelling, "dh-novels")
		if fd.Propose.Op != OpReview {
			t.Fatalf("proposal = %+v, want a review", fd.Propose)
		}
		assertVetoed(t, fd, "alike, slot for slot")
	})
}

// A family member is never a loser, and neither is a translation: an Italian series of
// the same name and a series a translation_of link touches stay out of the family folds
// altogether, even where their list is the primary's.
func TestFamilySpellingNeverFoldsAFamilyMemberOrTranslation(t *testing.T) {
	files := seriesFixture(t, []string{"one", "two", "three", "four"}, mergeFiles(dragonFamily(t), map[string]string{
		"works/un/uno/work.json":         workJSON(t, "uno", "Uno", withLanguage("it")),
		"works/un/uno/recordings/i.json": recJSON(t, "i", "uno", testpack.WithRecLanguage("it")),
		"series/dh/dh-it.json":           seriesJSON(t, "dh-it", "Dragon Heart", "uno@1"),
		"series/dh/dh-linked.json": withField(t, seriesJSON(t, "dh-linked", "Dragon Heart Novels",
			"one@1", "two@2", "three@3", "four@4"), "translation_of", []string{"dh-it"}),
	}))
	rep := runFixture(t, files)
	assertProposalsConsistent(t, rep)
	for _, sub := range []string{SubclassFamilySpelling, SubclassFamilyRenumbered} {
		for _, fd := range subclassOf(t, rep, ClassSeriesDup, sub) {
			t.Errorf("unexpected %s record: %s %+v", sub, fd.Key, fd.Propose)
		}
	}
}

// A plain spelling STATING an ordering is a spelling of that ordering only: a
// chronological list is never folded into the publication order, whatever its slots.
func TestFamilySpellingRefusesADifferentStatedOrdering(t *testing.T) {
	rep := runFixture(t, seriesFixture(t, []string{"one", "two", "three", "four"}, mergeFiles(dragonFamily(t), map[string]string{
		"series/dh/dh.json": withOrdering(t, seriesJSON(t, "dh", "Dragon Heart",
			"one@1", "two@2", "three@3", "four@4"), "chronological"),
	})))
	for _, fd := range familyRecords(t, rep, SubclassFamilySpelling, "dh") {
		if fd.Propose.Target == "dh-pub" {
			t.Errorf("a chronological spelling was judged against the publication order: %+v", fd.Propose)
		}
	}
}

// The RENUMBERED shape: a spelling stating the primary's ordering, listing only its
// works in the same relative order under other numbers (Audible's Ranger's Apprentice
// numbering, which omits The Lost Stories). Always advisory, Field "position": the
// primary's numbering stands, and every differing membership is named.
func TestFamilyRenumberedKeepsTheTargetsNumbering(t *testing.T) {
	rep := runFixture(t, seriesFixture(t, []string{"one", "two", "three", "four"}, mergeFiles(rangersFamily(t), map[string]string{
		"series/ra/ra-pub.json": withOrdering(t, seriesJSON(t, "ra-pub", "Ranger's Apprentice (published order)",
			"one@1", "two@2", "four@3"), "publication"),
	})))
	assertProposalsConsistent(t, rep)
	fd := oneFamilyRecord(t, rep, SubclassFamilyRenumbered, "ra-pub")
	p := fd.Propose
	if p.Op != OpMergeSeries || p.Target != "ra" || p.Field != FieldPosition || !p.Advisory {
		t.Fatalf("proposal = %+v, want an advisory position-field fold onto ra", p)
	}
	assertVetoed(t, fd, "four at 3 here, 4 in ra")
}

// The relative ORDER is the renumbering's whole evidence: a spelling that swaps two
// volumes is another order, and gets the review instead.
func TestFamilyRenumberedRefusesALoserOutOfOrder(t *testing.T) {
	rep := runFixture(t, seriesFixture(t, []string{"one", "two", "three", "four"}, mergeFiles(rangersFamily(t), map[string]string{
		"series/ra/ra-pub.json": withOrdering(t, seriesJSON(t, "ra-pub", "Ranger's Apprentice (published order)",
			"one@1", "four@2", "two@3"), "publication"),
	})))
	if got := familyRecords(t, rep, SubclassFamilyRenumbered, "ra-pub"); len(got) != 0 {
		t.Fatalf("an out-of-order spelling was proposed as a renumbering: %+v", got)
	}
	if fd := oneFamilyRecord(t, rep, SubclassFamilySpelling, "ra-pub"); fd.Propose.Op != OpReview {
		t.Errorf("proposal = %+v, want a review", fd.Propose)
	}
}

// rangersFamily is a publication-order primary holding a volume (three, The Lost
// Stories) another numbering omits, plus its chronological variant.
func rangersFamily(t testing.TB) map[string]string {
	t.Helper()
	return map[string]string{
		"series/ra/ra.json": withOrdering(t, seriesJSON(t, "ra", "Ranger's Apprentice",
			"one@1", "two@2", "three@3", "four@4"), "publication"),
		"series/ra/ra-chrono.json": orderingVariant(t, seriesJSON(t, "ra-chrono", "Ranger's Apprentice (chronological order)",
			"three@1", "one@2", "two@3", "four@4"), "chronological", "ra"),
	}
}

// ---- ordering twins -----------------------------------------------------------

// The Jack Ryan shape: an orphan publication order under one franchise word whose one
// membership a same-ordering twin under another word already holds. SER-DUP never groups
// them, so the twin is proposed apart - and ALWAYS advisory.
func TestOrderingTwinFoldsAnOrphanOntoItsTwin(t *testing.T) {
	rep := runFixture(t, seriesFixture(t, []string{"one", "two", "three", "four"}, map[string]string{
		"series/jr/novel.json": withOrdering(t, seriesJSON(t, "novel", "A Jack Ryan Novel (publication order)",
			"one@1", "two@2", "three@3", "four@4"), "publication"),
		"series/jr/universe.json": withOrdering(t, seriesJSON(t, "universe", "The Jack Ryan Universe (publication order)",
			"four@4"), "publication"),
	}))
	assertProposalsConsistent(t, rep)
	got := subclassOf(t, rep, ClassSeriesDup, serDupTwin)
	if len(got) != 1 {
		t.Fatalf("want one ordering-twin record, got %+v", got)
	}
	p := got[0].Propose
	if got[0].Key != "universe" || p.Op != OpMergeSeries || p.Target != "novel" || !slices.Equal(p.Others, []string{"universe"}) {
		t.Errorf("record = %s %+v, want universe folded onto novel", got[0].Key, p)
	}
	if !p.Advisory {
		t.Error("an ordering twin was proposed mechanically")
	}
}

// A SUB-SERIES is never folded onto its parent, though its one membership sits at the
// parent's slot (Rincewind 1 is Discworld 1): the franchise keys differ, and a
// "<parent>: <sub-series>" name whose head is the parent is refused even where the
// franchise words would make the keys agree.
func TestOrderingTwinIgnoresASubseries(t *testing.T) {
	for _, name := range []string{"Rincewind (Publication Order)", "Discworld: Rincewind (Publication Order)", "Discworld: Universe (Publication Order)"} {
		t.Run(name, func(t *testing.T) {
			rep := runFixture(t, seriesFixture(t, []string{"one", "two"}, map[string]string{
				"series/di/discworld.json": withOrdering(t, seriesJSON(t, "discworld", "Discworld", "one@1", "two@2"), "publication"),
				"series/ri/rincewind.json": withOrdering(t, seriesJSON(t, "rincewind", name, "one@1"), "publication"),
			}))
			if got := subclassOf(t, rep, ClassSeriesDup, serDupTwin); len(got) != 0 {
				t.Errorf("a sub-series was folded onto its parent: %+v", got)
			}
		})
	}
}

// The orphan must STATE an ordering, and the same one its twin states.
func TestOrderingTwinNeedsTheSameStatedOrdering(t *testing.T) {
	novel := func(t testing.TB) string {
		return withOrdering(t, seriesJSON(t, "novel", "A Jack Ryan Novel", "one@1", "two@2"), "publication")
	}
	for name, universe := range map[string]func(testing.TB) string{
		"another ordering": func(t testing.TB) string {
			return withOrdering(t, seriesJSON(t, "universe", "The Jack Ryan Universe", "two@2"), "chronological")
		},
		"no ordering": func(t testing.TB) string { return seriesJSON(t, "universe", "The Jack Ryan Universe", "two@2") },
	} {
		t.Run(name, func(t *testing.T) {
			rep := runFixture(t, seriesFixture(t, []string{"one", "two"}, map[string]string{
				"series/jr/novel.json":    novel(t),
				"series/jr/universe.json": universe(t),
			}))
			if got := subclassOf(t, rep, ClassSeriesDup, serDupTwin); len(got) != 0 {
				t.Errorf("unexpected ordering twin: %+v", got)
			}
		})
	}
}

// Exactly one twin, or no proposal: an orphan two same-ordering twins both hold is
// nobody's to fold. And whatever is proposed is advisory.
func TestOrderingTwinNeedsExactlyOneTwin(t *testing.T) {
	files := seriesFixture(t, []string{"one", "two"}, map[string]string{
		"series/jr/novel.json":  withOrdering(t, seriesJSON(t, "novel", "A Jack Ryan Novel", "one@1", "two@2"), "publication"),
		"series/jr/saga.json":   withOrdering(t, seriesJSON(t, "saga", "Jack Ryan Saga", "two@2"), "publication"),
		"series/jr/orphan.json": withOrdering(t, seriesJSON(t, "orphan", "The Jack Ryan Universe", "two@2"), "publication"),
	})
	rep := runFixture(t, files)
	for _, fd := range subclassOf(t, rep, ClassSeriesDup, serDupTwin) {
		if !fd.Propose.Advisory {
			t.Errorf("mechanical ordering twin: %+v", fd.Propose)
		}
		if fd.Key == "orphan" {
			t.Errorf("an orphan with two twins was folded: %+v", fd.Propose)
		}
	}
}

// Two orphans holding the same list are each other's twin: the fold is proposed once,
// onto the better-ranked spelling, never both ways.
func TestOrderingTwinIsProposedInOneDirection(t *testing.T) {
	rep := runFixture(t, seriesFixture(t, []string{"one", "two"}, map[string]string{
		"series/jr/novel.json": withOrdering(t, seriesJSON(t, "novel", "A Jack Ryan Novel (publication order)",
			"two@2"), "publication"),
		"series/jr/universe.json": withOrdering(t, seriesJSON(t, "universe", "The Jack Ryan Universe (publication order)",
			"two@2"), "publication"),
	}))
	got := subclassOf(t, rep, ClassSeriesDup, serDupTwin)
	if len(got) != 1 {
		t.Fatalf("want one ordering-twin record, got %+v", got)
	}
	if p := got[0].Propose; p.Target != "novel" || !slices.Equal(p.Others, []string{"universe"}) {
		t.Errorf("proposal = %+v, want universe folded onto novel", p)
	}
}
