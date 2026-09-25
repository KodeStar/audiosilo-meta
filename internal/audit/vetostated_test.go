package audit

import (
	"strings"
	"testing"
)

// statedWork is one work of a two-work one-sided-statement fixture: its id, title,
// recording runtime and narrator.
type statedWork struct {
	id, title, narrator string
	runtime             int
}

// statedPair runs a fixture holding the given works (one recording each, every narrator
// a person of its own) plus any extra files, and returns the ONE title-author cluster it
// must produce.
func statedPair(t *testing.T, works []statedWork, extra map[string]string) Finding {
	t.Helper()
	files := map[string]string{}
	for _, w := range works {
		dir := "works/" + w.id[:2] + "/" + w.id
		files[dir+"/work.json"] = workJSON(t, w.id, w.title)
		files[dir+"/recordings/r.json"] = recJSON(t, "r", w.id, withRuntime(w.runtime), withNarrators(w.narrator))
		files["people/"+w.narrator[:2]+"/"+w.narrator+".json"] = personJSON(t, w.narrator, strings.ToUpper(w.narrator[:1])+w.narrator[1:])
	}
	for k, v := range extra {
		files[k] = v
	}
	rep := runFixture(t, fixture(t, files))
	got := subclassOf(t, rep, ClassWorkDup, dupTitleAuthor)
	if len(got) != 1 {
		t.Fatalf("want one title-author cluster, got %d: %+v", len(got), classOf(t, rep, ClassWorkDup))
	}
	return got[0]
}

func wantAdvisory(t *testing.T, fd Finding, reason string) {
	t.Helper()
	if !fd.Propose.Advisory {
		t.Fatalf("proposed for mechanical merge: %+v", fd.Propose)
	}
	if !strings.Contains(fd.Propose.Reason, reason) {
		t.Errorf("reason = %q, want it to contain %q", fd.Propose.Reason, reason)
	}
}

func wantMechanical(t *testing.T, fd Finding) {
	t.Helper()
	if fd.Propose.Advisory {
		t.Errorf("a correct merge was withheld: %q", fd.Propose.Reason)
	}
}

// OUR VIETNAM WARS: "Volume 2" after the plain record's own title divides one work.
func TestWorkDupVetoesALaterPartOfAPlainTitle(t *testing.T) {
	for _, later := range []string{"Our Vietnam Wars, Volume 2", "Our Vietnam Wars, Pt.2", "Our Vietnam Wars (Vol. 2)"} {
		t.Run(later, func(t *testing.T) {
			fd := statedPair(t, []statedWork{
				{"our-vietnam-wars", "Our Vietnam Wars", "ann-reader", 890},
				{"our-vietnam-wars-later", later, "bob-reader", 948},
			}, nil)
			wantAdvisory(t, fd, "the whole work or its first part")
		})
	}
}

// ...and what it must leave alone: "Volume 1" IS the plain title's first volume, "Book 3"
// after a title is the retailer's series-position convention ("All In, Book 3" beside
// "All In" is one book - the 35 correct merges that declined the general rule), and a
// head that already carries its number restates a series position rather than
// dividing a work.
func TestWorkDupKeepsAPlainTitleBesideAFirstVolumeOrABookPosition(t *testing.T) {
	cases := []struct{ plain, stated string }{
		{"Our Vietnam Wars", "Our Vietnam Wars, Volume 1"},
		{"All In", "All In, Book 3"},
		{"Z-Burbia 2: Parkway To Hell", "Z-Burbia 2: Parkway To Hell, Volume 2"},
		{"Z-Burbia Two: Parkway To Hell", "Z-Burbia Two: Parkway To Hell, Volume 2"},
	}
	for _, c := range cases {
		t.Run(c.stated, func(t *testing.T) {
			fd := statedPair(t, []statedWork{
				{"plain-record", c.plain, "ann-reader", 500},
				{"stated-record", c.stated, "bob-reader", 510},
			}, nil)
			wantMechanical(t, fd)
		})
	}
}

// THE SHADOW WEAVER: "Book 2" is not a part marker, but the catalogue models the stating
// record at position 2 of a series NAMED the plain title - so the plain record is that
// series' name (or its first volume), not this second one.
func TestWorkDupVetoesAPlainTitleThatIsTheSeriesName(t *testing.T) {
	fd := statedPair(t, []statedWork{
		{"the-shadow-weaver", "The Shadow Weaver", "ann-reader", 26},
		{"the-shadow-weaver-book-2", "The Shadow Weaver, Book 2", "bob-reader", 28},
	}, map[string]string{
		"series/th/the-shadow-weaver.json": seriesJSON(t, "the-shadow-weaver", "The Shadow Weaver", "the-shadow-weaver-book-2@2"),
	})
	wantAdvisory(t, fd, "models as the name of its series")
}

// ...but the same series at position 1 is the plain record's own volume, which is the
// duplicate the class exists to find.
func TestWorkDupKeepsAPlainTitleBesideItsSeriesFirstVolume(t *testing.T) {
	fd := statedPair(t, []statedWork{
		{"the-shadow-weaver", "The Shadow Weaver", "ann-reader", 26},
		{"the-shadow-weaver-book-1", "The Shadow Weaver, Book 1", "bob-reader", 27},
	}, map[string]string{
		"series/th/the-shadow-weaver.json": seriesJSON(t, "the-shadow-weaver", "The Shadow Weaver", "the-shadow-weaver-book-1@1"),
	})
	wantMechanical(t, fd)
}

// SECURITY ANALYSIS: two numbered editions are revised texts.
func TestWorkDupVetoesTwoDifferentEditionOrdinals(t *testing.T) {
	fd := statedPair(t, []statedWork{
		{"security-analysis-sixth-edition", "Security Analysis (Sixth Edition)", "ann-reader", 1942},
		{"security-analysis-seventh-edition", "Security Analysis (Seventh Edition)", "bob-reader", 2093},
	}, nil)
	wantAdvisory(t, fd, "two numbered editions")
}

// One stated edition beside the plain title is not a disagreement, and an anniversary
// re-release states no ordinal at all.
func TestWorkDupKeepsAOneSidedEdition(t *testing.T) {
	for _, stated := range []string{"Fifteen Dogs (Tenth Anniversary Edition)", "Fifteen Dogs (Second Edition)"} {
		t.Run(stated, func(t *testing.T) {
			fd := statedPair(t, []statedWork{
				{"fifteen-dogs", "Fifteen Dogs", "ann-reader", 380},
				{"fifteen-dogs-edition", stated, "bob-reader", 400},
			}, nil)
			wantMechanical(t, fd)
		})
	}
}

// NOTES FROM A YOUNG BLACK CHEF and TANGLED: an adapted text on one side only.
func TestWorkDupVetoesAnAdaptedTextOnOneSide(t *testing.T) {
	cases := []struct{ plain, adapted string }{
		{"Notes from a Young Black Chef", "Notes from a Young Black Chef (Adapted for Young Adults)"},
		{"Tangled", "Tangled: The Series"},
	}
	for _, c := range cases {
		t.Run(c.adapted, func(t *testing.T) {
			fd := statedPair(t, []statedWork{
				{"plain-record", c.plain, "ann-reader", 400},
				{"adapted-record", c.adapted, "bob-reader", 410},
			}, nil)
			wantAdvisory(t, fd, "not a second record of the original")
		})
	}
}

// A dramatization is the same work produced as a drama - those merges are correct.
func TestWorkDupKeepsADramatization(t *testing.T) {
	fd := statedPair(t, []statedWork{
		{"the-woman-in-white", "The Woman in White", "ann-reader", 1471},
		{"the-woman-in-white-dramatized", "The Woman in White (Dramatized)", "bob-reader", 1518},
	}, nil)
	wantMechanical(t, fd)
}

// THE FRIEDRICH NIETZSCHE COLLECTION: two generic compilations whose runtimes say they
// are two selections.
func TestWorkDupVetoesTwoCollectionsOfDifferentSelections(t *testing.T) {
	fd := statedPair(t, []statedWork{
		{"friedrich-nietzsche-collection", "Friedrich Nietzsche Collection", "ann-reader", 2406},
		{"the-friedrich-nietzsche-collection", "The Friedrich Nietzsche Collection", "bob-reader", 3071},
	}, nil)
	wantAdvisory(t, fd, "two selections")
}

// ...while two records of ONE collection, within the importer's same-production
// tolerance, still merge.
func TestWorkDupKeepsTwoRecordsOfOneCollection(t *testing.T) {
	fd := statedPair(t, []statedWork{
		{"friedrich-nietzsche-collection", "Friedrich Nietzsche Collection", "ann-reader", 2406},
		{"the-friedrich-nietzsche-collection", "The Friedrich Nietzsche Collection", "bob-reader", 2450},
	}, nil)
	wantMechanical(t, fd)
}

// RAPID EXTREME WEIGHT LOSS HYPNOSIS: a "(2 in 1)" bundle carries no collection word, so
// it reached the mechanical path until IsCollection read the announcement; the existing
// one-sided collection veto then refuses it.
func TestWorkDupVetoesAnNInOneBundleBesideItsSingleTitle(t *testing.T) {
	fd := statedPair(t, []statedWork{
		{"rapid-extreme-weight-loss-hypnosis", "Rapid Extreme Weight Loss Hypnosis for Women", "ann-reader", 611},
		{"rapid-extreme-weight-loss-hypnosis-2-in-1", "Rapid Extreme Weight Loss Hypnosis for Women (2 in 1)", "bob-reader", 692},
	}, nil)
	wantAdvisory(t, fd, "announce a collection")
}

// A DECORATED plain twin is still the plain title: its key is its cleaned title, not the
// raw one, or the veto would skip the commonest W-DUP shape.
func TestWorkDupVetoesALaterPartBesideADecoratedPlainTwin(t *testing.T) {
	fd := statedPair(t, []statedWork{
		{"our-vietnam-wars-unabridged", "Our Vietnam Wars (Unabridged)", "ann-reader", 890},
		{"our-vietnam-wars-volume-2", "Our Vietnam Wars, Volume 2", "bob-reader", 948},
	}, nil)
	wantAdvisory(t, fd, "the whole work or its first part")
}

// A number that merely appears in the head is not a restatement: "2 States, Part 2" is
// part 2 of "2 States".
func TestWorkDupVetoesALaterPartWhoseHeadHoldsANumber(t *testing.T) {
	fd := statedPair(t, []statedWork{
		{"two-states", "2 States", "ann-reader", 500},
		{"two-states-part-2", "2 States, Part 2", "bob-reader", 510},
	}, nil)
	wantAdvisory(t, fd, "the whole work or its first part")
}

// The stand-down reads the series the stated volume refers to: a plain member placed at
// position 2 of an UNRELATED series says nothing about "Volume 2" of this title.
func TestWorkDupStandDownIsScopedToTheStatedSeries(t *testing.T) {
	fd := statedPair(t, []statedWork{
		{"our-vietnam-wars", "Our Vietnam Wars", "ann-reader", 890},
		{"our-vietnam-wars-volume-2", "Our Vietnam Wars, Volume 2", "bob-reader", 948},
	}, map[string]string{
		"works/fi/first-war/work.json":         workJSON(t, "first-war", "First War"),
		"works/fi/first-war/recordings/r.json": recJSON(t, "r", "first-war"),
		"series/hi/history-of-wars.json":       seriesJSON(t, "history-of-wars", "History of Wars", "first-war@1", "our-vietnam-wars@2"),
	})
	wantAdvisory(t, fd, "the whole work or its first part")
}
