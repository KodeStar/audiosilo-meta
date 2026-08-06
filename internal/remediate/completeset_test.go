package remediate

import (
	"reflect"
	"strings"
	"testing"
)

// TestAUKRowIsAccepted is the fix's headline: the dump spells the UK
// marketplace "gb" (it mirrors ISO 3166), and the schema spells it "uk". A
// private region check refused every UK complete-set row outright.
func TestAUKRowIsAccepted(t *testing.T) {
	dir := t.TempDir()
	seedWorks(t, dir, blackPrismParts()...)
	sets := writeCompleteSets(t, map[string]any{
		"asin": "B09FRBS927", "title": "Black Prism [Dramatized Adaptation]", "region": "gb",
		"lengthMinutes": 1173, "authors": []any{map[string]any{"name": "Brent Weeks"}},
	})

	rep := run(t, dir, true, sets)
	if rep.MatchedSets != 1 {
		t.Fatalf("matched %d rows, want the gb row to be usable: %v", rep.MatchedSets, rep.SetProblems)
	}
	_, rec := readRecording(t, dir, "black-prism-dramatized-adaptation")
	asins := rec.asins()
	if len(asins) == 0 || asins[0].ASIN != "B09FRBS927" || asins[0].Region != "uk" {
		t.Errorf("asin[0] = %+v, want B09FRBS927 in the schema's \"uk\"", asins[0])
	}
}

// TestRegionAndASINAreNormalized: a row spelling its region or identifier in
// upper case must not be written verbatim into a record the schema then
// rejects.
func TestRegionAndASINAreNormalized(t *testing.T) {
	dir := t.TempDir()
	seedWorks(t, dir, blackPrismParts()...)
	sets := writeCompleteSets(t, map[string]any{
		"asin": " b09frbs927 ", "title": "Black Prism [Dramatized Adaptation]", "region": "US",
		"authors": []any{map[string]any{"name": "Brent Weeks"}},
	})

	run(t, dir, true, sets)
	_, rec := readRecording(t, dir, "black-prism-dramatized-adaptation")
	asins := rec.asins()
	if len(asins) == 0 || asins[0].ASIN != "B09FRBS927" || asins[0].Region != "us" {
		t.Errorf("asin[0] = %+v, want the normalized identifier and region", asins[0])
	}
	if !sourcesInclude(rec, "libex-import", "B09FRBS927", testToday) {
		t.Errorf("the provenance ref must be the normalized ASIN: %s", rec["sources"])
	}
}

// TestAnUnusableRowIsDeclinedAndReported: an identifier that is not an ASIN, or
// a marketplace the schema has no name for, must never reach a record.
func TestAnUnusableRowIsDeclinedAndReported(t *testing.T) {
	dir := t.TempDir()
	seedWorks(t, dir, blackPrismParts()...)
	sets := writeCompleteSets(t,
		map[string]any{"asin": "not-an-asin", "title": "Black Prism [Dramatized Adaptation]", "region": "us",
			"authors": []any{map[string]any{"name": "Brent Weeks"}}},
		map[string]any{"asin": "B09FRBS927", "title": "Black Prism [Dramatized Adaptation]", "region": "zz",
			"authors": []any{map[string]any{"name": "Brent Weeks"}}},
	)

	rep := run(t, dir, true, sets)
	if rep.CompleteSets != 0 || rep.MatchedSets != 0 {
		t.Fatalf("read %d rows / matched %d, want both declined", rep.CompleteSets, rep.MatchedSets)
	}
	if len(rep.SetProblems) != 2 {
		t.Fatalf("problems = %+v, want both rows named", rep.SetProblems)
	}
	_, rec := readRecording(t, dir, "black-prism-dramatized-adaptation")
	for _, a := range rec.asins() {
		if a.ASIN == "not-an-asin" || a.Region == "zz" {
			t.Errorf("a declined row reached the record: %+v", a)
		}
	}
}

// TestChaptersAreValidatedNotCopied: a chapter list that breaks the
// monotonic-from-zero rule metacheck enforces is dropped with a note, not
// written into the recording for metacheck to fail on later.
func TestChaptersAreValidatedNotCopied(t *testing.T) {
	dir := t.TempDir()
	seedWorks(t, dir, blackPrismParts()...)
	sets := writeCompleteSets(t, map[string]any{
		"asin": "B09FRBS927", "title": "Black Prism [Dramatized Adaptation]", "region": "us",
		"authors": []any{map[string]any{"name": "Brent Weeks"}},
		"chapters": []any{
			map[string]any{"title": "One", "startOffsetMs": 5000, "lengthMs": 1000},
			map[string]any{"title": "Two", "startOffsetMs": 6000, "lengthMs": 1000},
		},
	})

	rep := run(t, dir, true, sets)
	_, rec := readRecording(t, dir, "black-prism-dramatized-adaptation")
	if rec.has("chapters") {
		t.Errorf("chapters = %s, want a list that does not start at 0 dropped", rec["chapters"])
	}
	if len(rep.SetProblems) == 0 || !strings.Contains(rep.SetProblems[0].Reason, "chapters") {
		t.Errorf("problems = %+v, want the drop reported", rep.SetProblems)
	}
}

// TestValidChaptersAreCarried is the passing half.
func TestValidChaptersAreCarried(t *testing.T) {
	dir := t.TempDir()
	seedWorks(t, dir, blackPrismParts()...)
	sets := writeCompleteSets(t, map[string]any{
		"asin": "B09FRBS927", "title": "Black Prism [Dramatized Adaptation]", "region": "us",
		"authors": []any{map[string]any{"name": "Brent Weeks"}},
		"chapters": []any{
			map[string]any{"title": " One ", "startOffsetMs": 0, "lengthMs": 1000},
			map[string]any{"title": "", "startOffsetMs": 1000, "lengthMs": 2000},
		},
	})

	run(t, dir, true, sets)
	_, rec := readRecording(t, dir, "black-prism-dramatized-adaptation")
	var chapters []struct {
		Title string `json:"title"`
	}
	mustUnmarshal(t, rec["chapters"], &chapters)
	got := []string{chapters[0].Title, chapters[1].Title}
	if want := []string{"One", "Chapter 2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("chapter titles = %v, want the importer's trimming and numbering (%v)", got, want)
	}
}

// TestATimestampReleaseDateIsReduced: the dump states a full timestamp, and
// only its DATE part is a fact about the book.
func TestATimestampReleaseDateIsReduced(t *testing.T) {
	dir := t.TempDir()
	parts := blackPrismParts()
	for i := range parts {
		parts[i].Rec.Release = ""
	}
	seedWorks(t, dir, parts...)
	sets := writeCompleteSets(t, map[string]any{
		"asin": "B09FRBS927", "title": "Black Prism [Dramatized Adaptation]", "region": "us",
		"releaseDate": "2019-11-05 00:00:00+00", "authors": []any{map[string]any{"name": "Brent Weeks"}},
	})

	run(t, dir, true, sets)
	_, rec := readRecording(t, dir, "black-prism-dramatized-adaptation")
	if got := rec.str("release_date"); got != "2019-11-05" {
		t.Errorf("release_date = %q, want the date part of the timestamp", got)
	}
}

// TestALongTitleKeepsItsEditionMarker: a hard truncation at MaxSlugLen would
// cut the "-dramatized-adaptation" tail, which is the very thing that keeps the
// dramatization off the plain edition's slug.
func TestALongTitleKeepsItsEditionMarker(t *testing.T) {
	const long = "Elantris Tenth Anniversary Authors Definitive Edition of the Collected Chronicles of the City of Gods"
	slug := mintedSlug(long + dramatizedSuffix)
	if len(slug) > 100 {
		t.Fatalf("slug %q is %d bytes, over MaxSlugLen", slug, len(slug))
	}
	if !strings.HasSuffix(slug, "-dramatized-adaptation") {
		t.Errorf("slug = %q, want the edition marker to survive the bounding", slug)
	}
	if strings.Contains(slug, "--") || strings.HasPrefix(slug, "-") {
		t.Errorf("slug = %q is not well formed", slug)
	}
	// A title that fits is untouched.
	if got := mintedSlug("Black Prism [Dramatized Adaptation]"); got != "black-prism-dramatized-adaptation" {
		t.Errorf("mintedSlug = %q, want the plain slug for a title that fits", got)
	}
}
