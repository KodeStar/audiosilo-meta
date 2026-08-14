package audit

import (
	"strings"
	"testing"
)

// THE HOURLY HISTORY SHAPE, which is what this veto exists for: two unrelated books by
// one publisher whose titles are "<subject>: A History from Beginning to End". Neither is
// modeled in a series, both subjects happen to BE catalogued series names, so each title
// sheds its own subject as a leading series segment and the two meet on the template.
// A live wave proposed the merge non-advisory.
func TestWorkDupVetoesATemplateResidualLeftByTwoSubjects(t *testing.T) {
	rep := runFixture(t, fixture(t, map[string]string{
		"works/co/cold-war-history/work.json":         workJSON(t, "cold-war-history", "Cold War: A History from Beginning to End"),
		"works/co/cold-war-history/recordings/a.json": recJSON(t, "a", "cold-war-history", withRuntime(69)),
		"works/th/hundred-years-history/work.json":    workJSON(t, "hundred-years-history", "The Hundred Years War: A History from Beginning to End"),
		"works/th/hundred-years-history/recordings/b.json": recJSON(t, "b", "hundred-years-history", withRuntime(69),
			withNarrators("other-narrator")),
		// The two series whose names the titles spell out, each with a member of its own -
		// they are what the embedded-series derivation resolves against.
		"works/be/berlin-airlift/work.json":         workJSON(t, "berlin-airlift", "The Berlin Airlift"),
		"works/be/berlin-airlift/recordings/c.json": recJSON(t, "c", "berlin-airlift"),
		"works/ag/agincourt/work.json":              workJSON(t, "agincourt", "Agincourt"),
		"works/ag/agincourt/recordings/d.json":      recJSON(t, "d", "agincourt"),
		"series/co/cold-war.json":                   seriesJSON(t, "cold-war", "Cold War", "berlin-airlift@1"),
		"series/hu/hundred-years-war.json":          seriesJSON(t, "hundred-years-war", "The Hundred Years War", "agincourt@1"),
		"people/ot/other-narrator.json":             personJSON(t, "other-narrator", "Other Narrator"),
	}))
	got := subclassOf(t, rep, ClassWorkDup, dupTitleAuthor)
	if len(got) != 1 {
		t.Fatalf("want one cluster over the two template titles, got %d: %+v", len(got), classOf(t, rep, ClassWorkDup))
	}
	if !got[0].Propose.Advisory {
		t.Fatalf("two different books were proposed for merge: %+v", got[0].Propose)
	}
	if !strings.Contains(got[0].Propose.Reason, "cleaned against series") {
		t.Errorf("reason = %q, want it to name both series the titles shed", got[0].Propose.Reason)
	}
}

// The positive it must not touch: a leading "<Series>: <Title>" where the title carries
// the identity, against the plain twin that IS that book. Both sides are read against
// the same name, so there is nothing to disagree about.
func TestWorkDupKeepsALeadingSeriesSegmentMerge(t *testing.T) {
	rep := runFixture(t, fixture(t, map[string]string{
		"works/cu/curse-of-the-bane/work.json":         workJSON(t, "curse-of-the-bane", "Curse of the Bane"),
		"works/cu/curse-of-the-bane/recordings/a.json": recJSON(t, "a", "curse-of-the-bane", withRuntime(400)),
		"works/th/the-last-apprentice-curse-of-the-bane/work.json": workJSON(t, "the-last-apprentice-curse-of-the-bane",
			"The Last Apprentice: Curse of the Bane"),
		"works/th/the-last-apprentice-curse-of-the-bane/recordings/b.json": recJSON(t, "b",
			"the-last-apprentice-curse-of-the-bane", withRuntime(402)),
		"series/th/the-last-apprentice.json": seriesJSON(t, "the-last-apprentice", "The Last Apprentice", "curse-of-the-bane@2"),
	}))
	got := subclassOf(t, rep, ClassWorkDup, dupTitleAuthor)
	if len(got) != 1 {
		t.Fatalf("want one cluster, got %d", len(got))
	}
	if got[0].Propose.Advisory {
		t.Errorf("a decorated twin of one book was withheld: %q", got[0].Propose.Reason)
	}
}

// THE EMPTY BODIES SHAPE: a record whose own title states "Book 2" of a series, beside
// the record the catalogue places at position 1 of that same series. Neither the title
// comparison nor vetoPositionConflict can see it - the volume-stating side is modeled in
// nothing - and every other rung clears (one author, one narrator, 298 against 294
// minutes). "Adaptation" is packaging vocabulary, so the leading segment comes off as
// fluff and the series reference is all that is left to clean.
func TestWorkDupVetoesAStatedVolumeTheCatalogueContradicts(t *testing.T) {
	rep := runFixture(t, fixture(t, map[string]string{
		"works/em/empty-bodies/work.json":         workJSON(t, "empty-bodies", "Empty Bodies"),
		"works/em/empty-bodies/recordings/a.json": recJSON(t, "a", "empty-bodies", withRuntime(294)),
		"works/ad/adaptation-empty-bodies-series-book-2/work.json": workJSON(t, "adaptation-empty-bodies-series-book-2",
			"Adaptation: (A Post-Apocalyptic Tale of Dystopian Survival) Empty Bodies Series, Book 2"),
		"works/ad/adaptation-empty-bodies-series-book-2/recordings/b.json": recJSON(t, "b",
			"adaptation-empty-bodies-series-book-2", withRuntime(298)),
		"series/em/empty-bodies.json": seriesJSON(t, "empty-bodies", "Empty Bodies", "empty-bodies@1"),
	}))
	got := subclassOf(t, rep, ClassWorkDup, dupTitleAuthor)
	if len(got) != 1 {
		t.Fatalf("want one cluster (both titles clean to the series name), got %d: %+v", len(got), classOf(t, rep, ClassWorkDup))
	}
	if !got[0].Propose.Advisory {
		t.Fatalf("volume 2 was proposed for merge onto volume 1: %+v", got[0].Propose)
	}
	if !strings.Contains(got[0].Propose.Reason, "states volume 2") {
		t.Errorf("reason = %q, want it to name the volume the title states", got[0].Propose.Reason)
	}
}

// ...and the positive arm it deliberately does not take: one book whose two records
// differ by the word "Series", where the MODELED side states the volume and the side it
// is asked about is modeled in nothing. Silence is not a contradiction here.
func TestWorkDupKeepsAMergeWhenNoPositionContradictsIt(t *testing.T) {
	rep := runFixture(t, fixture(t, map[string]string{
		"works/en/endangered-book-2/work.json": workJSON(t, "endangered-book-2", "Endangered: Zak Bates Eco-Adventure, Book 2"),
		"works/en/endangered-book-2/recordings/a.json": recJSON(t, "a", "endangered-book-2", withRuntime(336),
			withNarrators("benjamin-fife")),
		"works/en/endangered-series-book-2/work.json": workJSON(t, "endangered-series-book-2",
			"Endangered: Zak Bates Eco-Adventure Series, Book 2"),
		"works/en/endangered-series-book-2/recordings/b.json": recJSON(t, "b", "endangered-series-book-2", withRuntime(336),
			withNarrators("benjamin-fife")),
		"series/za/zak-bates.json":     seriesJSON(t, "zak-bates", "Zak Bates Eco-Adventure", "endangered-series-book-2@2"),
		"people/be/benjamin-fife.json": personJSON(t, "benjamin-fife", "Benjamin Fife"),
	}))
	got := subclassOf(t, rep, ClassWorkDup, dupTitleAuthor)
	if len(got) != 1 {
		t.Fatalf("want one cluster, got %d", len(got))
	}
	if got[0].Propose.Advisory {
		t.Errorf("one book's two records were withheld: %q", got[0].Propose.Reason)
	}
}

// A CLOSED cluster's members need not have met each other, and the veto must not read a
// disagreement neither key claimed. The six Dante records are the live shape: two keys
// ("inferno" and "infernofromthedivinecomedy") share a work, so the closure fuses them,
// and "The Divine Comedy: Inferno" (cleaned against that series) never met "The Inferno
// from The Divine Comedy" (a member of nothing, whose mid-title series mention the
// boundary anchoring correctly leaves alone).
func TestWorkDupVetoJudgesOnlyThePairsThatMetOnAKey(t *testing.T) {
	rep := runFixture(t, fixture(t, map[string]string{
		"works/in/inferno/work.json":         workJSON(t, "inferno", "Inferno"),
		"works/in/inferno/recordings/a.json": recJSON(t, "a", "inferno", withRuntime(500)),
		"works/in/inferno-from-the-divine-comedy/work.json": workJSON(t, "inferno-from-the-divine-comedy",
			"Inferno: From The Divine Comedy"),
		"works/in/inferno-from-the-divine-comedy/recordings/b.json": recJSON(t, "b",
			"inferno-from-the-divine-comedy", withRuntime(505)),
		"works/th/the-divine-comedy-inferno/work.json": workJSON(t, "the-divine-comedy-inferno", "The Divine Comedy: Inferno"),
		"works/th/the-divine-comedy-inferno/recordings/c.json": recJSON(t, "c",
			"the-divine-comedy-inferno", withRuntime(510)),
		"works/th/the-inferno-from-the-divine-comedy/work.json": workJSON(t, "the-inferno-from-the-divine-comedy",
			"The Inferno from The Divine Comedy"),
		"works/th/the-inferno-from-the-divine-comedy/recordings/d.json": recJSON(t, "d",
			"the-inferno-from-the-divine-comedy", withRuntime(515)),
		"series/th/the-divine-comedy.json": seriesJSON(t, "the-divine-comedy", "The Divine Comedy", "inferno@1"),
	}))
	got := subclassOf(t, rep, ClassWorkDup, dupTitleAuthor)
	if len(got) != 1 {
		t.Fatalf("want one closed cluster over the four records, got %d: %+v", len(got), classOf(t, rep, ClassWorkDup))
	}
	if len(got[0].Works) != 4 {
		t.Fatalf("the closure did not fuse the two keys: %d members", len(got[0].Works))
	}
	if got[0].Propose.Advisory {
		t.Errorf("a transitively closed cluster of one book was withheld: %q", got[0].Propose.Reason)
	}
}
