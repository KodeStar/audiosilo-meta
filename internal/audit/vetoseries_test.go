package audit

import (
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// serDupMerge is the one normalized-name SER-DUP record a fixture produced, with its
// proposal - every veto in vetoseries.go is observed through it rather than by calling the
// rule, so a veto that fires but never reaches the proposal cannot pass.
func serDupMerge(t testing.TB, rep *Report) Finding {
	t.Helper()
	got := subclassOf(t, rep, ClassSeriesDup, serDupName)
	if len(got) != 1 {
		t.Fatalf("want one normalized-name cluster, got %d: %+v", len(got), classOf(t, rep, ClassSeriesDup))
	}
	return got[0]
}

// assertVetoed fails unless the cluster's merge was withheld for the stated reason.
func assertVetoed(t testing.TB, fd Finding, want string) {
	t.Helper()
	if !fd.Propose.Advisory {
		t.Fatalf("the merge was proposed mechanically: %+v", fd.Propose)
	}
	if !strings.Contains(fd.Propose.Reason, want) {
		t.Errorf("reason = %q, want it to state %q", fd.Propose.Reason, want)
	}
}

// assertMechanical fails unless the cluster is still a mechanical merge - the negative
// half of every rule below, and the reason the class exists at all.
func assertMechanical(t testing.TB, fd Finding) {
	t.Helper()
	if fd.Propose.Advisory {
		t.Fatalf("a sound merge was withheld: %s", fd.Propose.Reason)
	}
	if fd.Propose.Op != OpMergeSeries {
		t.Errorf("op = %q, want %q", fd.Propose.Op, OpMergeSeries)
	}
}

// ---- author agreement --------------------------------------------------------

// The dominant veto: 581 of the 694 non-advisory proposals the class made were
// author-disjoint, and every one of the 36 a repair would have applied cleanly was.
func TestSeriesDupVetoesAuthorDisjointSides(t *testing.T) {
	files := fixture(t, map[string]string{
		"works/al/alpha-one/work.json":         workJSON(t, "alpha-one", "Alpha One", withAuthors("ann-author")),
		"works/al/alpha-one/recordings/a.json": recJSON(t, "a", "alpha-one"),
		"works/be/beta-one/work.json":          workJSON(t, "beta-one", "Beta One", withAuthors("bob-writer")),
		"works/be/beta-one/recordings/b.json":  recJSON(t, "b", "beta-one"),
		"people/an/ann-author.json":            personJSON(t, "ann-author", "Ann Author"),
		"people/bo/bob-writer.json":            personJSON(t, "bob-writer", "Bob Writer"),
		"series/aa/alpha.json":                 seriesJSON(t, "alpha", "Dragon Heart", "alpha-one@1"),
		"series/bb/beta.json":                  seriesJSON(t, "beta", "Dragon Heart Series", "beta-one@2"),
	})
	fd := serDupMerge(t, runFixture(t, files))
	assertVetoed(t, fd, "share no member-work author")
	for _, want := range []string{"ann-author", "bob-writer"} {
		if !strings.Contains(fd.Propose.Reason, want) {
			t.Errorf("reason = %q, want both sides' authors named (%s missing)", fd.Propose.Reason, want)
		}
	}
}

// The negative: one author across both spellings is what a duplicate series name looks
// like, and it stays mechanical.
func TestSeriesDupMergesTwoSpellingsOfOneAuthorsSeries(t *testing.T) {
	fd := serDupMerge(t, runFixture(t, seriesFixture(t, []string{"one", "two"}, map[string]string{
		"series/aa/alpha.json": seriesJSON(t, "alpha", "Dragon Heart", "one@1"),
		"series/bb/beta.json":  seriesJSON(t, "beta", "Dragon Heart Series", "two@2"),
	})))
	assertMechanical(t, fd)
}

// The MEMBER arm: the sides' author lists overlap, but the membership the fold would MOVE
// is by nobody the target holds - the shape of `kingkillerchronicle`, whose loser
// contributes the Dozois/Martin/Gaiman/Flynn anthology "Rogues" at 1.5.
func TestSeriesDupVetoesAMovedWorkByAnotherAuthor(t *testing.T) {
	// The three-work spelling outranks the two-work one, so the anthology is on the side
	// the fold would MOVE - which is the only side the member arm speaks about.
	files := seriesFixture(t, []string{"one", "two", "three"}, map[string]string{
		"works/ro/rogues/work.json":         workJSON(t, "rogues", "Rogues", withAuthors("bob-writer")),
		"works/ro/rogues/recordings/b.json": recJSON(t, "b", "rogues"),
		"people/bo/bob-writer.json":         personJSON(t, "bob-writer", "Bob Writer"),
		"series/th/the-kingkiller-chronicle.json": seriesJSON(t, "the-kingkiller-chronicle",
			"The Kingkiller Chronicle", "one@1", "two@2", "three@4"),
		"series/ki/kingkiller-chronicle.json": seriesJSON(t, "kingkiller-chronicle",
			"Kingkiller Chronicle", "one@1", "rogues@3"),
	})
	fd := serDupMerge(t, runFixture(t, files))
	assertVetoed(t, fd, "shares no author with it")
	if !strings.Contains(fd.Propose.Reason, "rogues") {
		t.Errorf("reason = %q, want the moved work named", fd.Propose.Reason)
	}
}

// PRECISION CASE (a): the two sides are author-disjoint only because one author's records
// forked in the PEOPLE family - `cheree-alsop` beside `cheree-lynn-alsop`. A defect in a
// different family must not manufacture a veto, so this merge stays mechanical.
func TestSeriesDupMergesThroughAForkedPersonIdentity(t *testing.T) {
	fd := serDupMerge(t, runFixture(t, fixture(t, map[string]string{
		"works/da/daybreak/work.json":          workJSON(t, "daybreak", "Daybreak", withAuthors("cheree-alsop")),
		"works/da/daybreak/recordings/a.json":  recJSON(t, "a", "daybreak"),
		"works/da/days-hunt/work.json":         workJSON(t, "days-hunt", "Day's Hunt", withAuthors("cheree-lynn-alsop")),
		"works/da/days-hunt/recordings/b.json": recJSON(t, "b", "days-hunt"),
		"people/ch/cheree-alsop.json":          personJSON(t, "cheree-alsop", "Cheree Alsop"),
		"people/ch/cheree-lynn-alsop.json":     personJSON(t, "cheree-lynn-alsop", "Cheree Lynn Alsop"),
		"series/gi/girl-from-the-stars.json":   seriesJSON(t, "girl-from-the-stars", "Girl from the Stars", "days-hunt@5"),
		"series/gi/girl-from-the-stars-s.json": seriesJSON(t, "girl-from-the-stars-s", "Girl from the Stars Series", "daybreak@1"),
	})))
	assertMechanical(t, fd)
}

// PRECISION CASE (b): a GHOSTWRITTEN franchise really is author-disjoint - Tom Clancy's
// own "The Teeth of the Tiger" shares no author with the continuations that follow it -
// and withholding that merge is the CORRECT outcome, not a false positive. A human
// confirms the franchise; nothing in the data states it.
func TestSeriesDupVetoesAGhostwrittenFranchise(t *testing.T) {
	fd := serDupMerge(t, runFixture(t, fixture(t, map[string]string{
		"works/un/under-fire/work.json":          workJSON(t, "under-fire", "Under Fire", withAuthors("grant-blackwood")),
		"works/un/under-fire/recordings/a.json":  recJSON(t, "a", "under-fire"),
		"works/te/teeth-tiger/work.json":         workJSON(t, "teeth-tiger", "The Teeth of the Tiger", withAuthors("tom-clancy")),
		"works/te/teeth-tiger/recordings/b.json": recJSON(t, "b", "teeth-tiger"),
		"people/gr/grant-blackwood.json":         personJSON(t, "grant-blackwood", "Grant Blackwood"),
		"people/to/tom-clancy.json":              personJSON(t, "tom-clancy", "Tom Clancy"),
		"series/aa/a-jack-ryan-jr-novel.json":    seriesJSON(t, "a-jack-ryan-jr-novel", "A Jack Ryan Jr. Novel", "under-fire@2"),
		"series/ja/jack-ryan-jr.json":            seriesJSON(t, "jack-ryan-jr", "Jack Ryan Jr.", "teeth-tiger@1"),
	})))
	assertVetoed(t, fd, "share no member-work author")
}

func TestSamePersonSpellingToleratesOneNameSpelledTwoWays(t *testing.T) {
	names := map[string]string{
		"cheree-alsop":         "Cheree Alsop",
		"cheree-lynn-alsop":    "Cheree Lynn Alsop",
		"sarah-maas":           "Sarah Maas",
		"sarah-j-maas":         "Sarah J. Maas",
		"sarah-pinsker":        "Sarah Pinsker",
		"a-b-kovacs":           "A.B. Kovacs",
		"ab-kovacs":            "AB Kovacs",
		"rachel-rene-russell":  "Rachel Rene Russell",
		"rachel-renee-russell": "Rachel Renee Russell",
		"jon-ray":              "Jon Ray",
		"jan-ray":              "Jan Ray",
		"cafe-society-press":   "Cafe Society Press",
		"cafe-society-press-2": "Café Society Press",
	}
	people := make([]*model.Person, 0, len(names))
	for id, name := range names {
		people = append(people, &model.Person{ID: id, Name: name, License: "CC0-1.0"})
	}
	ix := newIndex(&model.Catalog{People: people})

	cases := []struct {
		a, b string
		want bool
		why  string
	}{
		{"cheree-alsop", "cheree-alsop", true, "an id is itself"},
		{"cheree-alsop", "cheree-lynn-alsop", true, "a middle name is not a second person"},
		{"sarah-maas", "sarah-j-maas", true, "a middle initial is not a second person"},
		{"sarah-maas", "sarah-pinsker", false, "a shared first name is not an identity"},
		{"a-b-kovacs", "ab-kovacs", true, "the importer's own initials identity"},
		{"cafe-society-press", "cafe-society-press-2", true, "a diacritic is not an identity"},
		{"rachel-rene-russell", "rachel-renee-russell", true, "one edit over the length floor"},
		{"jon-ray", "jan-ray", false, "short names one edit apart are two people"},
		{"cheree-alsop", "nobody-at-all", false, "an id with no record is only itself"},
	}
	for _, c := range cases {
		if got := samePersonSpelling(ix, c.a, c.b); got != c.want {
			t.Errorf("samePersonSpelling(%q, %q) = %v, want %v: %s", c.a, c.b, got, c.want, c.why)
		}
	}
}

// ---- member-level collection evidence ----------------------------------------

// The name-level rule cannot see this: "Afflicted" is a plain name whose one member is
// Derek Shupert's "The Complete Undead Apocalypse Series".
func TestSeriesDupVetoesASideWhoseMembersAreAllCollections(t *testing.T) {
	files := seriesFixture(t, []string{"one", "two"}, map[string]string{
		"works/co/complete-set/work.json":         workJSON(t, "complete-set", "The Complete Undead Apocalypse Series"),
		"works/co/complete-set/recordings/c.json": recJSON(t, "c", "complete-set"),
		"series/th/the-afflicted.json":            seriesJSON(t, "the-afflicted", "The Afflicted", "one@1", "two@2"),
		"series/af/afflicted.json":                seriesJSON(t, "afflicted", "Afflicted", "complete-set@5"),
	})
	fd := serDupMerge(t, runFixture(t, files))
	assertVetoed(t, fd, "is titled as a collection")
}

// A real series that also HOLDS an omnibus is not a series of collections: the rule judges
// a side whole, so it stays mechanical.
func TestSeriesDupMergesASeriesThatMerelyHoldsAnOmnibus(t *testing.T) {
	files := seriesFixture(t, []string{"one", "two"}, map[string]string{
		"works/bo/boxed/work.json":         workJSON(t, "boxed", "Afflicted Box Set"),
		"works/bo/boxed/recordings/c.json": recJSON(t, "c", "boxed"),
		"series/th/the-afflicted.json":     seriesJSON(t, "the-afflicted", "The Afflicted", "one@1"),
		"series/af/afflicted.json":         seriesJSON(t, "afflicted", "Afflicted", "two@2", "boxed@5"),
	})
	assertMechanical(t, serDupMerge(t, runFixture(t, files)))
}

// The POSITION arm of the same rule: a range IS the statement "this product covers those
// volumes", which is what reaches the three Bayne omnibuses whose titles say nothing.
func TestSeriesDupVetoesRangePositionsAgainstSingleSlots(t *testing.T) {
	files := seriesFixture(t, []string{"one", "two"}, map[string]string{
		"works/om/omni/work.json":         workJSON(t, "omni", "Bayne of Existence"),
		"works/om/omni/recordings/c.json": recJSON(t, "c", "omni"),
		"series/de/deep-black.json":       seriesJSON(t, "deep-black", "Deep Black", "one@1", "two@2"),
		"series/th/the-deep-black.json":   seriesJSON(t, "the-deep-black", "The Deep Black", "omni@7-9"),
	})
	fd := serDupMerge(t, runFixture(t, files))
	assertVetoed(t, fd, "spans a RANGE of positions")
}

// Both sides ordered the same way is no evidence either way, so the range arm is silent.
func TestSeriesDupIsSilentWhenBothSidesUseRanges(t *testing.T) {
	files := fixture(t, map[string]string{
		"works/om/omni-a/work.json":         workJSON(t, "omni-a", "Volumes One to Three"),
		"works/om/omni-a/recordings/a.json": recJSON(t, "a", "omni-a"),
		"works/om/omni-b/work.json":         workJSON(t, "omni-b", "Volumes Four to Six"),
		"works/om/omni-b/recordings/b.json": recJSON(t, "b", "omni-b"),
		"series/de/deep-black.json":         seriesJSON(t, "deep-black", "Deep Black", "omni-a@1-3"),
		"series/th/the-deep-black.json":     seriesJSON(t, "the-deep-black", "The Deep Black", "omni-b@4-6"),
	})
	assertMechanical(t, serDupMerge(t, runFixture(t, files)))
}

// The EXEMPTION: a loser whose memberships are already in the target at the same slots
// moves nothing, so no omnibus can land in a volume's place - the pure duplicate-spelling
// retirement this class exists for (`mephistosmagiconline`).
func TestSeriesDupMergesACollectionSideThatMovesNothing(t *testing.T) {
	files := seriesFixture(t, []string{"one", "two"}, map[string]string{
		"works/om/omnibus/work.json":         workJSON(t, "omnibus", "Mephisto's Magic Online Omnibus"),
		"works/om/omnibus/recordings/c.json": recJSON(t, "c", "omnibus"),
		"series/me/mephistos-magic.json": seriesJSON(t, "mephistos-magic", "Mephisto's Magic Online",
			"one@1", "two@2", "omnibus@1-2"),
		"series/me/mephistos-magic-s.json": seriesJSON(t, "mephistos-magic-s", "Mephisto's Magic Online Series",
			"omnibus@1-2"),
	})
	assertMechanical(t, serDupMerge(t, runFixture(t, files)))
}

// ---- ordering agreement ------------------------------------------------------

// Two different works claiming one place in the order: which of them holds it is a human
// decision, and where the two are one book under two slugs it is W-DUP's to resolve first.
func TestSeriesDupVetoesTwoWorksAtOneSlot(t *testing.T) {
	fd := serDupMerge(t, runFixture(t, seriesFixture(t, []string{"one", "two"}, map[string]string{
		"series/aa/alpha.json": seriesJSON(t, "alpha", "Dragon Heart", "one@1"),
		"series/bb/beta.json":  seriesJSON(t, "beta", "Dragon Heart Series", "two@1"),
	})))
	assertVetoed(t, fd, "contradict each other")
}

// One SLOT spelled two ways is one place in the order: pkg/check compares the stored
// strings, so a merge that put two works there would have left a green tree.
func TestSeriesDupVetoesOneSlotSpelledTwoWays(t *testing.T) {
	fd := serDupMerge(t, runFixture(t, seriesFixture(t, []string{"one", "two"}, map[string]string{
		"series/aa/alpha.json": seriesJSON(t, "alpha", "Dragon Heart", "one@3"),
		"series/bb/beta.json":  seriesJSON(t, "beta", "Dragon Heart Series", "two@03"),
	})))
	assertVetoed(t, fd, "contradict each other")
}

// One work the two spellings place differently: two orderings are not one series.
func TestSeriesDupVetoesOneWorkAtTwoSlots(t *testing.T) {
	fd := serDupMerge(t, runFixture(t, seriesFixture(t, []string{"one", "two"}, map[string]string{
		"series/aa/alpha.json": seriesJSON(t, "alpha", "Dragon Heart", "one@1", "two@2"),
		"series/bb/beta.json":  seriesJSON(t, "beta", "Dragon Heart Series", "one@4"),
	})))
	assertVetoed(t, fd, "two orderings are not one series")
}

// The negative: memberships that agree where they overlap and otherwise fill free
// positions are exactly what a mechanical fold is for.
func TestSeriesDupMergesAgreeingOrderings(t *testing.T) {
	fd := serDupMerge(t, runFixture(t, seriesFixture(t, []string{"one", "two", "three"}, map[string]string{
		"series/aa/alpha.json": seriesJSON(t, "alpha", "Dragon Heart", "one@1", "two@2"),
		"series/bb/beta.json":  seriesJSON(t, "beta", "Dragon Heart Series", "one@1", "three@3"),
	})))
	assertMechanical(t, fd)
}

// ---- language, on the merits -------------------------------------------------

// A side with no strict MAJORITY language contributed nothing to the rule this replaced,
// which is exactly how the French edition of Hyperion (en 2 / fr 2) stayed mechanical.
func TestSeriesDupVetoesALanguageEditionWithNoMajority(t *testing.T) {
	files := fixture(t, map[string]string{
		"works/hy/hyperion/work.json":         workJSON(t, "hyperion", "Hyperion"),
		"works/hy/hyperion/recordings/a.json": recJSON(t, "a", "hyperion"),
		"works/en/endymion/work.json":         workJSON(t, "endymion", "Endymion"),
		"works/en/endymion/recordings/b.json": recJSON(t, "b", "endymion"),
		"works/la/la-chute/work.json":         workJSON(t, "la-chute", "La chute d'Hyperion", withLanguage("fr")),
		"works/la/la-chute/recordings/c.json": recJSON(t, "c", "la-chute"),
		"works/le/leveil/work.json":           workJSON(t, "leveil", "L'eveil d'Endymion", withLanguage("fr")),
		"works/le/leveil/recordings/d.json":   recJSON(t, "d", "leveil"),
		"series/hy/hyperion-series.json":      seriesJSON(t, "hyperion-series", "Hyperion Series", "hyperion@1", "endymion@3"),
		// The French edition, stored as the two translations BESIDE the two English works
		// it shares with its twin: en 2 / fr 2, a tie with no majority language at all.
		"series/hy/hyperion-two.json": seriesJSON(t, "hyperion-two", "Hyperion",
			"hyperion@1", "la-chute@2", "endymion@3", "leveil@4"),
	})
	rep := runFixture(t, files)
	fd := serDupMerge(t, rep)
	for _, s := range fd.Series {
		if s.ID == "hyperion-two" && s.Language != "" {
			t.Errorf("the French side reports a majority language %q; the fixture is not the tie this rule is about", s.Language)
		}
	}
	assertVetoed(t, fd, "a translation is a different series")
}

// An UNKNOWN language separates nothing, here as everywhere else in the project: the
// disagreement has to be between two STATED values.
func TestSeriesDupIsSilentOnAnUnstatedLanguage(t *testing.T) {
	files := fixture(t, map[string]string{
		"works/on/one/work.json":         workJSON(t, "one", "One"),
		"works/on/one/recordings/a.json": recJSON(t, "a", "one"),
		"works/tw/two/work.json":         withoutField(t, workJSON(t, "two", "Two"), "language"),
		"works/tw/two/recordings/b.json": recJSON(t, "b", "two"),
		"series/aa/alpha.json":           seriesJSON(t, "alpha", "Dragon Heart", "one@1"),
		"series/bb/beta.json":            seriesJSON(t, "beta", "Dragon Heart Series", "two@2"),
	})
	// The language-less work is schema-invalid, which the audit explicitly supports being
	// pointed at.
	rep, _ := runFixtureAllowingProblems(t, files)
	assertMechanical(t, serDupMerge(t, rep))
}
