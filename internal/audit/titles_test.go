package audit

import (
	"reflect"
	"strings"
	"testing"
)

// baseline is the minimum a fixture needs so pkg/check finds every credit it
// resolves: the two people every workJSON/recJSON credits.
func baseline(t testing.TB) map[string]string {
	t.Helper()
	return map[string]string{
		"people/ja/jane-doe.json":      personJSON(t, "jane-doe", "Jane Doe"),
		"people/na/nate-narrator.json": personJSON(t, "nate-narrator", "Nate Narrator"),
	}
}

// fixture merges the baseline with the caller's files.
func fixture(t testing.TB, files map[string]string) map[string]string {
	t.Helper()
	out := baseline(t)
	for k, v := range files {
		out[k] = v
	}
	return out
}

// oneWork seeds a single work with one recording, for the title rules.
func oneWork(t testing.TB, id, title string, extra ...string) map[string]string {
	t.Helper()
	files := fixture(t, map[string]string{
		"works/xx/" + id + "/work.json":                       workJSON(t, id, title),
		"works/xx/" + id + "/recordings/only-" + id + ".json": recJSON(t, "only-"+id, id),
	})
	for i := 0; i+1 < len(extra); i += 2 {
		files[extra[i]] = extra[i+1]
	}
	return files
}

func TestWorkTitleDetectsEachDecoration(t *testing.T) {
	cases := []struct {
		name     string
		id       string
		title    string
		subclass string
		want     string
	}{
		{"trailing bracketed edition marker", "mageling", "Mageling (Unabridged)", decEdition, "Mageling"},
		{"bare edition marker", "amelia", "Amelia Unabridged", decEdition, "Amelia"},
		{"embedded volume marker", "pomme-vol-1", "10 Trésors de Pomme d'Api - Vol. 1", decVolume, "10 Trésors de Pomme d'Api"},
		{"comma volume marker", "wimpy-book-5", "Diary of a Wimpy Kid, Book 5", decVolume, "Diary of a Wimpy Kid"},
		{"bracketed translation suffix", "eric-mundodisco", "Eric (Mundodisco 9) [Eric (Discworld)]", decBracketSuffix, "Eric"},
		{"genre subtitle", "chaos-omnibus", "Chaos Omnibus: A GameLit Dark Adventure Series", decGenreSubtitle, "Chaos Omnibus"},
		{"trailing separator", "superfoods", "30 Proven Natural Superfoods -", decTrailingPunct, "30 Proven Natural Superfoods"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rep := runFixture(t, oneWork(t, c.id, c.title))
			got := subclassOf(t, rep, ClassWorkTitle, c.subclass)
			if len(got) != 1 {
				t.Fatalf("want one %s record, got %d (all: %+v)", c.subclass, len(got), classOf(t, rep, ClassWorkTitle))
			}
			if got[0].Want != c.want {
				t.Errorf("proposed title = %q, want %q", got[0].Want, c.want)
			}
			if got[0].Have != c.title {
				t.Errorf("have = %q, want the recorded title %q", got[0].Have, c.title)
			}
			if got[0].Field != "title" {
				t.Errorf("field = %q, want title", got[0].Field)
			}
		})
	}
}

// The article-series-prefix shape: a retailer prefixing the book with an article
// and its series name, so the article belongs to nothing.
func TestWorkTitleDetectsAnArticleSeriesPrefix(t *testing.T) {
	rep := runFixture(t, fixture(t, map[string]string{
		"works/ah/a-httyd-heros-guide/work.json":         workJSON(t, "a-httyd-heros-guide", "A How to Train Your Dragon: A Hero's Guide to Deadly Dragons"),
		"works/ah/a-httyd-heros-guide/recordings/a.json": recJSON(t, "a", "a-httyd-heros-guide"),
		"works/ho/how-to-be-a-hero/work.json":            workJSON(t, "how-to-be-a-hero", "How to Be a Hero"),
		"works/ho/how-to-be-a-hero/recordings/b.json":    recJSON(t, "b", "how-to-be-a-hero"),
		"series/ho/httyd.json":                           seriesJSON(t, "httyd", "How to Train Your Dragon", "how-to-be-a-hero@1"),
	}))
	got := subclassOf(t, rep, ClassWorkTitle, decArticleSeries)
	if len(got) != 1 {
		t.Fatalf("want one article-series-prefix record, got %d", len(got))
	}
	if got[0].Key != "a-httyd-heros-guide" {
		t.Errorf("key = %q", got[0].Key)
	}
	if got[0].Want != "A Hero's Guide to Deadly Dragons" {
		t.Errorf("proposed title = %q", got[0].Want)
	}
	// The same record is a slug candidate too, in F-HYGIENE, where no rename is
	// proposed - only flagged.
	if hyg := subclassOf(t, rep, ClassHygiene, hygSlugArticleSeries); len(hyg) != 1 {
		t.Errorf("want the slug flagged in F-HYGIENE too, got %d records", len(hyg))
	}
}

// A leading article that is part of the title must survive the proposal: the
// tail-only stopword trim exists for exactly this.
func TestWorkTitleKeepsALeadingArticleInItsProposal(t *testing.T) {
	rep := runFixture(t, oneWork(t, "a-deadly-cliche", "A Deadly Cliché:"))
	got := subclassOf(t, rep, ClassWorkTitle, decTrailingPunct)
	if len(got) != 1 {
		t.Fatalf("want one record, got %d", len(got))
	}
	if got[0].Want != "A Deadly Cliché" {
		t.Errorf("proposed title = %q, want the article kept", got[0].Want)
	}
}

func TestWorkTitleIsSilentOnACleanTitle(t *testing.T) {
	for _, title := range []string{
		"The Book Thief",        // "book" is a real title word, not a marker
		"Star Wars: A New Hope", // a real subtitle, not genre fluff
		"A Novel Idea",          // "novel" inside a title, not a fluff tail
		"1984",
	} {
		t.Run(title, func(t *testing.T) {
			rep := runFixture(t, oneWork(t, "the-work", title))
			if got := classOf(t, rep, ClassWorkTitle); len(got) != 0 {
				t.Errorf("%q was reported as decorated: %+v", title, got)
			}
		})
	}
}

func TestWorkTitleListsEveryMarkerItFound(t *testing.T) {
	rep := runFixture(t, oneWork(t, "stacked", "Stacked, Book 3 (Unabridged)"))
	got := classOf(t, rep, ClassWorkTitle)
	if len(got) != 1 {
		t.Fatalf("want one record, got %d", len(got))
	}
	if want := []string{decEdition, decVolume}; !reflect.DeepEqual(got[0].Markers, want) {
		t.Errorf("markers = %v, want %v", got[0].Markers, want)
	}
	// The subclass is the most specific of them, by markerPriority.
	if got[0].Subclass != decVolume {
		t.Errorf("subclass = %q, want %q", got[0].Subclass, decVolume)
	}
}

func TestWorkTitleNeverProposesASlug(t *testing.T) {
	rep := runFixture(t, oneWork(t, "mageling", "Mageling (Unabridged)"))
	for _, f := range classOf(t, rep, ClassWorkTitle) {
		if f.Field != "title" {
			t.Errorf("W-TITLE proposed a change to %q; the class is title-only", f.Field)
		}
	}
}

func TestWorkNoSeriesInfersSeriesAndPosition(t *testing.T) {
	rep := runFixture(t, fixture(t, map[string]string{
		"works/ch/chaos-seeds-book-3/work.json":         workJSON(t, "chaos-seeds-book-3", "Chaos Seeds, Book 3"),
		"works/ch/chaos-seeds-book-3/recordings/a.json": recJSON(t, "a", "chaos-seeds-book-3"),
		"works/fo/founding/work.json":                   workJSON(t, "founding", "The Founding"),
		"works/fo/founding/recordings/b.json":           recJSON(t, "b", "founding"),
		"series/ch/chaos-seeds.json":                    seriesJSON(t, "chaos-seeds", "Chaos Seeds", "founding@1"),
	}))
	got := subclassOf(t, rep, ClassWorkNoSeries, noSeriesAndPosition)
	if len(got) != 1 {
		t.Fatalf("want one series-and-position record, got %d: %+v", len(got), classOf(t, rep, ClassWorkNoSeries))
	}
	if got[0].Key != "chaos-seeds-book-3" || got[0].Want != "3" {
		t.Errorf("record = key %q want %q, expected chaos-seeds-book-3 / 3", got[0].Key, got[0].Want)
	}
	if len(got[0].Series) != 1 || got[0].Series[0].ID != "chaos-seeds" {
		t.Errorf("the inferred series is not cited: %+v", got[0].Series)
	}
}

func TestWorkNoSeriesNotesAnOccupiedPosition(t *testing.T) {
	rep := runFixture(t, fixture(t, map[string]string{
		"works/ch/chaos-seeds-book-1/work.json":         workJSON(t, "chaos-seeds-book-1", "Chaos Seeds, Book 1"),
		"works/ch/chaos-seeds-book-1/recordings/a.json": recJSON(t, "a", "chaos-seeds-book-1"),
		"works/fo/founding/work.json":                   workJSON(t, "founding", "The Founding"),
		"works/fo/founding/recordings/b.json":           recJSON(t, "b", "founding"),
		"series/ch/chaos-seeds.json":                    seriesJSON(t, "chaos-seeds", "Chaos Seeds", "founding@1"),
	}))
	got := subclassOf(t, rep, ClassWorkNoSeries, noSeriesAndPosition)
	if len(got) != 1 {
		t.Fatalf("want one record, got %d", len(got))
	}
	if !strings.Contains(strings.Join(got[0].Notes, " "), "already held by founding") {
		t.Errorf("the occupied position is not named: %v", got[0].Notes)
	}
}

func TestWorkNoSeriesReportsASeriesWithNoPosition(t *testing.T) {
	rep := runFixture(t, fixture(t, map[string]string{
		"works/ta/tales-of-chaos-seeds/work.json":         workJSON(t, "tales-of-chaos-seeds", "Tales of Chaos Seeds"),
		"works/ta/tales-of-chaos-seeds/recordings/a.json": recJSON(t, "a", "tales-of-chaos-seeds"),
		"works/fo/founding/work.json":                     workJSON(t, "founding", "The Founding"),
		"works/fo/founding/recordings/b.json":             recJSON(t, "b", "founding"),
		"series/ch/chaos-seeds.json":                      seriesJSON(t, "chaos-seeds", "Chaos Seeds", "founding@1"),
	}))
	if got := subclassOf(t, rep, ClassWorkNoSeries, noSeriesOnly); len(got) != 1 {
		t.Fatalf("want one series-only record, got %d: %+v", len(got), classOf(t, rep, ClassWorkNoSeries))
	}
}

func TestWorkNoSeriesReportsAPositionWithNoSeries(t *testing.T) {
	rep := runFixture(t, oneWork(t, "unnamed-book-4", "Something Entirely Unrelated, Book 4"))
	got := subclassOf(t, rep, ClassWorkNoSeries, noPositionOnly)
	if len(got) != 1 {
		t.Fatalf("want one position-only record, got %d: %+v", len(got), classOf(t, rep, ClassWorkNoSeries))
	}
	if got[0].Want != "4" {
		t.Errorf("want = %q, expected 4", got[0].Want)
	}
}

func TestWorkNoSeriesIgnoresAModeledMember(t *testing.T) {
	rep := runFixture(t, fixture(t, map[string]string{
		"works/ch/chaos-seeds-book-1/work.json":         workJSON(t, "chaos-seeds-book-1", "Chaos Seeds, Book 1"),
		"works/ch/chaos-seeds-book-1/recordings/a.json": recJSON(t, "a", "chaos-seeds-book-1"),
		"series/ch/chaos-seeds.json":                    seriesJSON(t, "chaos-seeds", "Chaos Seeds", "chaos-seeds-book-1@1"),
	}))
	if got := classOf(t, rep, ClassWorkNoSeries); len(got) != 0 {
		t.Errorf("a modeled member was reported as series-less: %+v", got)
	}
}

// A one-word series name is deliberately NOT indexed: it occurs inside unrelated
// titles constantly, and cleaning against it would delete a title's own words.
func TestSeriesNameIndexIgnoresOneWordNames(t *testing.T) {
	rep := runFixture(t, fixture(t, map[string]string{
		"works/he/hexed-michelle/work.json":              workJSON(t, "hexed-michelle", "Hexed"),
		"works/he/hexed-michelle/recordings/a.json":      recJSON(t, "a", "hexed-michelle"),
		"works/th/the-hexed-detective/work.json":         workJSON(t, "the-hexed-detective", "The Hexed Detective"),
		"works/th/the-hexed-detective/recordings/b.json": recJSON(t, "b", "the-hexed-detective"),
		"series/he/hexed.json":                           seriesJSON(t, "hexed", "Hexed", "hexed-michelle@1"),
	}))
	for _, f := range classOf(t, rep, ClassWorkNoSeries) {
		t.Errorf("a one-word series name linked a title to it: %s", f.Key)
	}
	for _, f := range classOf(t, rep, ClassWorkDup) {
		t.Errorf("a one-word series name produced a duplicate cluster: %v", workIDs(f))
	}
}

// Two series spelling one name identically are ambiguous, so neither is indexed:
// a title naming that spelling cannot say which it means.
func TestSeriesNameIndexDropsAnAmbiguousSpelling(t *testing.T) {
	rep := runFixture(t, fixture(t, map[string]string{
		"works/da/dark-tide-book-2/work.json":         workJSON(t, "dark-tide-book-2", "Dark Tide, Book 2"),
		"works/da/dark-tide-book-2/recordings/a.json": recJSON(t, "a", "dark-tide-book-2"),
		"works/on/one/work.json":                      workJSON(t, "one", "One"),
		"works/on/one/recordings/b.json":              recJSON(t, "b", "one"),
		"works/tw/two/work.json":                      workJSON(t, "two", "Two"),
		"works/tw/two/recordings/c.json":              recJSON(t, "c", "two"),
		"series/da/dark-tide-a.json":                  seriesJSON(t, "dark-tide-a", "Dark Tide", "one@1"),
		"series/da/dark-tide-b.json":                  seriesJSON(t, "dark-tide-b", "The Dark Tide", "two@1"),
	}))
	for _, f := range subclassOf(t, rep, ClassWorkNoSeries, noSeriesAndPosition) {
		t.Errorf("an ambiguous series spelling was resolved anyway: %s -> %+v", f.Key, f.Series)
	}
}

func TestAuditCleanTitleDropsADanglingTail(t *testing.T) {
	cases := []struct{ title, series, want string }{
		{"Hammered: The Iron Druid Chronicles, Book 3", "Iron Druid Chronicles", "Hammered"},
		{"Two Tales of the Iron Druid Chronicles", "Iron Druid Chronicles", "Two Tales"},
		{"Star Wars: A New Hope", "", "Star Wars: A New Hope"},
		{"The One and Only", "", "The One and Only"},
	}
	for _, c := range cases {
		if got := auditCleanTitle(c.title, c.series); got != c.want {
			t.Errorf("auditCleanTitle(%q, %q) = %q, want %q", c.title, c.series, got, c.want)
		}
	}
}

func TestTitleCarriesIdentity(t *testing.T) {
	for _, s := range []string{"Hammered", "Two Tales", "Level Nine Wizard"} {
		if !titleCarriesIdentity(s) {
			t.Errorf("titleCarriesIdentity(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"Omnibus", "Box Set", "The Complete Collection", "Level 2 Lessons 21-25"} {
		if titleCarriesIdentity(s) {
			t.Errorf("titleCarriesIdentity(%q) = true, want false", s)
		}
	}
}
