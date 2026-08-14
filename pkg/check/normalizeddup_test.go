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

// decoratedTwin adds a second record of book-one whose title carries a retailer
// volume-and-series decoration, which is the population the class exists to count.
func decoratedTwin(t *testing.T, files map[string]string, id, title string) []string {
	t.Helper()
	files["works/"+id[:2]+"/"+id+"/work.json"] = `{"authors":["author-one"],"id":"` + id + `",` +
		`"language":"en","license":"CC0-1.0","sources":[{"type":"libex-import"}],"title":"` + title + `"}`
	return advisoryMatching(advisoryWarnings(t, files), normalizedDupMarker)
}

func TestAdvisoryNormalizedDuplicateWorks(t *testing.T) {
	// The base tree holds one work: nothing to collide with.
	if got := advisoryMatching(advisoryWarnings(t, baseValid()), normalizedDupMarker); len(got) != 0 {
		t.Errorf("a single-record tree reported %v", got)
	}

	// The defect: "Book One" recorded again as "Book One: A Dark Fantasy Adventure",
	// a title that slugs elsewhere, normalizes to the same identity, and carries the
	// same author.
	got := decoratedTwin(t, baseValid(), "book-one-a-dark-fantasy-adventure", "Book One: A Dark Fantasy Adventure")
	if len(got) != 1 {
		t.Fatalf("normalized-duplicate advisories = %v, want exactly one", got)
	}
	if !strings.Contains(got[0], "book-one") || !strings.Contains(got[0], "book-one-a-dark-fantasy-adventure") {
		t.Errorf("the advisory must name both records: %q", got[0])
	}
}

// The class is DISJOINT from its identity-equal neighbour: a pair spelling its title
// identically is that rule's finding, and counting one defect as two would make the
// census line useless for tracking a repair wave.
func TestAdvisoryNormalizedDuplicateLeavesSameTitlePairsToItsNeighbour(t *testing.T) {
	files := baseValid()
	files["works/bo/book-one-author-one/work.json"] = `{"authors":["author-one"],"id":"book-one-author-one",` +
		`"language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Book One"}`
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
	files := baseValid()
	files["works/bo/book-one/work.json"] = `{"authors":["author-one"],"id":"book-one","language":"en",` +
		`"license":"CC0-1.0","sources":[{"type":"user"}],"title":"Bravelands, Book 1"}`
	got := decoratedTwin(t, files, "bravelands-book-2", "Bravelands, Book 2")
	if len(got) != 0 {
		t.Errorf("two volumes of one serial were reported as one book: %v", got)
	}
}

// Two different authors' books that happen to share a title are two works. The
// author-nesting rule is what says so, and a key group holding both must report
// neither.
func TestAdvisoryNormalizedDuplicateSkipsDifferentAuthors(t *testing.T) {
	files := baseValid()
	files["people/au/author-two.json"] = `{"id":"author-two","license":"CC0-1.0",` +
		`"name":"Author Two","sources":[{"type":"user"}]}`
	files["works/bo/book-one-author-two/work.json"] = `{"authors":["author-two"],"id":"book-one-author-two",` +
		`"language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Book One (Unabridged)"}`
	if got := advisoryMatching(advisoryWarnings(t, files), normalizedDupMarker); len(got) != 0 {
		t.Errorf("two authors' books sharing a title were reported as one: %v", got)
	}
}

// A translation is a different work, so the language rule separates the pair here
// exactly as it does in every other duplicate reader.
func TestAdvisoryNormalizedDuplicateSkipsTranslations(t *testing.T) {
	files := baseValid()
	files["works/bu/buch-eins-ungekurzt/work.json"] = `{"authors":["author-one"],"id":"buch-eins-ungekurzt",` +
		`"language":"de","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Book One (Unabridged)"}`
	if got := advisoryMatching(advisoryWarnings(t, files), normalizedDupMarker); len(got) != 0 {
		t.Errorf("a translation was reported as a duplicate: %v", got)
	}
}

// The author-SUPERSET shape the intake gate is calibrated on reaches the census too:
// the second record lists a role-credited translator among its authors, so only the
// nesting rule can see that the two are one book.
func TestAdvisoryNormalizedDuplicateSeesTheRoleCreditFork(t *testing.T) {
	files := baseValid()
	files["people/tr/translator-one.json"] = `{"id":"translator-one","license":"CC0-1.0",` +
		`"name":"Translator One","sources":[{"type":"user"}]}`
	files["works/bo/book-one-unabridged/work.json"] = `{"authors":["author-one","translator-one"],` +
		`"credits":[{"person":"translator-one","role":"translator"}],"id":"book-one-unabridged",` +
		`"language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Book One (Unabridged)"}`
	got := advisoryMatching(advisoryWarnings(t, files), normalizedDupMarker)
	if len(got) != 1 {
		t.Fatalf("the role-credit fork was not reported: %v", got)
	}
}

// The census counts the class under its own label, and the line APPENDS it rather
// than reordering the columns a maintainer compares two waves by.
func TestAdvisoryCensusCountsNormalizedDuplicates(t *testing.T) {
	files := baseValid()
	files["works/bo/book-one-a-litrpg-adventure/work.json"] = `{"authors":["author-one"],` +
		`"id":"book-one-a-litrpg-adventure","language":"en","license":"CC0-1.0",` +
		`"sources":[{"type":"libex-import"}],"title":"Book One: A LitRPG Adventure"}`
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
