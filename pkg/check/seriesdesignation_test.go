package check

import (
	"fmt"
	"strings"
	"testing"
)

// seriesdesignation_test.go is the series-designation census's fixture set: the
// shapes it reports (a series and volume, a bare position, a listed series name,
// the author's possessive, a genre tag, an ampersand) and the longer list it must
// stay quiet about.

// sdBase is baseValid with book-one retitled "Hollow Crown" (Series One, 1) and its
// recording given a runtime.
func sdBase() map[string]string {
	files := dupBase("Hollow Crown")
	files["works/bo/book-one/recordings/rec-one.json"] = `{"abridged":false,"id":"rec-one","language":"en",` +
		`"license":"CC0-1.0","narrators":["narrator-one"],"runtime_min":600,"sources":[{"type":"user"}],"work":"book-one"}`
	return files
}

// sdTwin adds a work id titled title by author-one in lang, with one recording by
// narrator-one at runtime minutes (none when 0).
func sdTwin(files map[string]string, id, title, lang string, runtime int) {
	files["works/"+id[:2]+"/"+id+"/work.json"] = fmt.Sprintf(`{"authors":["author-one"],"id":%q,"language":%q,`+
		`"license":"CC0-1.0","sources":[{"type":"libex-import"}],"title":%q}`, id, lang, title)
	rt := ""
	if runtime > 0 {
		rt = fmt.Sprintf(`"runtime_min":%d,`, runtime)
	}
	files["works/"+id[:2]+"/"+id+"/recordings/rec-a.json"] = fmt.Sprintf(`{"id":"rec-a","language":%q,`+
		`"license":"CC0-1.0","narrators":["narrator-one"],%s"sources":[{"type":"libex-import"}],"work":%q}`, lang, rt, id)
}

func sdAdvisories(t *testing.T, files map[string]string) []string {
	t.Helper()
	return advisoryMatching(advisoryWarnings(t, files), seriesDesignationMarker)
}

func TestAdvisorySeriesDesignationReportsEachShape(t *testing.T) {
	cases := []struct {
		title, kind string
	}{
		{"Hollow Crown: Series One, Book 1", sdKindSeriesPosition},
		{"Series One I: Hollow Crown", sdKindSeriesPosition},
		{"Hollow Crown: The Crown Saga 1", sdKindPosition},
		{"Hollow Crown: Book One", sdKindPosition},
		{"Series One: Hollow Crown", sdKindSeries},
		{"Series One and the Hollow Crown", sdKindSeries},
		{"Author One's Hollow Crown", sdKindPossessive},
		{"Hollow Crown: A Slow Living LitRPG", sdKindGenre},
		{"Hollow & Crown", "same title, other spelling"},
	}
	for _, c := range cases {
		t.Run(c.title, func(t *testing.T) {
			files := sdBase()
			if c.title == "Hollow & Crown" {
				files["works/bo/book-one/work.json"] = strings.Replace(files["works/bo/book-one/work.json"],
					`"Hollow Crown"`, `"Hollow and Crown"`, 1)
			}
			sdTwin(files, "zz-decorated", c.title, "en", 610)
			got := sdAdvisories(t, files)
			if len(got) != 1 {
				t.Fatalf("advisories = %v, want exactly one", got)
			}
			if !strings.Contains(got[0], `"book-one" and "zz-decorated"`) || !strings.Contains(got[0], "("+c.kind+")") {
				t.Errorf("advisory = %q, want the pair and kind %q", got[0], c.kind)
			}
		})
	}
}

func TestAdvisorySeriesDesignationStaysQuiet(t *testing.T) {
	cases := []struct {
		name, title, lang string
		runtime           int
	}{
		// Another volume of the series the plain work is listed in.
		{"listed series at another position", "Hollow Crown: Series One, Book 2", "en", 600},
		// An unlisted name stating a number the plain work does not hold.
		{"unlisted series at another number", "Hollow Crown: Origins 2", "en", 600},
		// A real subtitle is no designation.
		{"a real subtitle", "Hollow Crown: The Fitzgerald Translation", "en", 600},
		// A bare number word is the title's own word.
		{"a number word with no volume word", "Hollow Crown: Take One", "en", 600},
		// So is a one-letter numeral after no listed name.
		{"a one-letter numeral with no volume word", "Hollow Crown: Me and I", "en", 600},
		// A bare "and" joins two books.
		{"two books joined by and", "Series One and Hollow Crown", "en", 600},
		// A translation is another work.
		{"another language", "Hollow Crown: Series One, Book 1", "de", 600},
		// Two products: no recordings within a factor of two.
		{"runtimes far apart", "Hollow Crown: Series One, Book 1", "en", 200},
		// The normalized-identity key already joins it: that class's finding.
		{"the neighbour's finding", "Hollow Crown: A Dark Fantasy Adventure", "en", 600},
		// One spelling: the identity-equal class's finding.
		{"the same title", "Hollow Crown", "en", 600},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := sdBase()
			sdTwin(files, "zz-decorated", c.title, c.lang, c.runtime)
			if got := sdAdvisories(t, files); len(got) != 0 {
				t.Errorf("advisories = %v, want none", got)
			}
		})
	}
}

// Different authors are two books, however the titles read.
func TestAdvisorySeriesDesignationSkipsDifferentAuthors(t *testing.T) {
	files := sdBase()
	files["people/au/author-two.json"] = `{"id":"author-two","license":"CC0-1.0","name":"Author Two","sources":[{"type":"user"}]}`
	sdTwin(files, "zz-decorated", "Hollow Crown: Series One, Book 1", "en", 600)
	files["works/zz/zz-decorated/work.json"] = strings.Replace(files["works/zz/zz-decorated/work.json"],
		`["author-one"]`, `["author-two"]`, 1)
	if got := sdAdvisories(t, files); len(got) != 0 {
		t.Errorf("advisories = %v, want none", got)
	}
}

// Two works listed in one series at different positions are two volumes.
func TestAdvisorySeriesDesignationSkipsTwoListedVolumes(t *testing.T) {
	files := sdBase()
	sdTwin(files, "zz-decorated", "Series One: Hollow Crown", "en", 600)
	files["series/se/series-one.json"] = seriesBody("series-one", "Series One", "", "book-one@1", "zz-decorated@2")
	if got := sdAdvisories(t, files); len(got) != 0 {
		t.Errorf("advisories = %v, want none", got)
	}
}

// A collection word in the series' own name is no box set: the unplaced twin is
// read against the series its designation names.
func TestAdvisorySeriesDesignationReadsASeriesCollectionWordAsTheName(t *testing.T) {
	files := sdBase()
	files["series/se/series-one.json"] = seriesBody("series-one", "The Crown Trilogy", "", "book-one@1")
	sdTwin(files, "zz-decorated", "Hollow Crown: The Crown Trilogy, Book 1", "en", 600)
	got := sdAdvisories(t, files)
	if len(got) != 1 || !strings.Contains(got[0], "("+sdKindSeriesPosition+")") {
		t.Errorf("advisories = %v, want the pair at its listed position", got)
	}
}

// A title that is a series' whole name is that series' book: the name's tail is
// not a book's title beside it.
func TestAdvisorySeriesDesignationSkipsATitleThatIsASeriesName(t *testing.T) {
	files := sdBase()
	sdTwin(files, "zz-decorated", "Lost Kingdom Hollow Crown", "en", 600)
	files["series/lo/lost-kingdom-hollow-crown.json"] = seriesBody("lost-kingdom-hollow-crown", "Lost Kingdom Hollow Crown", "", "zz-decorated@1")
	if got := sdAdvisories(t, files); len(got) != 0 {
		t.Errorf("advisories = %v, want none", got)
	}
}

// A reviewed rejection withholds the pair and is counted as suppressed - once,
// however many readings of the title reach the pair.
func TestAdvisorySeriesDesignationHonoursAReviewedRejection(t *testing.T) {
	for _, title := range []string{"Hollow Crown: Series One, Book 1", "Series One and the Hollow Crown"} {
		t.Run(title, func(t *testing.T) {
			files := sdBase()
			sdTwin(files, "zz-decorated", title, "en", 600)
			res := reviewedLoad(t, files, distinctWorks("book-one", "zz-decorated"))
			if got := advisoryMatching(problemLines(res.Warnings), seriesDesignationMarker); len(got) != 0 {
				t.Errorf("a reviewed pair was reported: %v", got)
			}
			if res.Suppressed != 1 {
				t.Errorf("suppressed = %d, want 1", res.Suppressed)
			}
		})
	}
}

// The census counts the class under its own label, appended after the others.
func TestAdvisoryCensusCountsSeriesDesignationPairs(t *testing.T) {
	files := sdBase()
	sdTwin(files, "zz-decorated", "Hollow Crown: Series One, Book 1", "en", 600)
	dir := t.TempDir()
	writeEntities(t, dir, files)
	res := Load(dir)
	if !res.OK() {
		t.Fatalf("an advisory fixture must still validate: %v", res.Problems)
	}
	census := AdvisoryCensus(res.Warnings)
	if !strings.Contains(census, "1 series-designation duplicate work pairs") {
		t.Errorf("census = %q", census)
	}
	if i, j := strings.Index(census, "duplicate series"), strings.Index(census, "series-designation"); i < 0 || j < i {
		t.Errorf("the new class must be appended: %q", census)
	}
	for _, w := range res.Warnings {
		if strings.Contains(w.Msg, seriesDesignationMarker) && AdvisoryClass(w) != AdvisorySeriesDesignationDup {
			t.Errorf("class of %q = %q", w.Msg, AdvisoryClass(w))
		}
	}
}
