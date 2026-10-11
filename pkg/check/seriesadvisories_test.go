package check

import (
	"fmt"
	"strings"
	"testing"
)

// The two series advisories (seriesadvisories.go), each with quiet and firing
// fixtures over a three-volume "Series One".

// seriesFixture is baseValid with three works by author-one and the series files
// given (name -> body); "series-one" is replaced when given.
func seriesFixture(series map[string]string) map[string]string {
	files := baseValid()
	for _, w := range []string{"book-two", "book-three"} {
		title := strings.ToUpper(w[:1]) + strings.ReplaceAll(w[1:], "-", " ")
		files["works/bo/"+w+"/work.json"] = `{"authors":["author-one"],"id":"` + w + `","language":"en",` +
			`"license":"CC0-1.0","sources":[{"type":"user"}],"title":"` + title + `"}`
	}
	files["series/se/series-one.json"] = seriesBody("series-one", "Series One", "", "book-one@1", "book-two@2", "book-three@3")
	for id, body := range series {
		files["series/"+id[:2]+"/"+id+".json"] = body
	}
	return files
}

// seriesBody renders a series over "<work>@<position>" members; extra is spliced in
// as further top-level members (`"ordering":"chronological",...`), or "".
func seriesBody(id, name, extra string, members ...string) string {
	works := make([]string, 0, len(members))
	for _, m := range members {
		i := strings.LastIndexByte(m, '@')
		works = append(works, fmt.Sprintf(`{"position":%q,"work":%q}`, m[i+1:], m[:i]))
	}
	if extra != "" {
		extra += ","
	}
	return fmt.Sprintf(`{%s"id":%q,"license":"CC0-1.0","name":%q,"sources":[{"type":"user"}],"works":[%s]}`,
		extra, id, name, strings.Join(works, ","))
}

func TestAdvisorySeriesRecordingQualifier(t *testing.T) {
	quiet := seriesFixture(nil)
	if got := advisoryMatching(advisoryWarnings(t, quiet), recordingQualifierMarker); len(got) != 0 {
		t.Errorf("a plain series reported %v", got)
	}

	for _, name := range []string{"Series One (Narrated by Narrator One)", "Series One (abridged)",
		"Series One (Full-Cast Editions)", "Series One - Gesprochen von Narrator One", "Series One [Dramatized Adaptation]"} {
		files := seriesFixture(map[string]string{"series-one-x": seriesBody("series-one-x", name, "", "book-one@1")})
		got := advisoryMatching(advisoryWarnings(t, files), recordingQualifierMarker)
		if len(got) != 1 || !strings.Contains(got[0], `the series "Series One"`) {
			t.Errorf("%q: got %v, want one report naming its base", name, got)
		}
	}

	// A language edition is not a recording qualifier; a series stating a link is a
	// modelled view, whatever its name says.
	for _, body := range []string{
		seriesBody("series-one-x", "Series One [German Edition]", "", "book-one@1"),
		seriesBody("series-one-x", "Series One (abridged)", `"ordering":"chronological","ordering_of":"series-one"`, "book-one@1", "book-two@2"),
	} {
		files := seriesFixture(map[string]string{"series-one-x": body})
		if got := advisoryMatching(advisoryWarnings(t, files), recordingQualifierMarker); len(got) != 0 {
			t.Errorf("%s reported %v", body, got)
		}
	}
}

func TestAdvisoryDuplicateSeries(t *testing.T) {
	// A variant listing two of series-one's works at the same positions is
	// reported against the variant, naming series-one - "02" being the same slot
	// as "2".
	files := seriesFixture(map[string]string{
		"series-one-abridged": seriesBody("series-one-abridged", "Series One (abridged)", "", "book-one@1", "book-two@02"),
	})
	got := advisoryMatching(advisoryWarnings(t, files), duplicateSeriesMarker)
	if len(got) != 1 || !strings.Contains(got[0], `"series-one-abridged"`) || !strings.Contains(got[0], `same position in "series-one"`) {
		t.Errorf("subset: got %v, want one report against series-one-abridged naming series-one", got)
	}

	// The same list twice is ONE report, against the recording-qualified side.
	files = seriesFixture(map[string]string{
		"series-one-full-cast": seriesBody("series-one-full-cast", "Series One (Full-Cast Editions)", "",
			"book-three@3", "book-two@2", "book-one@1"),
	})
	got = advisoryMatching(advisoryWarnings(t, files), duplicateSeriesMarker)
	if len(got) != 1 || !strings.Contains(got[0], `series "series-one-full-cast"`) {
		t.Errorf("equal lists: got %v, want one report against series-one-full-cast", got)
	}

	// Two plain spellings of the same list: the survivor is titlerule.SeriesRank's
	// (equal works, the smaller id), the ladder a SER-DUP merge keeps.
	files = seriesFixture(map[string]string{
		"aa-series-one": seriesBody("aa-series-one", "Series One Again", "", "book-one@1", "book-two@2", "book-three@3"),
	})
	got = advisoryMatching(advisoryWarnings(t, files), duplicateSeriesMarker)
	if len(got) != 1 || !strings.Contains(got[0], `series "series-one"`) || !strings.Contains(got[0], `in "aa-series-one"`) {
		t.Errorf("equal plain lists: got %v, want one report against series-one naming aa-series-one", got)
	}

	// A plain series is never reported as a duplicate of a recording variant that
	// lists more of its volumes: the variant is the recording spelled as a series,
	// which series-recording-qualifier names instead.
	files = seriesFixture(map[string]string{
		"series-one":       seriesBody("series-one", "Series One (gekürzt)", "", "book-one@1", "book-two@2", "book-three@3"),
		"series-one-plain": seriesBody("series-one-plain", "Series One", "", "book-one@1", "book-two@2"),
	})
	warns := advisoryWarnings(t, files)
	if got := advisoryMatching(warns, duplicateSeriesMarker); len(got) != 0 {
		t.Errorf("plain subset of a recording variant: reported %v", got)
	}
	if got := advisoryMatching(warns, recordingQualifierMarker); len(got) != 1 || !strings.Contains(got[0], `"series-one"`) {
		t.Errorf("plain subset of a recording variant: the variant is not named as one: %v", got)
	}

	// Quiet: a different position (a sub-series numbered on its own), a single
	// shared member (the floor), and an ordering variant of series-one.
	for name, body := range map[string]string{
		"renumbered": seriesBody("series-one-sub", "Sub Series", "", "book-two@1", "book-three@2"),
		"one member": seriesBody("series-one-sub", "Book Two", "", "book-two@2"),
		"ordering variant": seriesBody("series-one-sub", "Series One (Chronological)",
			`"ordering":"chronological","ordering_of":"series-one"`, "book-one@1", "book-two@2"),
	} {
		files := seriesFixture(map[string]string{"series-one-sub": body})
		if got := advisoryMatching(advisoryWarnings(t, files), duplicateSeriesMarker); len(got) != 0 {
			t.Errorf("%s: reported %v", name, got)
		}
	}
}

// The two classes are counted by the census and claimed by AdvisoryClass.
func TestSeriesAdvisoriesHaveClasses(t *testing.T) {
	files := seriesFixture(map[string]string{
		"series-one-abridged": seriesBody("series-one-abridged", "Series One (abridged)", "", "book-one@1", "book-two@2"),
	})
	dir := t.TempDir()
	writeEntities(t, dir, files)
	res := Load(dir)
	if !res.OK() {
		t.Fatalf("fixture must validate: %v", res.Problems)
	}
	counts := map[string]int{}
	for _, w := range res.Warnings {
		counts[AdvisoryClass(w)]++
	}
	if counts[AdvisorySeriesRecordingQualifier] != 1 || counts[AdvisoryDuplicateSeries] != 1 || counts[AdvisoryUnclassified] != 0 {
		t.Errorf("classes = %v, want one of each series class and nothing unclassified", counts)
	}
	census := AdvisoryCensus(res.Warnings)
	for _, want := range []string{"1 recording-qualified series names", "1 duplicate series"} {
		if !strings.Contains(census, want) {
			t.Errorf("census %q is missing %q", census, want)
		}
	}
}
