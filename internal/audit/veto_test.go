package audit

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/titlerule"
)

// The catalogue itself says these are different volumes: they hold different
// positions in one series. Their titles say nothing about it, which is why reading
// series_works rather than the titles is what catches the 520 real cases.
func TestWorkDupVetoesADifferentPositionInOneSeries(t *testing.T) {
	rep := runFixture(t, fixture(t, map[string]string{
		"works/th/the-gathering-a/work.json":         workJSON(t, "the-gathering-a", "The Gathering"),
		"works/th/the-gathering-a/recordings/a.json": recJSON(t, "a", "the-gathering-a"),
		"works/th/the-gathering-b/work.json":         workJSON(t, "the-gathering-b", "The Gathering."),
		"works/th/the-gathering-b/recordings/b.json": recJSON(t, "b", "the-gathering-b"),
		"series/da/darkness.json":                    seriesJSON(t, "darkness", "Darkness Rising", "the-gathering-a@1", "the-gathering-b@4"),
	}))
	got := subclassOf(t, rep, ClassWorkDup, dupTitleAuthor)
	if len(got) != 1 {
		t.Fatalf("want one cluster, got %d: %+v", len(got), classOf(t, rep, ClassWorkDup))
	}
	if !got[0].Propose.Advisory {
		t.Error("two different positions in one series must withhold the merge")
	}
	if !strings.Contains(got[0].Propose.Reason, "different positions") {
		t.Errorf("reason = %q", got[0].Propose.Reason)
	}
}

// Both modeled, in entirely different series: a book and its namesake, not two
// records of one book.
func TestWorkDupVetoesEntirelyDisjointSeries(t *testing.T) {
	rep := runFixture(t, fixture(t, map[string]string{
		"works/aw/awakening-a/work.json":         workJSON(t, "awakening-a", "Awakening"),
		"works/aw/awakening-a/recordings/a.json": recJSON(t, "a", "awakening-a"),
		"works/aw/awakening-b/work.json":         workJSON(t, "awakening-b", "Awakening."),
		"works/aw/awakening-b/recordings/b.json": recJSON(t, "b", "awakening-b"),
		"series/al/alpha.json":                   seriesJSON(t, "alpha", "Alpha Cycle", "awakening-a@1"),
		"series/be/beta.json":                    seriesJSON(t, "beta", "Beta Cycle", "awakening-b@1"),
	}))
	got := subclassOf(t, rep, ClassWorkDup, dupTitleAuthor)
	if len(got) != 1 {
		t.Fatalf("want one cluster, got %d", len(got))
	}
	if !got[0].Propose.Advisory || !strings.Contains(got[0].Propose.Reason, "different series") {
		t.Errorf("propose = %+v, want a disjoint-series veto", got[0].Propose)
	}
}

// A collection beside the volume it collects, in any language: the English-only test
// let three different series' omnibuses merge.
func TestWorkDupVetoesACollectionOnOneSideOnly(t *testing.T) {
	for name, title := range map[string]string{
		"english": "Ascend Online Omnibus",
		"german":  "Ascend Online Sammelband",
		"spanish": "Ascend Online Coleccion",
		"boxset":  "Ascend Online Box Set",
	} {
		t.Run(name, func(t *testing.T) {
			if !titlerule.IsCollection(title) {
				t.Fatalf("IsCollection(%q) = false; the vocabulary is missing it", title)
			}
			rep := runFixture(t, fixture(t, map[string]string{
				"works/as/ascend/work.json":                   workJSON(t, "ascend", "Ascend Online"),
				"works/as/ascend/recordings/a.json":           recJSON(t, "a", "ascend"),
				"works/as/ascend-collected/work.json":         workJSON(t, "ascend-collected", title),
				"works/as/ascend-collected/recordings/b.json": recJSON(t, "b", "ascend-collected"),
				"series/as/ascend-online.json":                seriesJSON(t, "ascend-online", "Ascend Online", "ascend@1"),
			}))
			for _, r := range subclassOf(t, rep, ClassWorkDup, dupTitleAuthor) {
				if !r.Propose.Advisory {
					t.Errorf("%q was proposed for merge with the volume it collects", title)
				}
			}
		})
	}
}

// An unknown language must not BRIDGE two stated ones into one cluster: a cluster
// may hold at most one stated language.
func TestWorkDupRefusesAnUnknownLanguageBridge(t *testing.T) {
	// The unknown-language work is schema-invalid, so problems are tolerated - the
	// audit explicitly supports being pointed at an invalid tree.
	rep, _ := runFixtureAllowingProblems(t, fixture(t, map[string]string{
		"works/br/bridge-en/work.json":           workJSON(t, "bridge-en", "Bridge"),
		"works/br/bridge-en/recordings/a.json":   recJSON(t, "a", "bridge-en"),
		"works/br/bridge-de/work.json":           workJSON(t, "bridge-de", "Bridge.", withLanguage("de")),
		"works/br/bridge-de/recordings/b.json":   recJSON(t, "b", "bridge-de"),
		"works/br/bridge-none/work.json":         withoutField(t, workJSON(t, "bridge-none", "Bridge,"), "language"),
		"works/br/bridge-none/recordings/c.json": recJSON(t, "c", "bridge-none"),
	}))
	for _, r := range classOf(t, rep, ClassWorkDup) {
		langs := map[string]bool{}
		for _, w := range r.Works {
			if w.Language != "" {
				langs[w.Language] = true
			}
		}
		if len(langs) > 1 {
			t.Errorf("%s holds two stated languages: %v", r.Key, workIDs(r))
		}
	}
}

// The nested-author-set pair the identity rule calls one work: the cluster key can
// only express EQUAL author sets, so this pair is only reached by asking the rule
// pairwise inside the title group.
func TestWorkDupReachesANestedAuthorSetPair(t *testing.T) {
	files := fixture(t, map[string]string{
		"works/ju/junes-flight/work.json":         workJSON(t, "junes-flight", "June's Wild Flight"),
		"works/ju/junes-flight/recordings/a.json": recJSON(t, "a", "junes-flight"),
		"people/il/illustrator-one.json":          personJSON(t, "illustrator-one", "Illustrator One"),
	})
	// The twin lists an extra author who IS role-credited, so the identity rule
	// reduces both to one set while the raw author lists differ.
	files["works/ju/junes-flight-twin/work.json"] = withCredits(t,
		workJSON(t, "junes-flight-twin", "June's Wild Flight.", withAuthors("jane-doe", "illustrator-one")),
		"illustrator-one", "illustrator")
	files["works/ju/junes-flight-twin/recordings/b.json"] = recJSON(t, "b", "junes-flight-twin")

	rep := runFixture(t, files)
	got := subclassOf(t, rep, ClassWorkDup, dupTitleAuthor)
	if len(got) != 1 {
		t.Fatalf("the nested-author-set pair was not reached: %+v", classOf(t, rep, ClassWorkDup))
	}
	if want := []string{"junes-flight", "junes-flight-twin"}; !reflect.DeepEqual(workIDs(got[0]), want) {
		t.Errorf("cluster = %v, want %v", workIDs(got[0]), want)
	}
}

// series-volume is advisory wholesale: a title's number is as often a part, an
// episode or a collection index as it is a series position.
func TestWorkDupSeriesVolumeIsAdvisoryWholesale(t *testing.T) {
	rep := runFixture(t, fixture(t, map[string]string{
		"works/fo/founding/work.json":                   workJSON(t, "founding", "The Founding"),
		"works/fo/founding/recordings/a.json":           recJSON(t, "a", "founding"),
		"works/ch/chaos-seeds-book-1/work.json":         workJSON(t, "chaos-seeds-book-1", "Chaos Seeds, Book 1"),
		"works/ch/chaos-seeds-book-1/recordings/b.json": recJSON(t, "b", "chaos-seeds-book-1"),
		"series/ch/chaos-seeds.json":                    seriesJSON(t, "chaos-seeds", "Chaos Seeds", "founding@1"),
	}))
	got := subclassOf(t, rep, ClassWorkDup, dupSeriesVolume)
	if len(got) != 1 {
		t.Fatalf("want one series-volume record, got %d", len(got))
	}
	if !got[0].Propose.Advisory || got[0].Propose.Op != OpReview {
		t.Errorf("propose = %+v, want an advisory review", got[0].Propose)
	}
	// The evidence a reviewer needs must be on the record.
	for _, w := range got[0].Works {
		if len(w.Publishers) == 0 && len(w.ReleaseDates) == 0 && w.Recordings > 0 {
			t.Errorf("%s cites neither publisher nor release date: the reviewer needs them", w.ID)
		}
	}
}

// A series name inside a title is a coincidence of words as often as a fact, so a
// membership needs corroboration: an author already in that series, or the explicit
// "<Series>, Book N" boundary shape.
func TestWorkNoSeriesNeedsCorroboration(t *testing.T) {
	// The title merely CONTAINS the words, the author has nothing in that series,
	// and the shape is not the boundary one.
	rep := runFixture(t, fixture(t, map[string]string{
		"works/mo/more-than-words/work.json":         workJSON(t, "more-than-words", "More Than Words Can Say"),
		"works/mo/more-than-words/recordings/a.json": recJSON(t, "a", "more-than-words"),
		"works/ot/other-book/work.json":              workJSON(t, "other-book", "Other Book", withAuthors("bram-stoker")),
		"works/ot/other-book/recordings/b.json":      recJSON(t, "b", "other-book"),
		"people/br/bram-stoker.json":                 personJSON(t, "bram-stoker", "Bram Stoker"),
		"series/mo/more-than.json":                   seriesJSON(t, "more-than", "More Than", "other-book@1"),
	}))
	for _, r := range subclassOf(t, rep, ClassWorkNoSeries, noSeriesAndPosition) {
		if !r.Propose.Advisory {
			t.Errorf("%s: an uncorroborated series reference must not add a membership", r.Key)
		}
	}
}

func TestWorkNoSeriesVetoesAnImplausiblePosition(t *testing.T) {
	for name, title := range map[string]string{
		"a year":            "Chaos Seeds, Book 1984",
		"a marketing index": "Chaos Seeds, Book 101",
	} {
		t.Run(name, func(t *testing.T) {
			rep := runFixture(t, fixture(t, map[string]string{
				"works/od/odd/work.json":              workJSON(t, "odd", title),
				"works/od/odd/recordings/a.json":      recJSON(t, "a", "odd"),
				"works/fo/founding/work.json":         workJSON(t, "founding", "The Founding"),
				"works/fo/founding/recordings/b.json": recJSON(t, "b", "founding"),
				"series/ch/chaos-seeds.json":          seriesJSON(t, "chaos-seeds", "Chaos Seeds", "founding@1"),
			}))
			got := subclassOf(t, rep, ClassWorkNoSeries, noSeriesAndPosition)
			if len(got) != 1 {
				t.Fatalf("want one record, got %d", len(got))
			}
			if !got[0].Propose.Advisory {
				t.Errorf("%q was proposed as a membership", title)
			}
		})
	}
}

// The measured destruction: a series name may only be removed at a title BOUNDARY.
func TestWorkTitleNeverExcisesASeriesNameMidTitle(t *testing.T) {
	cases := []struct{ id, title, series string }{
		{"more-than-words", "More Than Words", "More Than"},
		{"screwtape-letters", "The Screwtape Letters", "Screwtape"},
		{"do-you-take-this-man", "Do You Take This Man", "This Man"},
		{"needy-little-things", "Needy Little Things", "Little Things"},
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			if got, ok := titlerule.ProposeTitle(c.title, c.series); ok {
				t.Errorf("ProposeTitle(%q, %q) = %q; a mid-title excision must be refused", c.title, c.series, got)
			}
		})
	}
	// A COMMA-separated trailing segment that is only the series name is an
	// apposition inside a real title, not a catalogue tail.
	for _, c := range []struct{ title, series string }{
		{"Look Out, Secret Seven", "Secret Seven"},
		{"Lights Out, Full Throttle", "Full Throttle"},
	} {
		if got, ok := titlerule.ProposeTitle(c.title, c.series); ok {
			t.Errorf("ProposeTitle(%q, %q) = %q; a comma apposition is part of the title", c.title, c.series, got)
		}
	}
	// ...unless it also states a volume or calls itself a collection.
	if got, ok := titlerule.ProposeTitle("Blue Core, Secret Seven Book 3", "Secret Seven"); !ok || got != "Blue Core" {
		t.Errorf("a comma tail stating a volume must still strip: got %q, %v", got, ok)
	}
	// A colon or dash introduces a subtitle, where the series name alone IS
	// decoration.
	if got, ok := titlerule.ProposeTitle("Hammered: The Iron Druid Chronicles", "The Iron Druid Chronicles"); !ok || got != "Hammered" {
		t.Errorf("a colon subtitle naming the series must strip: got %q, %v", got, ok)
	}

	// The trailing-tail shape, which is the one it was ever right about, still works.
	if got, ok := titlerule.ProposeTitle("Resurrection: The Promiscus Guardians, Book 4", "The Promiscus Guardians"); !ok || got != "Resurrection" {
		t.Errorf("the trailing-tail shape broke: got %q, %v", got, ok)
	}
	// And the leading shape.
	if got, ok := titlerule.ProposeTitle("The Last Apprentice: Curse of the Bane (Book 2)", "The Last Apprentice"); !ok || got != "Curse of the Bane" {
		t.Errorf("the leading shape broke: got %q, %v", got, ok)
	}
}

// A residual that names no book is refused - judged on the RESIDUAL ALONE, because
// requiring the ORIGINAL to carry identity let every all-fluff title through and is
// where the degenerate "Las"/"Der"/"2" proposals came from.
func TestWorkTitleRefusesAPackagingOnlyResidual(t *testing.T) {
	cases := []struct{ title, series string }{
		{"The One-Armed Warlock: Book One", "The One-Armed Warlock"},
		{"Blue Core, Book Three", "Blue Core"},
		{"Las Cronicas: Volumen 2", "Las Cronicas"},
		{"Die Reihe: Sammelband 3", "Die Reihe"},
	}
	for _, c := range cases {
		if got, ok := titlerule.ProposeTitle(c.title, c.series); ok {
			t.Errorf("ProposeTitle(%q, %q) = %q; the residual names no book", c.title, c.series, got)
		}
	}
	// The mirror case: the same German shape where the book DOES have its own title
	// is a good proposal, because the series segment is a whole trailing segment.
	if got, ok := titlerule.ProposeTitle("Nightworld Academy: Die Schule - Band 5", "Die Schule"); !ok || got != "Nightworld Academy" {
		t.Errorf(`ProposeTitle("Nightworld Academy: Die Schule - Band 5", "Die Schule") = %q, %v; want "Nightworld Academy"`, got, ok)
	}
}

func TestWorkTitleRefusesADanglingConnective(t *testing.T) {
	for _, s := range []string{"und die grosse Liebe", "The the Wandering Trader", "of a Casino Mobster"} {
		if got, ok := titlerule.ProposeTitle(s+": Something", ""); ok && strings.EqualFold(got, s) {
			t.Errorf("a dangling connective was proposed: %q", got)
		}
	}
}

// Two works in one series whose stripped titles would collide: the volume number is
// the only thing a human can tell them apart by.
func TestWorkTitleRefusesASameSeriesCollision(t *testing.T) {
	files := fixture(t, map[string]string{})
	for _, n := range []string{"1", "2", "3"} {
		id := "spice-and-wolf-vol-" + n
		files["works/sp/"+id+"/work.json"] = workJSON(t, id, "Spice and Wolf, Vol. "+n)
		files["works/sp/"+id+"/recordings/r-"+n+".json"] = recJSON(t, "r-"+n, id)
	}
	files["series/sp/spice-and-wolf.json"] = seriesJSON(t, "spice-and-wolf", "Spice and Wolf",
		"spice-and-wolf-vol-1@1", "spice-and-wolf-vol-2@2", "spice-and-wolf-vol-3@3")

	rep := runFixture(t, files)
	for _, r := range classOf(t, rep, ClassWorkTitle) {
		t.Errorf("%s: proposing %q would collide with its own series siblings", r.Key, r.Propose.To)
	}
}

// article-series-prefix must never keep the series and drop the book's own title.
func TestArticleSeriesPrefixKeepsTheBooksOwnTitle(t *testing.T) {
	rep := runFixture(t, fixture(t, map[string]string{
		"works/ag/a-grim-awakening/work.json":         workJSON(t, "a-grim-awakening", "A Grim Awakening: The Forest of Hollow, Book 1"),
		"works/ag/a-grim-awakening/recordings/a.json": recJSON(t, "a", "a-grim-awakening"),
		"works/gr/grim-one/work.json":                 workJSON(t, "grim-one", "Grim One"),
		"works/gr/grim-one/recordings/b.json":         recJSON(t, "b", "grim-one"),
		"series/gr/grim-awakening.json":               seriesJSON(t, "grim-awakening", "Grim Awakening", "grim-one@1"),
	}))
	for _, r := range subclassOf(t, rep, ClassWorkTitle, titlerule.DecArticleSeries) {
		if strings.Contains(strings.ToLower(r.Propose.To), "forest of hollow") {
			t.Errorf("the proposal kept the series and dropped the book's title: %q -> %q", r.Propose.From, r.Propose.To)
		}
	}
}

// A numeric subtitle is IDENTITY, not a volume marker: two genuinely different books
// were reduced to one title and proposed for merge.
func TestNumericSubtitlesSurviveCleaning(t *testing.T) {
	for _, c := range []struct{ title, want string }{
		{"Without a Trace: 1881-1968", "Without a Trace: 1881-1968"},
		{"Without a Trace: 1970-2016", "Without a Trace: 1970-2016"},
		{"Something: 1984", "Something: 1984"},
	} {
		if got := titlerule.Clean(c.title, ""); got != c.want {
			t.Errorf("Clean(%q) = %q, want %q", c.title, got, c.want)
		}
	}
	// A single short number IS a volume marker and still goes.
	if got := titlerule.Clean("Something: 2", ""); got != "Something" {
		t.Errorf(`Clean("Something: 2") = %q, want "Something"`, got)
	}
}

// The two date-range books must therefore not cluster.
func TestWorkDupKeepsDateRangedVolumesApart(t *testing.T) {
	rep := runFixture(t, fixture(t, map[string]string{
		"works/wi/without-a-trace-early/work.json":         workJSON(t, "without-a-trace-early", "Without a Trace: 1881-1968"),
		"works/wi/without-a-trace-early/recordings/a.json": recJSON(t, "a", "without-a-trace-early", withRuntime(341)),
		"works/wi/without-a-trace-late/work.json":          workJSON(t, "without-a-trace-late", "Without a Trace: 1970-2016"),
		"works/wi/without-a-trace-late/recordings/b.json":  recJSON(t, "b", "without-a-trace-late", withRuntime(330)),
	}))
	for _, r := range classOf(t, rep, ClassWorkDup) {
		t.Errorf("two date-ranged volumes were clustered: %v", workIDs(r))
	}
}

func TestSeriesDupVetoesADifferentLanguage(t *testing.T) {
	files := fixture(t, map[string]string{
		"works/en/en-book/work.json":         workJSON(t, "en-book", "English Book"),
		"works/en/en-book/recordings/a.json": recJSON(t, "a", "en-book"),
		"works/fr/fr-book/work.json":         workJSON(t, "fr-book", "Livre", withLanguage("fr")),
		"works/fr/fr-book/recordings/b.json": recJSON(t, "b", "fr-book"),
		"series/aa/alpha.json":               seriesJSON(t, "alpha", "Dragon Heart", "en-book@1"),
		"series/bb/beta.json":                seriesJSON(t, "beta", "Dragon Heart Series", "fr-book@1"),
	})
	rep := runFixture(t, files)
	got := subclassOf(t, rep, ClassSeriesDup, serDupName)
	if len(got) != 1 {
		t.Fatalf("want one cluster, got %d", len(got))
	}
	if !got[0].Propose.Advisory || !strings.Contains(got[0].Propose.Reason, "different languages") {
		t.Errorf("propose = %+v, want a language veto", got[0].Propose)
	}
}

func TestSeriesDupVetoesACollectionSeries(t *testing.T) {
	rep := runFixture(t, fixture(t, map[string]string{
		"works/on/one/work.json":         workJSON(t, "one", "One"),
		"works/on/one/recordings/a.json": recJSON(t, "a", "one"),
		"works/tw/two/work.json":         workJSON(t, "two", "Two"),
		"works/tw/two/recordings/b.json": recJSON(t, "b", "two"),
		"series/pa/the-pack.json":        seriesJSON(t, "the-pack", "The Pack", "one@1"),
		"series/pc/pack-collection.json": seriesJSON(t, "pack-collection", "Pack Collection", "two@1"),
	}))
	for _, r := range classOf(t, rep, ClassSeriesDup) {
		if !r.Propose.Advisory {
			t.Errorf("%s: a series of collections must not fold onto the books it collects", r.Key)
		}
	}
}

// SER-PAREN's whole subject: a parenthetical is an alternative ordering or an author
// disambiguator, and folding erases it.
func TestSeriesDupVetoesParentheticalMembers(t *testing.T) {
	rep := runFixture(t, fixture(t, map[string]string{
		"works/on/one/work.json":           workJSON(t, "one", "One"),
		"works/on/one/recordings/a.json":   recJSON(t, "a", "one"),
		"works/tw/two/work.json":           workJSON(t, "two", "Two"),
		"works/tw/two/recordings/b.json":   recJSON(t, "b", "two"),
		"works/th/three/work.json":         workJSON(t, "three", "Three"),
		"works/th/three/recordings/c.json": recJSON(t, "c", "three"),
		"series/as/ascend-chron.json":      seriesJSON(t, "ascend-chron", "Ascend Online [chronological]", "one@1"),
		"series/as/ascend-pub.json":        seriesJSON(t, "ascend-pub", "Ascend Online [publication order]", "two@1"),
		"series/as/ascend-plain.json":      seriesJSON(t, "ascend-plain", "Ascend Online", "three@1"),
	}))
	for _, r := range classOf(t, rep, ClassSeriesDup) {
		if !r.Propose.Advisory {
			t.Errorf("%s: parenthetical-decorated series must not be folded mechanically", r.Key)
		}
		if !strings.Contains(r.Propose.Reason, "SER-PAREN") {
			t.Errorf("%s: reason = %q, want it to point at SER-PAREN", r.Key, r.Propose.Reason)
		}
	}
}

// The unaddressable-title veto: two different books whose cleaned titles reduce to one
// comparison key only because the key folds away everything non-ASCII. Found by running
// the repair pass over the real tree, where it proposed merging two Russian novels.
func TestWorkDupVetoesTitlesThatCarryNoIdentity(t *testing.T) {
	rep := runFixture(t, fixture(t, map[string]string{
		"works/ed/edge-of-insanity/work.json":         workJSON(t, "edge-of-insanity", "Грани безумия. Том 1", withLanguage("ru")),
		"works/ed/edge-of-insanity/recordings/a.json": recJSON(t, "a", "edge-of-insanity"),
		"works/bl/blade-and-heart/work.json":          workJSON(t, "blade-and-heart", "Клинком и сердцем, Том 1", withLanguage("ru")),
		"works/bl/blade-and-heart/recordings/b.json":  recJSON(t, "b", "blade-and-heart"),
	}))
	got := subclassOf(t, rep, ClassWorkDup, dupTitleAuthor)
	if len(got) != 1 {
		t.Fatalf("want one cluster (the key folds both titles to the same thing), got %d", len(got))
	}
	if !got[0].Propose.Advisory {
		t.Error("two books meeting on a number must withhold the merge")
	}
	if !strings.Contains(got[0].Propose.Reason, "meet on a") && !strings.Contains(got[0].Propose.Reason, "meeting on a number") {
		t.Errorf("reason = %q, want it to name the unaddressable titles", got[0].Propose.Reason)
	}
}

// ...and the same regime with IDENTICAL cleaned titles is a real duplicate ("1984"
// against "1984"), so the merge stands.
func TestWorkDupKeepsAMergeWhenTheNumericTitlesAreIdentical(t *testing.T) {
	rep := runFixture(t, fixture(t, map[string]string{
		"works/ni/nineteen-a/work.json":         workJSON(t, "nineteen-a", "1984"),
		"works/ni/nineteen-a/recordings/a.json": recJSON(t, "a", "nineteen-a", withRuntime(650)),
		"works/ni/nineteen-b/work.json":         workJSON(t, "nineteen-b", "1984 (Unabridged)"),
		"works/ni/nineteen-b/recordings/b.json": recJSON(t, "b", "nineteen-b", withRuntime(645)),
	}))
	got := subclassOf(t, rep, ClassWorkDup, dupTitleAuthor)
	if len(got) != 1 {
		t.Fatalf("want one cluster, got %d", len(got))
	}
	if got[0].Propose.Advisory {
		t.Errorf("a real duplicate whose title is a number was withheld: %q", got[0].Propose.Reason)
	}
}

// A range's canonical spelling is not safe to write: trimming fractional zeros
// collapses "2.1-2.10" to "2.1-2.1".
func TestSeriesIntegrityNeverRewritesARangeSpelling(t *testing.T) {
	rep := runFixture(t, seriesFixture(t, []string{"one"}, map[string]string{
		"series/se/seasons.json": seriesJSON(t, "seasons", "Season Chronicles", "one@2.1-2.10"),
	}))
	for _, r := range subclassOf(t, rep, ClassSeriesInteg, sNonCanonical) {
		if r.Propose.To != "" {
			t.Errorf("%s: a range rewrite was proposed (%q -> %q)", r.Key, r.Propose.From, r.Propose.To)
		}
		if !r.Propose.Advisory {
			t.Errorf("%s: a range recanonicalization must be advisory", r.Key)
		}
	}
	// The scalar rewrites are still proposed.
	rep = runFixture(t, seriesFixture(t, []string{"one", "two"}, map[string]string{
		"series/sc/scalars.json": seriesJSON(t, "scalars", "Scalar Chronicles", "one@0.50", "two@2"),
	}))
	got := subclassOf(t, rep, ClassSeriesInteg, sNonCanonical)
	if len(got) != 1 {
		t.Fatalf("want one scalar record, got %d", len(got))
	}
	if got[0].Propose.To != "0.5" || got[0].Propose.Advisory {
		t.Errorf("propose = %+v, want a plain 0.50 -> 0.5 rewrite", got[0].Propose)
	}
}
