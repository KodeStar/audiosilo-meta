package audit

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// The three W-DUP widenings and the relaxed disjoint-series veto, each with the shape it
// was built for and the shape it must still refuse.

// recordPair seeds the shape every widening here is about: two records of one book,
// a plain one and a second whose title the widening has to see through (with
// otherOpts on its work record), at one runtime, plus the case's own files.
func recordPair(t *testing.T, plainID, plainTitle, otherID, otherTitle string, runtime int,
	otherOpts []testpack.WorkOpt, extra map[string]string) map[string]string {
	t.Helper()
	files := fixture(t, map[string]string{
		"works/aa/" + plainID + "/work.json":         workJSON(t, plainID, plainTitle),
		"works/aa/" + plainID + "/recordings/a.json": recJSON(t, "a", plainID, withRuntime(runtime)),
		"works/bb/" + otherID + "/work.json":         workJSON(t, otherID, otherTitle, otherOpts...),
		"works/bb/" + otherID + "/recordings/b.json": recJSON(t, "b", otherID, withRuntime(runtime)),
		"people/jo/john-roe.json":                    personJSON(t, "john-roe", "John Roe"),
	})
	for k, v := range extra {
		files[k] = v
	}
	return files
}

// royalRanger seeds the Ranger's Apprentice: The Royal Ranger shape: a plain-titled work,
// and a second record of it whose retailer title leads with the subseries' post-colon
// tail. Both records are Jane Doe's; seriesAuthor is who wrote the work the subseries
// holds, which is the only thing that varies between the two tests.
func royalRanger(t *testing.T, seriesAuthor string) map[string]string {
	t.Helper()
	return recordPair(t, "the-red-fox-clan", "The Red Fox Clan",
		"the-royal-ranger-the-red-fox-clan", "The Royal Ranger: The Red Fox Clan", 676, nil, map[string]string{
			"works/ro/the-royal-ranger/work.json":         workJSON(t, "the-royal-ranger", "The Royal Ranger", withAuthors(seriesAuthor)),
			"works/ro/the-royal-ranger/recordings/c.json": recJSON(t, "c", "the-royal-ranger", withRuntime(600)),
			"series/ra/rangers-apprentice-the-royal-ranger.json": seriesJSON(t, "rangers-apprentice-the-royal-ranger",
				"Ranger's Apprentice: The Royal Ranger", "the-royal-ranger@1"),
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

// armour seeds a US and a UK title of one book; authors credits the UK record.
func armour(t *testing.T, authors ...string) map[string]string {
	t.Helper()
	return recordPair(t, "the-armor-of-light", "The Armor of Light", "the-armour-of-light", "The Armour of Light", 1299,
		[]testpack.WorkOpt{withAuthors(authors...)}, map[string]string{
			"series/ki/kingsbridge.json": seriesJSON(t, "kingsbridge", "Kingsbridge", "the-armour-of-light@4"),
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

// The soundness veto reads each JOIN's own derivations. In the closed cluster a work
// carries one derivation, which need not be the one a pair met on; here the two
// members of the join met on a key each reached by shedding a different series name,
// while the cluster-level derivations (own-series, keyed apart) would never compare
// them - the veto must still fire.
func TestStrippedSeriesVetoReadsEachJoinsDerivations(t *testing.T) {
	a := &model.Work{ID: "cold-war-history", Title: "Cold War: A History from Beginning to End"}
	b := &model.Work{ID: "hundred-years-history", Title: "The Hundred Years War: A History from Beginning to End"}
	join := []dupMember{
		{work: a, wk: workKey{key: "historyfrombeginningtoend", series: "Cold War", via: viaSeriesTail}},
		{work: b, wk: workKey{key: "historyfrombeginningtoend", series: "The Hundred Years War", via: viaSeriesTail}},
	}
	if _, vetoed := vetoStrippedSeriesDiffers([][]dupMember{join}); !vetoed {
		t.Error("a pair that met only on a tail key escaped the stripped-series veto")
	}
	// Two different joins never compare across each other.
	if _, vetoed := vetoStrippedSeriesDiffers([][]dupMember{join[:1], join[1:]}); vetoed {
		t.Error("the veto compared two members that never met on a key")
	}
}

// A series that is one book's own EDITION, named after it, holding that work alone at
// position 1: the series-name strip would remove the book's title and keep its tagline,
// so the retitle is withheld.
func TestWorkTitleWithholdsAStripOfTheBooksOwnEditionSeries(t *testing.T) {
	const title = "Let's Split Up - Ein verfluchtes Haus. Vier Freunde. Eine verhängnisvolle Entscheidung"
	for name, c := range map[string]struct {
		members  []string
		advisory bool
	}{
		"its own edition, alone at 1": {[]string{"lets-split-up-de@1"}, true},
		"a volume of a real series":   {[]string{"lets-split-up-de@3"}, false},
	} {
		t.Run(name, func(t *testing.T) {
			files := oneWork(t, "lets-split-up-de", title)
			files["series/le/lets-split-up-german-edition.json"] = seriesJSON(t, "lets-split-up-german-edition",
				"Let’s Split Up (German Edition)", c.members...)
			got := classOf(t, runFixture(t, files), ClassWorkTitle)
			if len(got) != 1 {
				t.Fatalf("want one W-TITLE record, got %d", len(got))
			}
			if got[0].Propose.Advisory != c.advisory {
				t.Errorf("advisory = %v, want %v (%s)", got[0].Propose.Advisory, c.advisory, got[0].Propose.Reason)
			}
		})
	}
}
