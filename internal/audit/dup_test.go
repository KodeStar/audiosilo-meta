package audit

import (
	"reflect"
	"strings"
	"testing"
)

// The calibration pair, reduced to a fixture: one work modeled as volume 3 of its
// series with a clean title, and a second record of the same book whose retailer
// title spells the series and the volume out. They must cluster.
func TestWorkDupFindsADecoratedTitleAgainstItsModeledVolume(t *testing.T) {
	rep := runFixture(t, map[string]string{
		"works/ha/hammered/work.json":                    workJSON(t, "hammered", "Hammered"),
		"works/ha/hammered/recordings/luke-2011.json":    recJSON(t, "luke-2011", "hammered", withRuntime(576)),
		"works/ha/hammered-book-3/work.json":             workJSON(t, "hammered-book-3", "Hammered: The Druid Tales, Book 3"),
		"works/ha/hammered-book-3/recordings/chris.json": recJSON(t, "chris", "hammered-book-3", withRuntime(626)),
		"people/ja/jane-doe.json":                        personJSON(t, "jane-doe", "Jane Doe"),
		"people/na/nate-narrator.json":                   personJSON(t, "nate-narrator", "Nate Narrator"),
		"series/dr/druid-tales.json":                     seriesJSON(t, "druid-tales", "The Druid Tales", "hammered@3"),
	})
	got := subclassOf(t, rep, ClassWorkDup, dupTitleAuthor)
	if len(got) != 1 {
		t.Fatalf("want one title-author cluster, got %d: %+v", len(got), got)
	}
	if want := []string{"hammered", "hammered-book-3"}; !reflect.DeepEqual(workIDs(got[0]), want) {
		t.Errorf("cluster = %v, want %v", workIDs(got[0]), want)
	}
	// The modeled member is the canonical one: it is the record that already has a
	// series membership and the cleaner title.
	if got[0].Propose.Target != "hammered" {
		t.Errorf("canonical = %q, want %q", got[0].Propose.Target, "hammered")
	}
	if !strings.Contains(strings.Join(got[0].Notes, " "), "embedded-series") {
		t.Errorf("the cluster does not say it rests on a series name embedded in a title: %v", got[0].Notes)
	}
}

// The genre-subtitle variant: one work, two records, one of them carrying a
// retailer marketing subtitle. It turns on "dark", which the server's narrower
// vocabulary does not hold - see wideGenreFluff.
func TestWorkDupFindsAGenreSubtitleVariant(t *testing.T) {
	rep := runFixture(t, map[string]string{
		"works/se/seeds-omnibus/work.json":                     workJSON(t, "seeds-omnibus", "Seeds of Chaos Omnibus"),
		"works/se/seeds-omnibus/recordings/nat-a.json":         recJSON(t, "nat-a", "seeds-omnibus", withRuntime(1890)),
		"works/se/seeds-omnibus-gamelit/work.json":             workJSON(t, "seeds-omnibus-gamelit", "Seeds of Chaos Omnibus: A GameLit Dark Adventure Series"),
		"works/se/seeds-omnibus-gamelit/recordings/nat-b.json": recJSON(t, "nat-b", "seeds-omnibus-gamelit", withRuntime(1890)),
		"people/ja/jane-doe.json":                              personJSON(t, "jane-doe", "Jane Doe"),
		"people/na/nate-narrator.json":                         personJSON(t, "nate-narrator", "Nate Narrator"),
		"series/se/seeds-of-chaos.json":                        seriesJSON(t, "seeds-of-chaos", "Seeds of Chaos", "seeds-omnibus-gamelit@1-2"),
	})
	got := subclassOf(t, rep, ClassWorkDup, dupTitleAuthor)
	if len(got) != 1 {
		t.Fatalf("want one cluster, got %d: %+v", len(got), got)
	}
	if want := []string{"seeds-omnibus", "seeds-omnibus-gamelit"}; !reflect.DeepEqual(workIDs(got[0]), want) {
		t.Errorf("cluster = %v, want %v", workIDs(got[0]), want)
	}
	// The key is the cleaned title's own. The series name is NOT excised from the
	// middle of a title any more (titlerule.Clean is boundary-anchored), so the
	// residual is "Seeds of Chaos Omnibus" rather than the bare "Omnibus" that named
	// no book and had to be keyed by its series to stay apart from other omnibuses -
	// see TestWorkDupKeepsTwoOmnibusesOfDifferentSeriesApart, which now holds on the
	// titles alone.
	if !strings.Contains(got[0].Key, "seedsofchaosomnibus") {
		t.Errorf("key = %q, want the cleaned title's own key", got[0].Key)
	}
}

// THE FALSE MERGE THIS CLASS MADE, pinned as a fixture: two travel guides to two
// different cities, by one author, each naming a catalogued series in its own title.
// The clustering key excised EVERY occurrence of the matched series name, so both
// reduced to "The Best of for Short Stay Travel" - one key, and a repair wave proposed
// the merge NON-ADVISORY. The strip is anchored to a title boundary now
// (titlerule.Clean), so the leading "New Orleans: " segment comes off and the city the
// book is about stays in the key.
func TestWorkDupDoesNotClusterTwoCitiesOfOneGuideSeries(t *testing.T) {
	rep := runFixture(t, map[string]string{
		"works/ne/new-orleans-guide/work.json": workJSON(t, "new-orleans-guide",
			"New Orleans: The Best of New Orleans for Short Stay Travel"),
		"works/ne/new-orleans-guide/recordings/sangita-2018.json": recJSON(t, "sangita-2018", "new-orleans-guide",
			withNarrators("sangita-chauhan"), withRuntime(95)),
		"works/ne/new-york-guide/work.json": workJSON(t, "new-york-guide",
			"New York: The Best of New York for Short Stay Travel"),
		"works/ne/new-york-guide/recordings/gina-2018.json": recJSON(t, "gina-2018", "new-york-guide",
			withNarrators("gina-stack"), withRuntime(67)),
		// The two series whose names the titles spell out. Neither guide is a member -
		// the derivation reads the name off the TITLE, which is the weaker claim and
		// the one the excision was applied to.
		"works/qu/quarter-to-midnight/work.json":         workJSON(t, "quarter-to-midnight", "Quarter to Midnight"),
		"works/qu/quarter-to-midnight/recordings/a.json": recJSON(t, "a", "quarter-to-midnight"),
		"works/at/a-table-for-three/work.json":           workJSON(t, "a-table-for-three", "A Table for Three"),
		"works/at/a-table-for-three/recordings/b.json":   recJSON(t, "b", "a-table-for-three"),
		"series/ne/new-orleans.json":                     seriesJSON(t, "new-orleans", "New Orleans", "quarter-to-midnight@1"),
		"series/ne/new-york.json":                        seriesJSON(t, "new-york", "New York", "a-table-for-three@1"),
		"people/ja/jane-doe.json":                        personJSON(t, "jane-doe", "Jane Doe"),
		"people/na/nate-narrator.json":                   personJSON(t, "nate-narrator", "Nate Narrator"),
		"people/sa/sangita-chauhan.json":                 personJSON(t, "sangita-chauhan", "Sangita Chauhan"),
		"people/gi/gina-stack.json":                      personJSON(t, "gina-stack", "Gina Stack"),
	})
	for _, f := range classOf(t, rep, ClassWorkDup) {
		t.Errorf("two different cities' guides were clustered: %v (key %q, %s)", workIDs(f), f.Key, f.Action)
	}
}

func TestWorkDupKeepsTwoOmnibusesOfDifferentSeriesApart(t *testing.T) {
	rep := runFixture(t, map[string]string{
		"works/al/alpha-omnibus/work.json":         workJSON(t, "alpha-omnibus", "Alpha Saga Omnibus"),
		"works/al/alpha-omnibus/recordings/a.json": recJSON(t, "a", "alpha-omnibus"),
		"works/be/beta-omnibus/work.json":          workJSON(t, "beta-omnibus", "Beta Cycle Omnibus"),
		"works/be/beta-omnibus/recordings/b.json":  recJSON(t, "b", "beta-omnibus"),
		"people/ja/jane-doe.json":                  personJSON(t, "jane-doe", "Jane Doe"),
		"people/na/nate-narrator.json":             personJSON(t, "nate-narrator", "Nate Narrator"),
		"series/al/alpha-saga.json":                seriesJSON(t, "alpha-saga", "Alpha Saga", "alpha-omnibus@1"),
		"series/be/beta-cycle.json":                seriesJSON(t, "beta-cycle", "Beta Cycle", "beta-omnibus@1"),
	})
	for _, f := range classOf(t, rep, ClassWorkDup) {
		t.Errorf("two omnibuses of different series were clustered: %v (%s)", workIDs(f), f.Key)
	}
}

func TestWorkDupNeverClustersAcrossLanguages(t *testing.T) {
	rep := runFixture(t, map[string]string{
		"works/th/the-hobbit-en/work.json":          workJSON(t, "the-hobbit-en", "The Hobbit"),
		"works/th/the-hobbit-en/recordings/en.json": recJSON(t, "en", "the-hobbit-en"),
		"works/th/the-hobbit-de/work.json":          workJSON(t, "the-hobbit-de", "The Hobbit", withLanguage("de")),
		"works/th/the-hobbit-de/recordings/de.json": recJSON(t, "de", "the-hobbit-de"),
		"people/ja/jane-doe.json":                   personJSON(t, "jane-doe", "Jane Doe"),
		"people/na/nate-narrator.json":              personJSON(t, "nate-narrator", "Nate Narrator"),
	})
	for _, f := range classOf(t, rep, ClassWorkDup) {
		t.Errorf("a translation was proposed for merge: %v", workIDs(f))
	}
}

// A language-split key with two works on ONE side still clusters that side, and
// says what it was split away from.
func TestWorkDupClustersWithinALanguageAndNamesTheSplit(t *testing.T) {
	rep := runFixture(t, map[string]string{
		"works/th/hobbit-en-a/work.json":         workJSON(t, "hobbit-en-a", "The Hobbit"),
		"works/th/hobbit-en-a/recordings/a.json": recJSON(t, "a", "hobbit-en-a"),
		"works/th/hobbit-en-b/work.json":         workJSON(t, "hobbit-en-b", "The Hobbit (Unabridged)"),
		"works/th/hobbit-en-b/recordings/b.json": recJSON(t, "b", "hobbit-en-b"),
		"works/th/hobbit-de/work.json":           workJSON(t, "hobbit-de", "The Hobbit", withLanguage("de")),
		"works/th/hobbit-de/recordings/d.json":   recJSON(t, "d", "hobbit-de"),
		"people/ja/jane-doe.json":                personJSON(t, "jane-doe", "Jane Doe"),
		"people/na/nate-narrator.json":           personJSON(t, "nate-narrator", "Nate Narrator"),
	})
	got := subclassOf(t, rep, ClassWorkDup, dupTitleAuthor)
	if len(got) != 1 {
		t.Fatalf("want one cluster, got %d: %+v", len(got), got)
	}
	if want := []string{"hobbit-en-a", "hobbit-en-b"}; !reflect.DeepEqual(workIDs(got[0]), want) {
		t.Errorf("cluster = %v, want %v", workIDs(got[0]), want)
	}
	if !strings.Contains(strings.Join(got[0].Notes, " "), "language-split") {
		t.Errorf("the cluster does not name the language split: %v", got[0].Notes)
	}
}

func TestWorkDupNeverClustersDifferentAuthors(t *testing.T) {
	rep := runFixture(t, map[string]string{
		"works/dr/dracula-a/work.json":         workJSON(t, "dracula-a", "Dracula"),
		"works/dr/dracula-a/recordings/a.json": recJSON(t, "a", "dracula-a"),
		"works/dr/dracula-b/work.json":         workJSON(t, "dracula-b", "Dracula", withAuthors("bram-stoker")),
		"works/dr/dracula-b/recordings/b.json": recJSON(t, "b", "dracula-b"),
		"people/ja/jane-doe.json":              personJSON(t, "jane-doe", "Jane Doe"),
		"people/br/bram-stoker.json":           personJSON(t, "bram-stoker", "Bram Stoker"),
		"people/na/nate-narrator.json":         personJSON(t, "nate-narrator", "Nate Narrator"),
	})
	for _, f := range classOf(t, rep, ClassWorkDup) {
		t.Errorf("two authors' books were clustered: %v", workIDs(f))
	}
}

// A runtime difference within the veto ratio is a different PRODUCTION, which is
// two recordings of one work - so the merge proposal stands.
//
// The two sides carry DIFFERENT narrators, which is what a second production has (and what
// a dramatization certainly has). That is load-bearing for what this test is about: the
// unexplained-same-narrator-gap veto would otherwise speak first, and this fixture exists
// to pin the RATIO rung's own boundary.
func TestWorkDupAllowsARuntimeDifferenceWithinTheRatio(t *testing.T) {
	rep := runFixture(t, map[string]string{
		"works/fe/fermentation/work.json":            workJSON(t, "fermentation", "Fermentation"),
		"works/fe/fermentation/recordings/solo.json": recJSON(t, "solo", "fermentation", withRuntime(720)),
		"works/fe/fermentation-dramatized/work.json": workJSON(t, "fermentation-dramatized", "Fermentation (Dramatized Adaptation)"),
		"works/fe/fermentation-dramatized/recordings/c.json": recJSON(t, "c", "fermentation-dramatized",
			withRuntime(503), withNarrators("other-narrator")),
		"people/ja/jane-doe.json":       personJSON(t, "jane-doe", "Jane Doe"),
		"people/na/nate-narrator.json":  personJSON(t, "nate-narrator", "Nate Narrator"),
		"people/ot/other-narrator.json": personJSON(t, "other-narrator", "Other Narrator"),
	})
	got := subclassOf(t, rep, ClassWorkDup, dupTitleAuthor)
	if len(got) != 1 {
		t.Fatalf("want one cluster, got %d", len(got))
	}
	if got[0].Propose.Advisory || got[0].Propose.Op != OpMergeWorks {
		t.Errorf("a 1.4x runtime difference must not veto the merge: %+v", got[0].Propose)
	}
}

// Beyond the ratio it is not two productions of one book - it is a collection beside
// a volume, or two different books - so the merge is withheld and the reason names
// the numbers.
func TestWorkDupVetoesAnImpossibleRuntimeRatio(t *testing.T) {
	rep := runFixture(t, map[string]string{
		"works/co/complete/work.json":             workJSON(t, "complete", "Wandering Inn"),
		"works/co/complete/recordings/long.json":  recJSON(t, "long", "complete", withRuntime(3843)),
		"works/co/volume-one/work.json":           workJSON(t, "volume-one", "Wandering Inn."),
		"works/co/volume-one/recordings/one.json": recJSON(t, "one", "volume-one", withRuntime(409)),
		"people/ja/jane-doe.json":                 personJSON(t, "jane-doe", "Jane Doe"),
		"people/na/nate-narrator.json":            personJSON(t, "nate-narrator", "Nate Narrator"),
	})
	got := subclassOf(t, rep, ClassWorkDup, dupTitleAuthor)
	if len(got) != 1 {
		t.Fatalf("want one cluster, got %d: %+v", len(got), classOf(t, rep, ClassWorkDup))
	}
	if !got[0].Propose.Advisory {
		t.Error("a 9x runtime ratio must withhold the merge")
	}
	if !strings.Contains(got[0].Propose.Reason, "runtimes differ") {
		t.Errorf("reason = %q, want the runtimes named", got[0].Propose.Reason)
	}
}

func TestWorkDupIsSilentOnARuntimeGapUnderTheThreshold(t *testing.T) {
	rep := runFixture(t, map[string]string{
		"works/ha/hammered/work.json":                    workJSON(t, "hammered", "Hammered"),
		"works/ha/hammered/recordings/luke.json":         recJSON(t, "luke", "hammered", withRuntime(576)),
		"works/ha/hammered-book-3/work.json":             workJSON(t, "hammered-book-3", "Hammered: The Druid Tales, Book 3"),
		"works/ha/hammered-book-3/recordings/chris.json": recJSON(t, "chris", "hammered-book-3", withRuntime(600)),
		"people/ja/jane-doe.json":                        personJSON(t, "jane-doe", "Jane Doe"),
		"people/na/nate-narrator.json":                   personJSON(t, "nate-narrator", "Nate Narrator"),
		"series/dr/druid-tales.json":                     seriesJSON(t, "druid-tales", "The Druid Tales", "hammered@3"),
	})
	got := subclassOf(t, rep, ClassWorkDup, dupTitleAuthor)
	if len(got) != 1 {
		t.Fatalf("want one cluster, got %d", len(got))
	}
	if strings.Contains(strings.Join(got[0].Notes, " "), "runtime gap") {
		t.Errorf("a 4%% gap was reported as a runtime gap: %v", got[0].Notes)
	}
}

// Volumes of one multi-volume work all clean to one title. They are siblings, not
// duplicates, and their own titles say so - so the cluster is reported under its
// own subclass with NO merge proposed.
func TestWorkDupWithholdsAMergeWhenTheTitlesStateDifferentVolumes(t *testing.T) {
	rep := runFixture(t, map[string]string{
		"works/de/decline/work.json":             workJSON(t, "decline", "The Decline and Fall of Rome"),
		"works/de/decline/recordings/whole.json": recJSON(t, "whole", "decline"),
		"works/de/decline-v1/work.json":          workJSON(t, "decline-v1", "The Decline and Fall of Rome, Volume 1"),
		"works/de/decline-v1/recordings/v1.json": recJSON(t, "v1", "decline-v1"),
		"works/de/decline-v2/work.json":          workJSON(t, "decline-v2", "The Decline and Fall of Rome, Volume 2"),
		"works/de/decline-v2/recordings/v2.json": recJSON(t, "v2", "decline-v2"),
		"people/ja/jane-doe.json":                personJSON(t, "jane-doe", "Jane Doe"),
		"people/na/nate-narrator.json":           personJSON(t, "nate-narrator", "Nate Narrator"),
	})
	if got := subclassOf(t, rep, ClassWorkDup, dupTitleAuthor); len(got) != 0 {
		t.Errorf("volumes were proposed for merge: %+v", got)
	}
	got := subclassOf(t, rep, ClassWorkDup, dupVolumeConflict)
	if len(got) != 1 {
		t.Fatalf("want one volume-conflict record, got %d", len(got))
	}
	if got[0].Propose.Target != "" {
		t.Errorf("a volume conflict must name no canonical member, got %q", got[0].Propose.Target)
	}
	if !strings.Contains(strings.Join(got[0].Notes, " "), "volume numbers stated") {
		t.Errorf("the conflicting volume numbers are not stated: %v", got[0].Notes)
	}
}

// The second duplicate shape: a series-less work whose title states a series and a
// volume that the series already fills with a DIFFERENT work, where the residual
// titles do not agree.
func TestWorkDupFindsASeriesVolumeCollision(t *testing.T) {
	rep := runFixture(t, map[string]string{
		"works/fo/founding/work.json":                   workJSON(t, "founding", "The Founding"),
		"works/fo/founding/recordings/a.json":           recJSON(t, "a", "founding"),
		"works/ch/chaos-seeds-book-1/work.json":         workJSON(t, "chaos-seeds-book-1", "Chaos Seeds, Book 1"),
		"works/ch/chaos-seeds-book-1/recordings/b.json": recJSON(t, "b", "chaos-seeds-book-1"),
		"people/ja/jane-doe.json":                       personJSON(t, "jane-doe", "Jane Doe"),
		"people/na/nate-narrator.json":                  personJSON(t, "nate-narrator", "Nate Narrator"),
		"series/ch/chaos-seeds.json":                    seriesJSON(t, "chaos-seeds", "Chaos Seeds", "founding@1"),
	})
	got := subclassOf(t, rep, ClassWorkDup, dupSeriesVolume)
	if len(got) != 1 {
		t.Fatalf("want one series-volume record, got %d: %+v", len(got), got)
	}
	if want := []string{"chaos-seeds-book-1", "founding"}; !reflect.DeepEqual(workIDs(got[0]), want) {
		t.Errorf("pair = %v, want %v", workIDs(got[0]), want)
	}
	if got[0].Propose.Target != "founding" {
		t.Errorf("canonical = %q, want the modeled member", got[0].Propose.Target)
	}
}

// A pair the title-author key already clustered is not reported twice.
func TestWorkDupDoesNotReportOnePairTwice(t *testing.T) {
	rep := runFixture(t, map[string]string{
		"works/ha/hammered/work.json":                    workJSON(t, "hammered", "Hammered"),
		"works/ha/hammered/recordings/luke.json":         recJSON(t, "luke", "hammered"),
		"works/ha/hammered-book-3/work.json":             workJSON(t, "hammered-book-3", "Hammered: The Druid Tales, Book 3"),
		"works/ha/hammered-book-3/recordings/chris.json": recJSON(t, "chris", "hammered-book-3"),
		"people/ja/jane-doe.json":                        personJSON(t, "jane-doe", "Jane Doe"),
		"people/na/nate-narrator.json":                   personJSON(t, "nate-narrator", "Nate Narrator"),
		"series/dr/druid-tales.json":                     seriesJSON(t, "druid-tales", "The Druid Tales", "hammered@3"),
	})
	if got := subclassOf(t, rep, ClassWorkDup, dupSeriesVolume); len(got) != 0 {
		t.Errorf("the pair was reported a second time as series-volume: %+v", got)
	}
}

func TestWorkDupIsSilentOnACleanCatalogue(t *testing.T) {
	rep := runFixture(t, map[string]string{
		"works/ho/hounded/work.json":         workJSON(t, "hounded", "Hounded"),
		"works/ho/hounded/recordings/a.json": recJSON(t, "a", "hounded"),
		"works/he/hexed/work.json":           workJSON(t, "hexed", "Hexed"),
		"works/he/hexed/recordings/b.json":   recJSON(t, "b", "hexed"),
		"people/ja/jane-doe.json":            personJSON(t, "jane-doe", "Jane Doe"),
		"people/na/nate-narrator.json":       personJSON(t, "nate-narrator", "Nate Narrator"),
		"series/dr/druid-tales.json":         seriesJSON(t, "druid-tales", "The Druid Tales", "hounded@1", "hexed@2"),
	})
	if got := classOf(t, rep, ClassWorkDup); len(got) != 0 {
		t.Errorf("a clean catalogue produced %d duplicate findings: %+v", len(got), got)
	}
}

func TestCanonicalMemberPrefersTheModeledRecord(t *testing.T) {
	rep := runFixture(t, map[string]string{
		// Neither is in a series; the one carrying a sidecar wins, then recordings.
		"works/pl/plain/work.json":                    workJSON(t, "plain", "Plain Story"),
		"works/pl/plain/recordings/a.json":            recJSON(t, "a", "plain"),
		"works/pl/plain/characters.json":              charactersJSON(t, "plain", "someone"),
		"works/pl/plain-unabridged/work.json":         workJSON(t, "plain-unabridged", "Plain Story (Unabridged)"),
		"works/pl/plain-unabridged/recordings/b.json": recJSON(t, "b", "plain-unabridged"),
		"works/pl/plain-unabridged/recordings/c.json": recJSON(t, "c", "plain-unabridged"),
		"people/ja/jane-doe.json":                     personJSON(t, "jane-doe", "Jane Doe"),
		"people/na/nate-narrator.json":                personJSON(t, "nate-narrator", "Nate Narrator"),
	})
	got := subclassOf(t, rep, ClassWorkDup, dupTitleAuthor)
	if len(got) != 1 {
		t.Fatalf("want one cluster, got %d", len(got))
	}
	// The sidecar outranks the extra recording: a spoiler layer is the expensive
	// thing to re-point.
	if got[0].Propose.Target != "plain" {
		t.Errorf("canonical = %q, want the record carrying the sidecar", got[0].Propose.Target)
	}
}
