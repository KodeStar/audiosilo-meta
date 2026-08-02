package check

import (
	"strings"
	"testing"
)

// Each advisory ships with a quiet fixture and a firing one, and every case
// asserts the same two things: the class is REPORTED, and it is reported as a
// warning - a tree carrying one still validates, because none of these shapes
// can be called wrong on its own evidence.

// advisoryWarnings runs a load and returns the advisory lines, failing if the
// tree does not validate: an advisory must never be a problem.
func advisoryWarnings(t *testing.T, files map[string]string) []string {
	t.Helper()
	dir := t.TempDir()
	writeEntities(t, dir, files)
	res := Load(dir)
	if !res.OK() {
		t.Fatalf("an advisory fixture must still validate: %v", res.Problems)
	}
	out := make([]string, 0, len(res.Warnings))
	for _, w := range res.Warnings {
		out = append(out, w.String())
	}
	return out
}

// advisoryMatching returns the advisory lines containing substr.
func advisoryMatching(lines []string, substr string) []string {
	var out []string
	for _, l := range lines {
		if strings.Contains(l, substr) {
			out = append(out, l)
		}
	}
	return out
}

func TestAdvisoryCrossLanguageRecording(t *testing.T) {
	const marker = "a translation is a different work"

	if got := advisoryMatching(advisoryWarnings(t, baseValid()), marker); len(got) != 0 {
		t.Errorf("a single-language tree reported %v", got)
	}

	files := baseValid()
	files["works/bo/book-one/recordings/rec-two.json"] = `{"id":"rec-two","language":"de","license":"CC0-1.0",` +
		`"narrators":["narrator-one"],"sources":[{"type":"user"}],"work":"book-one"}`
	got := advisoryMatching(advisoryWarnings(t, files), marker)
	if len(got) != 1 || !strings.Contains(got[0], `"rec-two"`) {
		t.Errorf("cross-language recording advisories = %v", got)
	}
}

func TestAdvisoryHonorificPersonPair(t *testing.T) {
	const marker = "only by a courtesy title"

	// The bare twin is absent: "Dr. Seuss" has no "Seuss", and this is the
	// overwhelming majority of honorific-prefixed records.
	lonely := baseValid()
	lonely["people/dr/dr-arthur-conan-doyle.json"] = `{"id":"dr-arthur-conan-doyle","license":"CC0-1.0",` +
		`"name":"Dr. Arthur Conan Doyle","sources":[{"type":"user"}]}`
	if got := advisoryMatching(advisoryWarnings(t, lonely), marker); len(got) != 0 {
		t.Errorf("an honorific record with no bare twin reported %v", got)
	}

	pair := lonely
	pair["people/ar/arthur-conan-doyle.json"] = `{"id":"arthur-conan-doyle","license":"CC0-1.0",` +
		`"name":"Arthur Conan Doyle","sources":[{"type":"user"}]}`
	got := advisoryMatching(advisoryWarnings(t, pair), marker)
	if len(got) != 1 || !strings.Contains(got[0], "dr-arthur-conan-doyle") {
		t.Errorf("honorific pair advisories = %v", got)
	}
}

// A one-word remainder is coincidence far more often than it is a duplicate:
// "mr-peter" against "peter" says nothing.
func TestAdvisoryHonorificIgnoresAOneWordRemainder(t *testing.T) {
	files := baseValid()
	files["people/mr/mr-peter.json"] = `{"id":"mr-peter","license":"CC0-1.0","name":"Mr. Peter","sources":[{"type":"user"}]}`
	files["people/pe/peter.json"] = `{"id":"peter","license":"CC0-1.0","name":"Peter","sources":[{"type":"user"}]}`
	if got := advisoryMatching(advisoryWarnings(t, files), "only by a courtesy title"); len(got) != 0 {
		t.Errorf("a one-word remainder reported %v", got)
	}
}

func TestAdvisoryIdentityEqualWorkPair(t *testing.T) {
	const marker = "one book under two ids"

	// The base/fork pair. Both records list the same two people; only the fork records that one of
	// them is the translator. Their identity sets are therefore nested, which is
	// what makes them one book to the importer.
	files := baseValid()
	files["people/tr/translator-one.json"] = `{"id":"translator-one","license":"CC0-1.0",` +
		`"name":"Translator One","sources":[{"type":"user"}]}`
	files["works/bo/book-one/work.json"] = `{"authors":["author-one","translator-one"],` +
		`"id":"book-one","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Book One"}`
	files["works/bo/book-one-author-one/work.json"] = `{"authors":["author-one","translator-one"],` +
		`"credits":[{"person":"translator-one","role":"translator"}],` +
		`"id":"book-one-author-one","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Book One"}`
	got := advisoryMatching(advisoryWarnings(t, files), marker)
	if len(got) != 1 || !strings.Contains(got[0], "book-one-author-one") {
		t.Errorf("identity-equal advisories = %v", got)
	}
}

// The same fork with the role qualifier MISSING: one edition simply lists a
// person the other does not, and nothing says what they did. That is 361 of the
// 381 pairs in the seeded tree, so it is reported, and the extra person is named
// because they are the finding.
func TestAdvisoryIdentityEqualReportsAPlainAuthorSubset(t *testing.T) {
	files := baseValid()
	files["people/tr/translator-one.json"] = `{"id":"translator-one","license":"CC0-1.0",` +
		`"name":"Translator One","sources":[{"type":"user"}]}`
	files["works/bo/book-one-author-one/work.json"] = `{"authors":["author-one","translator-one"],` +
		`"id":"book-one-author-one","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Book One"}`
	got := advisoryMatching(advisoryWarnings(t, files), "one book under two ids")
	if len(got) != 1 {
		t.Fatalf("plain-subset advisories = %v", got)
	}
	for _, want := range []string{`"book-one-author-one" lists the same authors plus "translator-one"`, `"book-one"`} {
		if !strings.Contains(got[0], want) {
			t.Errorf("advisory %q is missing %q", got[0], want)
		}
	}
}

// Two works whose author lists merely OVERLAP are not this class: neither list
// contains the other, so the pair is two different books that share a title and
// a collaborator.
func TestAdvisoryIdentityEqualSkipsOverlappingAuthors(t *testing.T) {
	files := baseValid()
	for id, name := range map[string]string{"author-two": "Author Two", "author-three": "Author Three"} {
		files["people/au/"+id+".json"] = `{"id":"` + id + `","license":"CC0-1.0",` +
			`"name":"` + name + `","sources":[{"type":"user"}]}`
	}
	files["works/bo/book-one/work.json"] = `{"authors":["author-one","author-two"],` +
		`"id":"book-one","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Book One"}`
	files["works/bo/book-one-author-one/work.json"] = `{"authors":["author-one","author-three"],` +
		`"id":"book-one-author-one","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Book One"}`
	if got := advisoryMatching(advisoryWarnings(t, files), "one book under two ids"); len(got) != 0 {
		t.Errorf("an overlapping-author pair reported %v", got)
	}
}

// A subset pair in two languages is still a translation and its original: the
// language carve-out applies to the widened shape exactly as it does to the
// identity rule's own.
func TestAdvisoryIdentityEqualSubsetSkipsATranslation(t *testing.T) {
	files := baseValid()
	files["people/tr/translator-one.json"] = `{"id":"translator-one","license":"CC0-1.0",` +
		`"name":"Translator One","sources":[{"type":"user"}]}`
	files["works/bo/book-one-author-one/work.json"] = `{"authors":["author-one","translator-one"],` +
		`"id":"book-one-author-one","language":"de","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Book One"}`
	if got := advisoryMatching(advisoryWarnings(t, files), "one book under two ids"); len(got) != 0 {
		t.Errorf("a translated subset pair reported %v", got)
	}
}

// A translation and its original share a title and reduce to one author set,
// and they SHOULD be two works - the language is what says so.
func TestAdvisoryIdentityEqualSkipsATranslation(t *testing.T) {
	files := baseValid()
	files["works/bo/book-one-author-one/work.json"] = `{"authors":["author-one"],` +
		`"id":"book-one-author-one","language":"de","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Book One"}`
	if got := advisoryMatching(advisoryWarnings(t, files), "one book under two ids"); len(got) != 0 {
		t.Errorf("a translation pair reported %v", got)
	}
}

// Two different books that merely share a title stay apart.
func TestAdvisoryIdentityEqualSkipsADifferentAuthor(t *testing.T) {
	files := baseValid()
	files["people/au/author-two.json"] = `{"id":"author-two","license":"CC0-1.0","name":"Author Two","sources":[{"type":"user"}]}`
	files["works/bo/book-one-author-two/work.json"] = `{"authors":["author-two"],` +
		`"id":"book-one-author-two","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Book One"}`
	if got := advisoryMatching(advisoryWarnings(t, files), "one book under two ids"); len(got) != 0 {
		t.Errorf("a different-author title collision reported %v", got)
	}
}

// An orphan record is a person nothing credits. The quiet fixture is the base
// tree, where the author writes the work and the narrator reads the recording.
func TestAdvisoryOrphanPerson(t *testing.T) {
	const marker = "an orphan record"

	if got := advisoryMatching(advisoryWarnings(t, baseValid()), marker); len(got) != 0 {
		t.Errorf("a fully-credited tree reported %v", got)
	}

	files := baseValid()
	files["people/no/nobody-at-all.json"] = `{"id":"nobody-at-all","license":"CC0-1.0",` +
		`"name":"Nobody At All","sources":[{"type":"user"}]}`
	got := advisoryMatching(advisoryWarnings(t, files), marker)
	if len(got) != 1 || !strings.Contains(got[0], "nobody-at-all") {
		t.Errorf("orphan-person advisories = %v", got)
	}
}

// Every place a person can be named silences the advisory, including the one
// that is not a work or a recording: a series author is a reference like any
// other, and a maintainer sent to delete the record would break the series.
func TestAdvisoryOrphanPersonAcceptsEveryCreditSite(t *testing.T) {
	const marker = "an orphan record"
	person := func(id, name string) string {
		return `{"id":"` + id + `","license":"CC0-1.0","name":"` + name + `","sources":[{"type":"user"}]}`
	}
	files := baseValid()
	files["people/ed/editor-one.json"] = person("editor-one", "Editor One")
	files["people/se/series-author-one.json"] = person("series-author-one", "Series Author One")
	files["works/bo/book-one/work.json"] = `{"authors":["author-one"],` +
		`"credits":[{"person":"editor-one","role":"editor"}],` +
		`"id":"book-one","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Book One"}`
	files["series/se/series-one.json"] = `{"authors":["series-author-one"],"id":"series-one","license":"CC0-1.0",` +
		`"name":"Series One","sources":[{"type":"user"}],"works":[{"position":"1","work":"book-one"}]}`
	if got := advisoryMatching(advisoryWarnings(t, files), marker); len(got) != 0 {
		t.Errorf("a credited-elsewhere person reported %v", got)
	}
}

func TestAdvisoryCensusCountsEachClass(t *testing.T) {
	if got := AdvisoryCensus(nil); got != "" {
		t.Errorf("AdvisoryCensus(nil) = %q, want empty", got)
	}
	warns := []Problem{
		{Path: "a", Msg: "recording x: a translation is a different work"},
		{Path: "b", Msg: "person y differs only by a courtesy title: ..."},
		{Path: "c", Msg: "work z: one book under two ids"},
		{Path: "d", Msg: "recording w: a translation is a different work"},
		{Path: "e", Msg: "entry is 300000 bytes, over the pack target"},
		{Path: "f", Msg: `person "q" is credited by no work, recording or series: an orphan record`},
	}
	got := AdvisoryCensus(warns)
	for _, want := range []string{"2 cross-language recordings", "1 honorific person pairs",
		"1 identity-equal work pairs", "1 orphan people"} {
		if !strings.Contains(got, want) {
			t.Errorf("census %q is missing %q", got, want)
		}
	}
}

// TestAdvisoryIdentityEqualSkipsSerialVolumes: a serial published under its bare
// series name is six works with one title and one author, deliberately
// separated by the importer's position pre-pass. They are that rule WORKING,
// and fifteen pairs of them would bury the finding this advisory is for.
func TestAdvisoryIdentityEqualSkipsSerialVolumes(t *testing.T) {
	// The slug evidence alone: no series file records either work, because a
	// sibling edition already held both positions.
	files := baseValid()
	delete(files, "series/se/series-one.json")
	delete(files, "works/bo/book-one/work.json")
	delete(files, "works/bo/book-one/recordings/rec-one.json")
	for _, pos := range []string{"1", "2"} {
		slug := "bravelands-book-" + pos
		files["works/br/"+slug+"/work.json"] = `{"authors":["author-one"],"id":"` + slug + `",` +
			`"language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Bravelands"}`
	}
	if got := advisoryMatching(advisoryWarnings(t, files), "one book under two ids"); len(got) != 0 {
		t.Errorf("two volumes of one serial reported %v", got)
	}

	// A different SERIES POSITION is not evidence enough to silence the pair:
	// one book recorded at two positions of one series is exactly the finding
	// this advisory is for, and the migration - not the advisory - is where the
	// conservative threshold lives.
	files = baseValid()
	files["works/bo/book-one-two/work.json"] = `{"authors":["author-one"],"id":"book-one-two",` +
		`"language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Book One"}`
	files["series/se/series-one.json"] = `{"id":"series-one","license":"CC0-1.0","name":"Series One",` +
		`"sources":[{"type":"user"}],"works":[{"position":"1","work":"book-one"},{"position":"2","work":"book-one-two"}]}`
	if got := advisoryMatching(advisoryWarnings(t, files), "one book under two ids"); len(got) != 1 {
		t.Errorf("a same-title identity-equal pair at two series positions must still be reported: %v", got)
	}
}
