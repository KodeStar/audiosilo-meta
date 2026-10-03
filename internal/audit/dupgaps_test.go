package audit

import (
	"reflect"
	"strings"
	"testing"
)

// The three W-DUP widenings and the relaxed disjoint-series veto, each with the shape it
// was built for and the shape it must still refuse.

// royalRanger seeds the Ranger's Apprentice: The Royal Ranger shape: a plain-titled work,
// and a second record of it whose retailer title leads with the subseries' post-colon
// tail. Both records are Jane Doe's; seriesAuthor is who wrote the work the subseries
// holds, which is the only thing that varies between the two tests.
func royalRanger(t *testing.T, seriesAuthor string) map[string]string {
	t.Helper()
	return fixture(t, map[string]string{
		"works/re/the-red-fox-clan/work.json":         workJSON(t, "the-red-fox-clan", "The Red Fox Clan"),
		"works/re/the-red-fox-clan/recordings/a.json": recJSON(t, "a", "the-red-fox-clan", withRuntime(676)),
		"works/ro/the-royal-ranger-the-red-fox-clan/work.json": workJSON(t, "the-royal-ranger-the-red-fox-clan",
			"The Royal Ranger: The Red Fox Clan"),
		"works/ro/the-royal-ranger-the-red-fox-clan/recordings/b.json": recJSON(t, "b", "the-royal-ranger-the-red-fox-clan",
			withRuntime(676)),
		"works/ro/the-royal-ranger/work.json":         workJSON(t, "the-royal-ranger", "The Royal Ranger", withAuthors(seriesAuthor)),
		"works/ro/the-royal-ranger/recordings/c.json": recJSON(t, "c", "the-royal-ranger", withRuntime(600)),
		"series/ra/rangers-apprentice-the-royal-ranger.json": seriesJSON(t, "rangers-apprentice-the-royal-ranger",
			"Ranger's Apprentice: The Royal Ranger", "the-royal-ranger@1"),
		"people/jo/john-roe.json": personJSON(t, "john-roe", "John Roe"),
	})
}

func TestWorkDupMeetsThroughASubseriesTail(t *testing.T) {
	rep := runFixture(t, royalRanger(t, "jane-doe"))
	got := subclassOf(t, rep, ClassWorkDup, dupTitleAuthor)
	if len(got) != 1 {
		t.Fatalf("want one cluster, got %d: %+v", len(got), classOf(t, rep, ClassWorkDup))
	}
	if want := []string{"the-red-fox-clan", "the-royal-ranger-the-red-fox-clan"}; !reflect.DeepEqual(workIDs(got[0]), want) {
		t.Errorf("cluster = %v, want %v", workIDs(got[0]), want)
	}
	if got[0].Propose.Advisory {
		t.Errorf("the subseries-tail pair was withheld: %s", got[0].Propose.Reason)
	}
	if !strings.Contains(strings.Join(got[0].Notes, " "), viaSeriesTail) {
		t.Errorf("the cluster does not say it rests on a subseries tail: %v", got[0].Notes)
	}
}

// The same two records, but the series whose tail the title leads with is somebody
// else's: "The Royal Ranger" is two ordinary words, and only a shared author makes it
// this book's subseries.
func TestWorkDupTailNeedsASharedAuthor(t *testing.T) {
	rep := runFixture(t, royalRanger(t, "john-roe"))
	for _, f := range classOf(t, rep, ClassWorkDup) {
		t.Errorf("a subseries tail was read for a work sharing no author with the series: %v (%s)", workIDs(f), f.Key)
	}
}

// armour seeds a UK and a US title of one book.
func armour(t *testing.T, authors ...string) map[string]string {
	t.Helper()
	return fixture(t, map[string]string{
		"works/th/the-armor-of-light/work.json":         workJSON(t, "the-armor-of-light", "The Armor of Light"),
		"works/th/the-armor-of-light/recordings/a.json": recJSON(t, "a", "the-armor-of-light", withRuntime(1299)),
		"works/th/the-armour-of-light/work.json": workJSON(t, "the-armour-of-light", "The Armour of Light",
			withAuthors(authors...)),
		"works/th/the-armour-of-light/recordings/b.json": recJSON(t, "b", "the-armour-of-light", withRuntime(1299)),
		"series/ki/kingsbridge.json":                     seriesJSON(t, "kingsbridge", "Kingsbridge", "the-armour-of-light@4"),
		"people/jo/john-roe.json":                        personJSON(t, "john-roe", "John Roe"),
	})
}

func TestWorkDupMeetsAUSUKSpelling(t *testing.T) {
	rep := runFixture(t, armour(t, "jane-doe"))
	got := subclassOf(t, rep, ClassWorkDup, dupTitleAuthor)
	if len(got) != 1 {
		t.Fatalf("want one cluster, got %d: %+v", len(got), classOf(t, rep, ClassWorkDup))
	}
	if want := []string{"the-armor-of-light", "the-armour-of-light"}; !reflect.DeepEqual(workIDs(got[0]), want) {
		t.Errorf("cluster = %v, want %v", workIDs(got[0]), want)
	}
	// The stripped-series soundness condition is asked in the variant key's own terms:
	// under the plain key the two titles never agree, which would withhold every pair.
	if got[0].Propose.Advisory {
		t.Errorf("the spelling-variant pair was withheld: %s", got[0].Propose.Reason)
	}
}

// The variant key is a TITLE key: identity is still the author rule's.
func TestWorkDupSpellingVariantStillNeedsTheAuthors(t *testing.T) {
	rep := runFixture(t, armour(t, "john-roe"))
	for _, f := range classOf(t, rep, ClassWorkDup) {
		t.Errorf("two authors' books met on a spelling variant: %v", workIDs(f))
	}
}

// disjointPair seeds two records of one book, each modeled in its own spelling of one
// series, at the given positions and series names.
func disjointPair(t *testing.T, nameA, posA, nameB, posB string) map[string]string {
	t.Helper()
	return fixture(t, map[string]string{
		"works/aw/awakening-a/work.json":         workJSON(t, "awakening-a", "Awakening"),
		"works/aw/awakening-a/recordings/a.json": recJSON(t, "a", "awakening-a"),
		"works/aw/awakening-b/work.json":         workJSON(t, "awakening-b", "Awakening."),
		"works/aw/awakening-b/recordings/b.json": recJSON(t, "b", "awakening-b"),
		"series/al/alpha.json":                   seriesJSON(t, "alpha", nameA, "awakening-a@"+posA),
		"series/be/beta.json":                    seriesJSON(t, "beta", nameB, "awakening-b@"+posB),
	})
}

func TestDisjointVetoReadsSameKeySameSlotAsShared(t *testing.T) {
	for name, c := range map[string]struct {
		nameA, posA, nameB, posB string
		vetoed                   bool
	}{
		// One series spelled twice, one place in its order: not disjoint.
		"article and catalogue suffix": {"The Kingsbridge Novels", "4", "Kingsbridge", "04", false},
		"one-sided ordering note":      {"Ranger's Apprentice", "13", "Ranger's Apprentice (published order)", "13", false},
		// The same name at two slots is two places.
		"another slot": {"Ranger's Apprentice", "14", "Ranger's Apprentice (published order)", "13", true},
		// Two DIFFERENT decorations are two products, which SeriesKey alone cannot see:
		// the Cantonese and Mandarin courses, lesson for lesson at the same slots.
		"two decorations": {"Pimsleur Chinese (Cantonese)", "1", "Pimsleur Chinese (Mandarin)", "1", true},
		"two names":       {"Alpha Cycle", "1", "Beta Cycle", "1", true},
	} {
		t.Run(name, func(t *testing.T) {
			rep := runFixture(t, disjointPair(t, c.nameA, c.posA, c.nameB, c.posB))
			got := subclassOf(t, rep, ClassWorkDup, dupTitleAuthor)
			if len(got) != 1 {
				t.Fatalf("want one cluster, got %d", len(got))
			}
			vetoed := strings.Contains(got[0].Propose.Reason, "entirely different series")
			if vetoed != c.vetoed {
				t.Errorf("disjoint-series veto = %v, want %v (%s)", vetoed, c.vetoed, got[0].Propose.Reason)
			}
		})
	}
}
