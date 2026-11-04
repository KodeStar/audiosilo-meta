package check

import (
	"fmt"
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
	return problemLines(res.Warnings)
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
		{Path: "e", Msg: "entry is 300000 bytes, over the 262144-byte pack target: an oversized entry cannot be split and keeps its pack over target"},
		{Path: "f", Msg: `person "q" is credited by no work, recording or series: an orphan record`},
		{Path: "g", Msg: "characters are " + scaleMarker},
	}
	got := AdvisoryCensus(warns)
	for _, want := range []string{"2 cross-language recordings", "1 honorific person pairs",
		"1 identity-equal work pairs", "1 orphan people", "1 oversized entries",
		"1 mis-scaled sidecars"} {
		if !strings.Contains(got, want) {
			t.Errorf("census %q is missing %q", got, want)
		}
	}
	// Every warning above belongs to a class: the census's counts must SUM to the
	// input, or it is under-reporting what the load produced. It was - the
	// pack-storage advisory had no class, so 12 of the real tree's 991 warnings
	// were counted by nothing.
	classified := 0
	for _, w := range warns {
		if AdvisoryClass(w) != AdvisoryUnclassified {
			classified++
		}
	}
	if classified != len(warns) {
		t.Errorf("%d of %d warnings are unclassified; every advisory rule needs a class in advisoryMarkers",
			len(warns)-classified, len(warns))
	}
}

// Every advisory a real load can emit must have a class, so the census and any
// consumer grouping by class account for all of them. The guard is over a FIXTURE
// tree's own warnings, read as Problems rather than as rendered strings.
func TestEveryAdvisoryHasAClass(t *testing.T) {
	files := baseValid()
	files["people/or/orphan-person.json"] = `{"id":"orphan-person","license":"CC0-1.0",` +
		`"name":"Orphan Person","sources":[{"type":"user"}]}`
	dir := t.TempDir()
	writeEntities(t, dir, files)
	res := Load(dir)
	if !res.OK() {
		t.Fatalf("an advisory fixture must still validate: %v", res.Problems)
	}
	if len(res.Warnings) == 0 {
		t.Fatal("the fixture produced no advisories")
	}
	for _, w := range res.Warnings {
		if AdvisoryClass(w) == AdvisoryUnclassified {
			t.Errorf("no advisory class claims %q", w)
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

// scaleMarker is the fragment the position-scale advisory is recognised by,
// here and in AdvisoryCensus.
const scaleMarker = "scaled to something other than the work's chapters"

// chapterList renders n sequential chapters for a recording, so a fixture can
// state "this work's recording has n chapters" without spelling them out.
func chapterList(n int) string {
	var b strings.Builder
	b.WriteString(`[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(`,`)
		}
		fmt.Fprintf(&b, `{"length_ms":1000,"start_ms":%d,"title":"Chapter %d"}`, i*1000, i+1)
	}
	b.WriteString(`]`)
	return b.String()
}

// sidecarSpread builds a characters sidecar and a recaps sidecar staged across
// the given positions - a real gradient, which is what the rule judges.
func sidecarSpread(at ...int) (string, string) { return sidecarPair(at, at) }

// sidecarPair is sidecarSpread with the two members staged apart: a cast
// revealed across reveals, and recaps through each of through.
func sidecarPair(reveals, through []int) (string, string) {
	var cs, rs []string
	seen := map[int]bool{}
	for i, p := range reveals {
		cs = append(cs, fmt.Sprintf(`{"description":"A person in the book, described in the contributor's own words.",`+
			`"id":"someone-%d","name":"Someone %d","reveal":{"chapter":%d}}`, i, i, p))
	}
	for _, p := range through {
		// checkRecaps forbids two recaps at one position, so a repeated position
		// yields a single recap - which is exactly the unstaged shape the
		// gradient guard exists for.
		if seen[p] {
			continue
		}
		seen[p] = true
		rs = append(rs, fmt.Sprintf(`{"scope":"book","text":"The story so far, in the contributor's own words.",`+
			`"through":{"chapter":%d}}`, p))
	}
	chars := `{"characters":[` + strings.Join(cs, ",") + `],"license":"CC-BY-SA-4.0",` +
		`"sources":[{"type":"community"}],"work":"book-one"}`
	recaps := `{"license":"CC-BY-SA-4.0","recaps":[` + strings.Join(rs, ",") + `],` +
		`"sources":[{"type":"community"}],"work":"book-one"}`
	return chars, recaps
}

// scaleFixture is baseValid plus a recording carrying chapters and a sidecar
// pair staged across at.
func scaleFixture(chapters int, at ...int) map[string]string {
	files := baseValid()
	files["works/bo/book-one/recordings/rec-one.json"] = withChapters(chapterList(chapters))
	chars, recaps := sidecarSpread(at...)
	files["works/bo/book-one/characters.json"] = chars
	files["works/bo/book-one/recaps.json"] = recaps
	return files
}

func TestAdvisorySidecarPositionScale(t *testing.T) {
	// A sidecar whose positions track its work's chapters is quiet.
	if got := advisoryMatching(advisoryWarnings(t, scaleFixture(40, 10, 20, 30)), scaleMarker); len(got) != 0 {
		t.Errorf("a sidecar reaching chapter 30 of 40 reported %v", got)
	}

	// A sidecar stopping far short of them is reported - once per member, so a
	// characters and a recaps sidecar each name themselves. Five distinct
	// positions, since a characters member its recaps do not vouch for is judged
	// only from sidecarScaleCastMinPositions.
	got := advisoryMatching(advisoryWarnings(t, scaleFixture(80, 3, 7, 11, 15, 19)), scaleMarker)
	if len(got) != 2 {
		t.Fatalf("a sidecar stopping at chapter 19 of 80 reported %d advisories, want 2 (characters + recaps): %v",
			len(got), got)
	}
	var sawChars, sawRecaps bool
	for _, g := range got {
		if strings.Contains(g, "characters sidecar") {
			sawChars = true
		}
		if strings.Contains(g, "recaps sidecar") {
			sawRecaps = true
		}
	}
	if !sawChars || !sawRecaps {
		t.Errorf("both members should name themselves, got %v", got)
	}
}

// TestAdvisorySidecarPositionScaleGuards pins the two cases the rule declines to
// judge: a work whose recordings carry no chapter list at all (nothing to
// compare against), and a chapter list too short for the ratio to mean anything.
func TestAdvisorySidecarPositionScaleGuards(t *testing.T) {
	noChapters := baseValid()
	chars, recaps := sidecarSpread(1, 2, 3)
	noChapters["works/bo/book-one/characters.json"] = chars
	noChapters["works/bo/book-one/recaps.json"] = recaps
	if got := advisoryMatching(advisoryWarnings(t, noChapters), scaleMarker); len(got) != 0 {
		t.Errorf("a work with no recording chapters reported %v", got)
	}

	// 3 of 15 is a smaller fraction than the threshold, but 15 chapters is below
	// the floor the rule will judge.
	if got := advisoryMatching(advisoryWarnings(t, scaleFixture(15, 1, 2, 3)), scaleMarker); len(got) != 0 {
		t.Errorf("a 15-chapter work reported %v", got)
	}
}

// TestAdvisorySidecarPositionScaleUsesSmallestChapterList pins that a second
// recording splitting the book more finely cannot be what condemns a sidecar:
// the comparison is against the SMALLEST chapter list among the work's
// recordings.
func TestAdvisorySidecarPositionScaleUsesSmallestChapterList(t *testing.T) {
	files := scaleFixture(30, 8, 14, 20)
	files["works/bo/book-one/recordings/rec-two.json"] = `{"abridged":false,"chapters":` + chapterList(90) +
		`,"id":"rec-two","language":"en","license":"CC0-1.0","narrators":["narrator-one"],` +
		`"sources":[{"type":"user"}],"work":"book-one"}`
	if got := advisoryMatching(advisoryWarnings(t, files), scaleMarker); len(got) != 0 {
		t.Errorf("a finely split second recording should not condemn a sidecar, got %v", got)
	}
}

// TestAdvisoryCensusCountsMisScaledSidecars pins the class into the one-line
// census metacheck prints, so a wave can be compared against the last one.
func TestAdvisoryCensusCountsMisScaledSidecars(t *testing.T) {
	dir := t.TempDir()
	writeEntities(t, dir, scaleFixture(80, 3, 7, 11, 15, 19))
	line := AdvisoryCensus(Load(dir).Warnings)
	if !strings.Contains(line, "2 mis-scaled sidecars") {
		t.Errorf("census = %q, want it to count 2 mis-scaled sidecars", line)
	}
}

// TestAdvisorySidecarPositionScaleNeedsAGradient pins that a sidecar using ONE
// position throughout is never judged: an all-at-chapter-1 cast list and a
// recaps member holding only the chapter-0 "previously" entry have no gradient
// to have scaled wrongly, and together they are the largest shape in the tree.
func TestAdvisorySidecarPositionScaleNeedsAGradient(t *testing.T) {
	if got := advisoryMatching(advisoryWarnings(t, scaleFixture(80, 1, 1, 1)), scaleMarker); len(got) != 0 {
		t.Errorf("an unstaged sidecar reported %v", got)
	}
	// Two positions is still short of a gradient the rule will judge.
	if got := advisoryMatching(advisoryWarnings(t, scaleFixture(80, 1, 2)), scaleMarker); len(got) != 0 {
		t.Errorf("a two-position sidecar reported %v", got)
	}
}

// titledChapters renders a recording chapter list with the given track titles.
func titledChapters(titles ...string) string {
	var b strings.Builder
	b.WriteString(`[`)
	for i, title := range titles {
		if i > 0 {
			b.WriteString(`,`)
		}
		fmt.Fprintf(&b, `{"length_ms":1000,"start_ms":%d,"title":%q}`, i*1000, title)
	}
	b.WriteString(`]`)
	return b.String()
}

// pairFixture is baseValid plus a recording of the given track titles and a
// sidecar pair staged apart; a nil through leaves the recaps member out.
func pairFixture(tracks []string, reveals, through []int) map[string]string {
	files := baseValid()
	files["works/bo/book-one/recordings/rec-one.json"] = withChapters(titledChapters(tracks...))
	chars, recaps := sidecarPair(reveals, through)
	files["works/bo/book-one/characters.json"] = chars
	if through != nil {
		files["works/bo/book-one/recaps.json"] = recaps
	}
	return files
}

// numberedTracks is n "Chapter i" titles.
func numberedTracks(n int) []string {
	var out []string
	for i := 1; i <= n; i++ {
		out = append(out, fmt.Sprintf("Chapter %d", i))
	}
	return out
}

// TestAdvisorySidecarScaleRecapsVouchForTheCast pins that a characters member is
// judged against the same entry's recaps: a cast is front-loaded by nature (a
// reveal is a FIRST appearance), so a cast whose recaps reach well into the book
// is on the book's scale however early its last card sits. A recaps member that
// only clears the floor does not vouch, and the cast is judged after all.
func TestAdvisorySidecarScaleRecapsVouchForTheCast(t *testing.T) {
	cast := []int{1, 2, 3, 5, 8, 12} // six distinct reveals, the last at 12 of 80
	// Recaps through chapter 60 of 80 vouch: nothing is reported.
	if got := advisoryMatching(advisoryWarnings(t, pairFixture(numberedTracks(80), cast,
		[]int{10, 20, 30, 40, 50, 60})), scaleMarker); len(got) != 0 {
		t.Errorf("a front-loaded cast beside recaps reaching 60 of 80 reported %v", got)
	}
	// Recaps through chapter 40 clear the floor (not reported themselves) but
	// stop short of the vouch floor, so the cast is judged and reported.
	got := advisoryMatching(advisoryWarnings(t, pairFixture(numberedTracks(80), cast,
		[]int{10, 20, 30, 40})), scaleMarker)
	if len(got) != 1 || !strings.Contains(got[0], "characters sidecar") {
		t.Errorf("a cast beside recaps reaching only 40 of 80 should be reported alone, got %v", got)
	}
}

// TestAdvisorySidecarScaleCastAloneNeedsFiveReveals pins the higher gradient a
// characters member judged ALONE must stage: four distinct reveals in the
// opening chapters is a sequel's returning cast, five is a gradient.
func TestAdvisorySidecarScaleCastAloneNeedsFiveReveals(t *testing.T) {
	if got := advisoryMatching(advisoryWarnings(t, pairFixture(numberedTracks(80),
		[]int{0, 1, 1, 2, 4}, nil)), scaleMarker); len(got) != 0 {
		t.Errorf("a four-reveal cast with no recaps reported %v", got)
	}
	// The recaps member keeps its own floor of three: recaps through 3, 7 and
	// 11 of 80 are reported, the three-reveal cast beside them (unvouched) is not.
	got := advisoryMatching(advisoryWarnings(t, scaleFixture(80, 3, 7, 11)), scaleMarker)
	if len(got) != 1 || !strings.Contains(got[0], "recaps sidecar") {
		t.Errorf("a three-position pair stopping at 11 of 80 reported %v, want the recaps member alone", got)
	}
	// Recaps holding only the chapter-0 "previously" entry vouch for nothing.
	if got := advisoryMatching(advisoryWarnings(t, pairFixture(numberedTracks(80),
		[]int{0, 1, 2, 4, 6}, []int{0})), scaleMarker); len(got) != 1 {
		t.Errorf("a five-reveal cast stopping at 6 of 80 reported %v, want one advisory", got)
	}
}

// TestAdvisorySidecarScaleCountsOnlyChapterTracks pins that the measure is the
// recording's CHAPTERS: credits, dedication, epigraph and part-divider tracks
// are not chapters of the work, and counting them made a sound sidecar look
// mis-scaled.
func TestAdvisorySidecarScaleCountsOnlyChapterTracks(t *testing.T) {
	tracks := []string{"Opening Credits", "Dedication", "Epigraph"}
	for p, part := range []string{"One", "Two", "Three", "Four", "Five", "Six"} {
		tracks = append(tracks, "Part "+part)
		for c := 1; c <= 4; c++ {
			tracks = append(tracks, fmt.Sprintf("Chapter %d", p*4+c))
		}
	}
	tracks = append(tracks, "End Credits", "Acknowledgments") // 35 tracks, 24 chapters

	// Recaps through 11: 11 of 35 tracks, but 11 of 24 chapters clears the floor.
	if got := advisoryMatching(advisoryWarnings(t, pairFixture(tracks, []int{1, 2, 3, 5, 7, 11},
		[]int{3, 5, 7, 9, 11})), scaleMarker); len(got) != 0 {
		t.Errorf("a sidecar reaching chapter 11 of 24 reported %v", got)
	}
	// Through 9 of 24 is short of it, and the message counts the chapters.
	got := advisoryMatching(advisoryWarnings(t, pairFixture(tracks, []int{1, 2, 3, 5, 7, 9},
		[]int{3, 5, 7, 9})), scaleMarker)
	if len(got) != 2 || !strings.Contains(got[0], "carry 24 chapters") {
		t.Errorf("a sidecar stopping at chapter 9 of 24 reported %v, want two advisories counting 24 chapters", got)
	}
}

// TestAdvisorySidecarScaleNamesTheParts pins the signature of a sidecar written
// per PART while the recording carries chapters: it stops exactly at the
// recording's part count, and the message says so. A part count is read from
// divider tracks ("Part One: ...", "Seven: ...") or, with none, from the part
// each chapter title states ("Part 2 - Chapter 7").
func TestAdvisorySidecarScaleNamesTheParts(t *testing.T) {
	words := []string{"One", "Two", "Three", "Four", "Five", "Six", "Seven", "Eight", "Nine", "Ten",
		"Eleven", "Twelve", "Thirteen", "Fourteen"}
	var named, bare, prefixed []string
	for p, w := range words {
		named = append(named, "Part "+w+": A Title")
		bare = append(bare, w+": A Title")
		for c := 1; c <= 4; c++ {
			named = append(named, fmt.Sprintf("Chapter %d", c))
			bare = append(bare, fmt.Sprintf("Chapter %d", c))
			prefixed = append(prefixed, fmt.Sprintf("Part %d - Chapter %d", p+1, p*4+c))
		}
	}
	const sig = "positions match the recording's 14 parts (rec-one)"
	recaps := []int{2, 4, 6, 8, 10, 12, 14}
	for name, tracks := range map[string][]string{"named": named, "bare": bare, "prefixed": prefixed} {
		got := advisoryMatching(advisoryWarnings(t, pairFixture(tracks, []int{1, 2, 3, 4, 5}, recaps)), sig)
		if len(got) != 1 || !strings.Contains(got[0], "recaps sidecar") || !strings.Contains(got[0], "carry 56 chapters") {
			t.Errorf("%s: a recaps member stopping at part 14 of 14 reported %v", name, got)
		}
	}
	// One short of the part count is the plain message: a coincidence of scale
	// is not the signature.
	lines := advisoryWarnings(t, pairFixture(named, []int{1, 2, 3, 4, 5}, []int{2, 4, 6, 8, 10, 13}))
	if got := advisoryMatching(lines, "parts ("); len(got) != 0 {
		t.Errorf("a sidecar stopping at 13 of 14 parts named the parts: %v", got)
	}
	if got := advisoryMatching(lines, scaleMarker); len(got) != 2 {
		t.Errorf("a sidecar stopping at 13 of 56 chapters reported %v, want two advisories", got)
	}
	// Both messages are one class in the census.
	dir := t.TempDir()
	writeEntities(t, dir, pairFixture(named, []int{1, 2, 3, 4, 5}, recaps))
	if line := AdvisoryCensus(Load(dir).Warnings); !strings.Contains(line, "2 mis-scaled sidecars") {
		t.Errorf("census = %q, want it to count 2 mis-scaled sidecars", line)
	}
}
