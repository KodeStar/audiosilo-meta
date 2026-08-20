package check

import (
	"strings"
	"testing"
)

// normalizeddup_test.go is the normalized-identity census's fixture pair set: the
// tree shapes it must report, and - the longer list, because a census that
// over-reports is a census nobody reads - the ones it must stay quiet about.

// normalizedDupMarker is the class's own marker, deliberately different from
// checkIdentityEqualWorks' ("one book under two ids"): an advisory is classified by
// the first marker its message matches, so a message carrying both would be counted
// as the wrong class.
const normalizedDupMarker = "under two spellings of its title"

// dupBase is baseValid with the shared fixture's work RETITLED to something that
// names a book.
//
// The shared title is "Book One", which the identity rule deliberately refuses to
// key at all (it is a volume marker and nothing else - see
// titlerule.IdentityTitleKey), so a census fixture built on it would pass while
// testing nothing.
func dupBase(title string) map[string]string {
	files := baseValid()
	files["works/bo/book-one/work.json"] = `{"authors":["author-one"],"id":"book-one","language":"en",` +
		`"license":"CC0-1.0","sources":[{"type":"user"}],"title":"` + title + `"}`
	return files
}

// dupTwin adds a second record of the base work under another spelling of its title,
// and returns the advisories the class reported.
func dupTwin(t *testing.T, files map[string]string, id, title string) []string {
	t.Helper()
	files["works/"+id[:2]+"/"+id+"/work.json"] = `{"authors":["author-one"],"id":"` + id + `",` +
		`"language":"en","license":"CC0-1.0","sources":[{"type":"libex-import"}],"title":"` + title + `"}`
	return advisoryMatching(advisoryWarnings(t, files), normalizedDupMarker)
}

func TestAdvisoryNormalizedDuplicateWorks(t *testing.T) {
	// One record: nothing to collide with.
	if got := advisoryMatching(advisoryWarnings(t, dupBase("Hollow Crown")), normalizedDupMarker); len(got) != 0 {
		t.Errorf("a single-record tree reported %v", got)
	}

	// The defect: "Hollow Crown" recorded again as "Hollow Crown: A Dark Fantasy
	// Adventure", a title that slugs elsewhere, normalizes to the same identity, and
	// carries the same author.
	got := dupTwin(t, dupBase("Hollow Crown"), "hollow-crown-a-dark-fantasy-adventure",
		"Hollow Crown: A Dark Fantasy Adventure")
	if len(got) != 1 {
		t.Fatalf("normalized-duplicate advisories = %v, want exactly one", got)
	}
	if !strings.Contains(got[0], "book-one") || !strings.Contains(got[0], "hollow-crown-a-dark-fantasy-adventure") {
		t.Errorf("the advisory must name both records: %q", got[0])
	}
}

// The class is DISJOINT from its identity-equal neighbour: a pair spelling its title
// identically is that rule's finding, and counting one defect as two would make the
// census line useless for tracking a repair wave.
func TestAdvisoryNormalizedDuplicateLeavesSameTitlePairsToItsNeighbour(t *testing.T) {
	files := dupBase("Hollow Crown")
	files["works/ho/hollow-crown-author-one/work.json"] = `{"authors":["author-one"],` +
		`"id":"hollow-crown-author-one","language":"en","license":"CC0-1.0",` +
		`"sources":[{"type":"user"}],"title":"Hollow Crown"}`
	lines := advisoryWarnings(t, files)
	if got := advisoryMatching(lines, normalizedDupMarker); len(got) != 0 {
		t.Errorf("a same-title pair was reported by both classes: %v", got)
	}
	if got := advisoryMatching(lines, "one book under two ids"); len(got) != 1 {
		t.Errorf("the same-title pair must still be its neighbour's finding: %v", got)
	}
}

// A pair whose titles state DIFFERENT volume numbers is a serial's siblings. Their
// normalized titles collide precisely because the volume marker comes off, so
// without this test the class would report every multi-volume serial in the tree.
func TestAdvisoryNormalizedDuplicateSkipsStatedVolumes(t *testing.T) {
	got := dupTwin(t, dupBase("Bravelands, Book 1"), "bravelands-book-2", "Bravelands, Book 2")
	if len(got) != 0 {
		t.Errorf("two volumes of one serial were reported as one book: %v", got)
	}
}

// The same, for the two volume spellings markerSeq cannot read: a SEASON ordinal
// (wideGenreFluff strips it from the key as packaging) and a ROMAN numeral
// (wordVolumeMarker strips it as a marker). Both collapsed onto their season-1 and
// volume-I siblings until titlerule.StatedVolume learned to read them.
func TestAdvisoryNormalizedDuplicateSkipsOrdinalAndRomanVolumes(t *testing.T) {
	if got := dupTwin(t, dupBase("The Wandering Inn: Season 1"), "the-wandering-inn-season-2",
		"The Wandering Inn: Season 2"); len(got) != 0 {
		t.Errorf("two seasons were reported as one book: %v", got)
	}
	if got := dupTwin(t, dupBase("Faraway Paladin: Volume I"), "faraway-paladin-volume-ii",
		"Faraway Paladin: Volume II"); len(got) != 0 {
		t.Errorf("two roman-numbered volumes were reported as one book: %v", got)
	}
}

// And for the third: a volume number spelled as a WORD inside a decorative group,
// which the key drops with the group. "Book Two" matched wordVolumeMarker's shape all
// along and carried no number out of it, so the pair stated nothing to disagree about.
func TestAdvisoryNormalizedDuplicateSkipsWordVolumes(t *testing.T) {
	if got := dupTwin(t, dupBase("Wildwood (Book One)"), "wildwood-book-two",
		"Wildwood (Book Two)"); len(got) != 0 {
		t.Errorf("two word-numbered volumes were reported as one book: %v", got)
	}
}

// A COLLECTION on one side is not the volume it collects. The veto lives in the
// index's own predicate, so the census and both writers inherit it - it was
// documented as "the caller's" and implemented by nobody.
func TestAdvisoryNormalizedDuplicateSkipsCollections(t *testing.T) {
	if got := dupTwin(t, dupBase("Bravelands"), "bravelands-books-1-3", "Bravelands: Books 1-3"); len(got) != 0 {
		t.Errorf("a boxed set was reported as its own volume 1: %v", got)
	}
	if got := dupTwin(t, dupBase("Red Rising"), "red-rising-the-complete-boxed-set",
		"Red Rising: The Complete Boxed Set"); len(got) != 0 {
		t.Errorf("a complete boxed set was reported as the first book: %v", got)
	}
}

// Two different authors' books that happen to share a title are two works. The
// author-nesting rule is what says so, and a key group holding both must report
// neither.
func TestAdvisoryNormalizedDuplicateSkipsDifferentAuthors(t *testing.T) {
	files := dupBase("Hollow Crown")
	files["people/au/author-two.json"] = `{"id":"author-two","license":"CC0-1.0",` +
		`"name":"Author Two","sources":[{"type":"user"}]}`
	files["works/ho/hollow-crown-author-two/work.json"] = `{"authors":["author-two"],` +
		`"id":"hollow-crown-author-two","language":"en","license":"CC0-1.0",` +
		`"sources":[{"type":"user"}],"title":"Hollow Crown (Unabridged)"}`
	if got := advisoryMatching(advisoryWarnings(t, files), normalizedDupMarker); len(got) != 0 {
		t.Errorf("two authors' books sharing a title were reported as one: %v", got)
	}
}

// A translation is a different work, so the language rule separates the pair here
// exactly as it does in every other duplicate reader.
func TestAdvisoryNormalizedDuplicateSkipsTranslations(t *testing.T) {
	files := dupBase("Hollow Crown")
	files["works/ho/hohle-krone-ungekurzt/work.json"] = `{"authors":["author-one"],` +
		`"id":"hohle-krone-ungekurzt","language":"de","license":"CC0-1.0",` +
		`"sources":[{"type":"user"}],"title":"Hollow Crown (Unabridged)"}`
	if got := advisoryMatching(advisoryWarnings(t, files), normalizedDupMarker); len(got) != 0 {
		t.Errorf("a translation was reported as a duplicate: %v", got)
	}
}

// A title whose residual names NO BOOK is no identity at all: "Cars 2" against the
// series "Cars" reduces to "2", which 945 other sequels reduce to as well. The rule
// refuses to key it, so two unrelated sequels are not a finding - the defect that put
// ~500 unrelated works in one advisory line.
func TestAdvisoryNormalizedDuplicateSkipsDegenerateResiduals(t *testing.T) {
	files := dupBase("Cars 2")
	files["series/ca/cars.json"] = `{"id":"cars","license":"CC0-1.0","name":"Cars",` +
		`"sources":[{"type":"user"}],"works":[{"position":"2","work":"book-one"}]}`
	files["series/ha/hawk.json"] = `{"id":"hawk","license":"CC0-1.0","name":"Hawk",` +
		`"sources":[{"type":"user"}],"works":[{"position":"2","work":"hawk-2"}]}`
	if got := dupTwin(t, files, "hawk-2", "Hawk 2"); len(got) != 0 {
		t.Errorf("two unrelated sequels were reported as one book: %v", got)
	}
}

// The author-SUPERSET shape the intake gate is calibrated on reaches the census too:
// the second record lists a role-credited translator among its authors, so only the
// nesting rule can see that the two are one book.
func TestAdvisoryNormalizedDuplicateSeesTheRoleCreditFork(t *testing.T) {
	files := dupBase("Hollow Crown")
	files["people/tr/translator-one.json"] = `{"id":"translator-one","license":"CC0-1.0",` +
		`"name":"Translator One","sources":[{"type":"user"}]}`
	files["works/ho/hollow-crown-unabridged/work.json"] = `{"authors":["author-one","translator-one"],` +
		`"credits":[{"person":"translator-one","role":"translator"}],"id":"hollow-crown-unabridged",` +
		`"language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Hollow Crown (Unabridged)"}`
	got := advisoryMatching(advisoryWarnings(t, files), normalizedDupMarker)
	if len(got) != 1 {
		t.Fatalf("the role-credit fork was not reported: %v", got)
	}
}

// The census counts the class under its own label, and the line APPENDS it rather
// than reordering the columns a maintainer compares two waves by.
func TestAdvisoryCensusCountsNormalizedDuplicates(t *testing.T) {
	files := dupBase("Hollow Crown")
	files["works/ho/hollow-crown-a-litrpg-adventure/work.json"] = `{"authors":["author-one"],` +
		`"id":"hollow-crown-a-litrpg-adventure","language":"en","license":"CC0-1.0",` +
		`"sources":[{"type":"libex-import"}],"title":"Hollow Crown: A LitRPG Adventure"}`
	dir := t.TempDir()
	writeEntities(t, dir, files)
	res := Load(dir)
	if !res.OK() {
		t.Fatalf("an advisory fixture must still validate: %v", res.Problems)
	}
	census := AdvisoryCensus(res.Warnings)
	if !strings.Contains(census, "1 normalized-identity duplicate work groups") {
		t.Errorf("census = %q", census)
	}
	// The pre-existing classes keep their place ahead of it.
	if i, j := strings.Index(census, "orphan people"), strings.Index(census, "normalized-identity"); i < 0 || j < i {
		t.Errorf("the new class must be appended, not reordered in: %q", census)
	}
}
