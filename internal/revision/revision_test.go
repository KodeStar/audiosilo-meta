package revision

import (
	"bytes"
	"context"
	"database/sql"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/kodestar/audiosilo-meta/internal/artifacttest"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/query"
)

var (
	t1 = time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	t2 = time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	t3 = time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
)

func stamp(t *testing.T, cat *model.Catalog, prev string, at time.Time) string {
	t.Helper()
	out := artifacttest.Build(t, cat)
	if _, err := Stamp(context.Background(), out, Options{Previous: prev, At: at, Workers: 3}); err != nil {
		t.Fatal(err)
	}
	return out
}

// baseline is cat stamped against an EMPTY previous release, which makes every
// page new and so dates every one of them at.
func baseline(t *testing.T, cat *model.Catalog, at time.Time) string {
	t.Helper()
	return stamp(t, cat, artifacttest.Build(t, &model.Catalog{}), at)
}

// rows reads an artifact's page_revisions as "family/id" -> revised_at.
func rows(t *testing.T, path string) map[string]string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	r, err := db.Query(`SELECT kind, id, revised_at FROM page_revisions`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	out := map[string]string{}
	for r.Next() {
		var kind, id, at string
		if err := r.Scan(&kind, &id, &at); err != nil {
			t.Fatal(err)
		}
		out[kind+"/"+id] = at
	}
	if err := r.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func metaValue(t *testing.T, path, key string) string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var v string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&v); err != nil && err != sql.ErrNoRows {
		t.Fatal(err)
	}
	return v
}

func stampedAt(rows map[string]string, at time.Time) []string {
	var out []string
	for k, v := range rows {
		if v == at.Format(time.RFC3339) {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// moved is the pages a mutation of cat moves: cat is dated t1 against an empty
// release, the mutated copy is stamped t2 against that, and the pages stamped t2
// are returned. Everything else must have kept t1, which this checks too.
func moved(t *testing.T, cat func() *model.Catalog, mutate func(*model.Catalog)) []string {
	t.Helper()
	base := baseline(t, cat(), t1)
	next := cat()
	mutate(next)
	got := rows(t, stamp(t, next, base, t2))
	before := rows(t, base)
	for k, v := range got {
		if v != t2.Format(time.RFC3339) && v != before[k] {
			t.Errorf("%s: date %s is neither carried (%s) nor the new stamp", k, v, before[k])
		}
	}
	return stampedAt(got, t2)
}

func person(cat *model.Catalog, id string) *model.Person {
	for _, p := range cat.People {
		if p.ID == id {
			return p
		}
	}
	panic("no person " + id)
}

func work(cat *model.Catalog, id string) *model.Work {
	for _, w := range cat.Works {
		if w.ID == id {
			return w
		}
	}
	panic("no work " + id)
}

func TestEveryPageOfANewReleaseIsDated(t *testing.T) {
	path := baseline(t, artifacttest.Fixture(), t1)
	got := rows(t, path)
	want := []string{
		"characters/project-hail-mary",
		"people/andy-weir", "people/brandon-sanderson", "people/kate-reading", "people/michael-kramer", "people/ray-porter",
		"recaps/project-hail-mary",
		"series/the-stormlight-archive",
		"works/edgedancer", "works/project-hail-mary", "works/the-way-of-kings", "works/words-of-radiance",
	}
	if keys := slices.Sorted(maps.Keys(got)); !slices.Equal(keys, want) {
		t.Fatalf("dated pages = %v\nwant %v", keys, want)
	}
	if got := stampedAt(got, t1); len(got) != len(want) {
		t.Errorf("pages stamped %s = %v, want all of them", t1, got)
	}
	if v := metaValue(t, path, "revisions_at"); v != t1.Format(time.RFC3339) {
		t.Errorf("meta(revisions_at) = %q, want %s", v, t1.Format(time.RFC3339))
	}
}

// TestUnchangedRebuildKeepsEveryDate: the same data stamped against a release
// that already dated it carries every date, and two stamps of the same inputs
// are the same bytes.
func TestUnchangedRebuildKeepsEveryDate(t *testing.T) {
	base := baseline(t, artifacttest.Fixture(), t1)
	again := stamp(t, artifacttest.Fixture(), base, t2)
	if got, want := rows(t, again), rows(t, base); !maps.Equal(got, want) {
		t.Errorf("an unchanged rebuild moved dates:\n got %v\nwant %v", got, want)
	}
	if v := metaValue(t, again, "revisions_at"); v != t2.Format(time.RFC3339) {
		t.Errorf("meta(revisions_at) = %q, want the new stamp %s", v, t2.Format(time.RFC3339))
	}

	twin := stamp(t, artifacttest.Fixture(), base, t2)
	a, err := os.ReadFile(again)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(twin)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Error("two stamps of the same data, previous release and stamp differ in bytes")
	}
}

func TestCommunityOnlyChangeMovesOnlyItsGuide(t *testing.T) {
	got := moved(t, artifacttest.Fixture, func(c *model.Catalog) {
		c.Recaps[0].Recaps[0].Text = "First contact is made, and goes badly."
	})
	if want := []string{"recaps/project-hail-mary"}; !slices.Equal(got, want) {
		t.Errorf("moved %v, want %v", got, want)
	}
	got = moved(t, artifacttest.Fixture, func(c *model.Catalog) {
		c.Characters[0].Characters[1].Description = "An engineer from another star."
	})
	if want := []string{"characters/project-hail-mary"}; !slices.Equal(got, want) {
		t.Errorf("moved %v, want %v", got, want)
	}
}

func TestDescriptionChangeMovesTheWorkPage(t *testing.T) {
	got := moved(t, artifacttest.Fixture, func(c *model.Catalog) {
		c.Descriptions[0].Text += " A second paragraph's worth of setup."
	})
	if want := []string{"works/project-hail-mary"}; !slices.Equal(got, want) {
		t.Errorf("moved %v, want %v", got, want)
	}
}

func TestChapterEditMovesOnlyItsWork(t *testing.T) {
	got := moved(t, artifacttest.Fixture, func(c *model.Catalog) {
		work(c, artifacttest.HailMary).Recordings[0].Chapters[1].Title = "Chapter One"
	})
	if want := []string{"works/project-hail-mary"}; !slices.Equal(got, want) {
		t.Errorf("moved %v, want %v", got, want)
	}
}

// TestRenamedCreditMovesEveryPageShowingIt: a person's name is shown on their
// own page, on every work crediting them, on the guides of the works they wrote
// (the byline), on the series listing those works and on every OTHER person's
// page listing one of those works by its card's byline - and nowhere else.
func TestRenamedCreditMovesEveryPageShowingIt(t *testing.T) {
	cases := []struct {
		person string
		want   []string
	}{
		// An author: the guides carry the byline, and Ray Porter's page lists the
		// book he narrated by its author.
		{"andy-weir", []string{"characters/project-hail-mary", "people/andy-weir", "people/ray-porter",
			"recaps/project-hail-mary", "works/project-hail-mary"}},
		// A narrator: no guide and no card shows a narrator.
		{"ray-porter", []string{"people/ray-porter", "works/project-hail-mary"}},
		// A series author: the series page, its books, and the narrators' pages
		// listing those books.
		{"brandon-sanderson", []string{"people/brandon-sanderson", "people/kate-reading", "people/michael-kramer",
			"series/the-stormlight-archive", "works/edgedancer", "works/the-way-of-kings", "works/words-of-radiance"}},
	}
	for _, tc := range cases {
		t.Run(tc.person, func(t *testing.T) {
			got := moved(t, artifacttest.Fixture, func(c *model.Catalog) { person(c, tc.person).Name += " Jr." })
			if !slices.Equal(got, tc.want) {
				t.Errorf("moved %v\nwant %v", got, tc.want)
			}
		})
	}
}

func TestRelationshipChangesMoveBothEnds(t *testing.T) {
	// A series membership: the work, its guides (they link the first series), the
	// series, and the people whose pages show the work's card (which names its
	// series).
	got := moved(t, artifacttest.Fixture, func(c *model.Catalog) {
		c.Series[0].Works = append(c.Series[0].Works, model.SeriesWork{Work: artifacttest.HailMary, Position: "3"})
	})
	want := []string{"characters/project-hail-mary", "people/andy-weir", "people/ray-porter",
		"recaps/project-hail-mary", "series/the-stormlight-archive", "works/project-hail-mary"}
	if !slices.Equal(got, want) {
		t.Errorf("series membership moved %v\nwant %v", got, want)
	}

	// A translation link: both works, and nothing that does not show it.
	got = moved(t, artifacttest.Languages, func(c *model.Catalog) {
		work(c, "buch-eins").TranslationOf = []string{"book-one", "book-two"}
	})
	if want := []string{"works/book-two", "works/buch-eins"}; !slices.Equal(got, want) {
		t.Errorf("translation link moved %v\nwant %v", got, want)
	}
}

func TestProvenanceMovesNothing(t *testing.T) {
	got := moved(t, artifacttest.Fixture, func(c *model.Catalog) {
		w := work(c, artifacttest.HailMary)
		w.AddedAt = "2026-11-02T00:00:00Z"
		w.Sources = append(w.Sources, model.Source{Type: "community", Ref: "probe"})
	})
	if len(got) != 0 {
		t.Errorf("a provenance-only change moved %v", got)
	}
}

func TestNewPageIsDated(t *testing.T) {
	got := moved(t, artifacttest.Fixture, func(c *model.Catalog) {
		c.Works = append(c.Works, &model.Work{
			ID: "the-martian", Title: "The Martian", Language: "en", Authors: []string{"andy-weir"}, License: "CC0-1.0",
		})
	})
	// The new work, and its author's page, which lists it.
	if want := []string{"people/andy-weir", "works/the-martian"}; !slices.Equal(got, want) {
		t.Errorf("moved %v, want %v", got, want)
	}
}

// TestColdStartLeavesUnchangedPagesUnknown: the first release stamped against an
// artifact older than the revision layer dates only what changed; an unchanged
// page has no date, rather than the release's.
func TestColdStartLeavesUnchangedPagesUnknown(t *testing.T) {
	old := artifacttest.Downgraded(t, artifacttest.Fixture(), 13, "page_revisions")
	if got := rows(t, stamp(t, artifacttest.Fixture(), old, t2)); len(got) != 0 {
		t.Errorf("an unchanged cold start dated %v", got)
	}
	next := artifacttest.Fixture()
	next.Recaps[0].Ending = "Grace stays on Erid, teaching."
	got := rows(t, stamp(t, next, old, t2))
	if want := map[string]string{"recaps/project-hail-mary": t2.Format(time.RFC3339)}; !maps.Equal(got, want) {
		t.Errorf("cold start dated %v, want %v", got, want)
	}
}

func TestRevertAdvancesTheDate(t *testing.T) {
	base := baseline(t, artifacttest.Fixture(), t1)
	edited := artifacttest.Fixture()
	edited.Recaps[0].InShort = "Somebody else's summary."
	mid := stamp(t, edited, base, t2)
	back := rows(t, stamp(t, artifacttest.Fixture(), mid, t3))
	if got := back["recaps/project-hail-mary"]; got != t3.Format(time.RFC3339) {
		t.Errorf("a reverted recap page is dated %s, want %s: going back is a change too", got, t3.Format(time.RFC3339))
	}
	if got := back["works/project-hail-mary"]; got != t1.Format(time.RFC3339) {
		t.Errorf("the untouched work page is dated %s, want %s", got, t1.Format(time.RFC3339))
	}
}

// TestStampIsClampedToThePreviousRelease: a stamp older than the previous
// release's never dates a change before it.
func TestStampIsClampedToThePreviousRelease(t *testing.T) {
	base := baseline(t, artifacttest.Fixture(), t2)
	next := artifacttest.Fixture()
	next.Descriptions[0].Text += " More."
	out := stamp(t, next, base, t1)
	if got := rows(t, out)["works/project-hail-mary"]; got != t2.Format(time.RFC3339) {
		t.Errorf("changed page dated %s, want the clamped %s", got, t2.Format(time.RFC3339))
	}
	if v := metaValue(t, out, "revisions_at"); v != t2.Format(time.RFC3339) {
		t.Errorf("meta(revisions_at) = %q, want %s", v, t2.Format(time.RFC3339))
	}
}

func TestUnreadablePreviousIsAnError(t *testing.T) {
	garbage := filepath.Join(t.TempDir(), "garbage.sqlite")
	if err := os.WriteFile(garbage, []byte("not a database"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, prev := range map[string]string{
		"missing": filepath.Join(t.TempDir(), "absent.sqlite"),
		"garbage": garbage,
		"none":    "",
	} {
		t.Run(name, func(t *testing.T) {
			out := artifacttest.Build(t, artifacttest.Fixture())
			if _, err := Stamp(context.Background(), out, Options{Previous: prev, At: t1, Workers: 1}); err == nil {
				t.Fatal("stamped against an unreadable previous release")
			}
			if got := rows(t, out); len(got) != 0 {
				t.Errorf("a refused stamp wrote %v", got)
			}
		})
	}
}

func TestStampingTwiceIsRefused(t *testing.T) {
	empty := artifacttest.Build(t, &model.Catalog{})
	out := stamp(t, artifacttest.Fixture(), empty, t1)
	if _, err := Stamp(context.Background(), out, Options{Previous: empty, At: t2, Workers: 1}); err == nil ||
		!strings.Contains(err.Error(), "already stamped") {
		t.Fatalf("second stamp: err = %v, want already stamped", err)
	}
}

// TestRowsStayWithinTheirFamily: a family never holds a date for a page the
// sitemap does not list.
func TestRowsStayWithinTheirFamily(t *testing.T) {
	path := baseline(t, artifacttest.Fixture(), t1)
	db, err := query.Open(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	perFamily := map[string]int{}
	for k := range rows(t, path) {
		family, _, _ := strings.Cut(k, "/")
		perFamily[family]++
	}
	for _, family := range Families {
		listed := map[string]bool{}
		entries, err := db.Pages(family, 1000, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			listed[e.ID] = true
			if e.RevisedAt == nil || *e.RevisedAt != t1.Format(time.RFC3339) {
				t.Errorf("%s/%s: listed revision %v, want %s", family, e.ID, e.RevisedAt, t1.Format(time.RFC3339))
			}
		}
		if n, max := perFamily[string(family)], db.PageCount(family); n > max {
			t.Errorf("%s: %d dated pages, but the family holds %d", family, n, max)
		}
	}
}

func TestDigestsAreIndependentOfTheWorkers(t *testing.T) {
	db, err := query.Open(artifacttest.Build(t, artifacttest.Fixture()), "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	one, err := Digests(context.Background(), db, 1)
	if err != nil {
		t.Fatal(err)
	}
	many, err := Digests(context.Background(), db, 8)
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(one, many) {
		t.Error("digests depend on the worker count")
	}
}

// ---- the field decisions ----------------------------------------------------

// digestRoots is every document type a digest reads, as Digests reaches them.
var digestRoots = []reflect.Type{
	reflect.TypeFor[workFacts](), reflect.TypeFor[guideFacts](), reflect.TypeFor[recordingChapters](),
	reflect.TypeFor[query.PersonDetail](), reflect.TypeFor[query.PersonRecordingsPage](),
	reflect.TypeFor[query.SeriesListeningGuide](),
}

// TestEveryDocumentFieldIsDecided: every field of every struct a digest reaches
// is hashed or excluded, and every decision names a real field - so a field added
// to WorkDetail, RecordingDetail, PersonDetail, SeriesListeningGuide or anything
// they carry fails here until it is decided, and a decision cannot outlive its
// field. (The walk refuses an undecided field at run time too; this is the
// guard that sees the field before any data carries a value for it.)
func TestEveryDocumentFieldIsDecided(t *testing.T) {
	seen := map[reflect.Type]bool{}
	var walk func(reflect.Type)
	walk = func(tp reflect.Type) {
		switch tp.Kind() {
		case reflect.Pointer, reflect.Slice:
			walk(tp.Elem())
			return
		case reflect.Struct:
		case reflect.String, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return
		default:
			t.Errorf("%s: a %s cannot be digested", tp, tp.Kind())
			return
		}
		if seen[tp] {
			return
		}
		seen[tp] = true
		d, ok := decisions[tp]
		if !ok {
			t.Errorf("%s has no field decisions", tp)
			return
		}
		names := map[string]bool{}
		for _, f := range documentFields(tp) {
			names[f.Name] = true
			hashed := slices.Contains(d.hashed, f.Name)
			if hashed == slices.Contains(d.excluded, f.Name) {
				t.Errorf("%s.%s must be EITHER hashed or excluded", tp, f.Name)
			}
			if hashed {
				walk(f.Type) // an excluded field's value is never read
			}
		}
		for _, n := range append(slices.Clone(d.hashed), d.excluded...) {
			if !names[n] {
				t.Errorf("%s: decision for %s, which is not one of its fields", tp, n)
			}
		}
	}
	for _, root := range digestRoots {
		walk(root)
	}
	for tp := range decisions {
		if !seen[tp] {
			t.Errorf("decisions for %s, which no digest reaches", tp)
		}
	}
}

func TestAnUndecidedFieldIsRefused(t *testing.T) {
	type undecided struct{ Name string }
	if _, err := digestOf(query.WorkPages, undecided{"x"}); err == nil {
		t.Fatal("digested a struct with no field decisions")
	}
}
