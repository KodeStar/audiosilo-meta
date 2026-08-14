package audit

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

func TestHygieneReportsARecordingWithNoIdentifier(t *testing.T) {
	rep := runFixture(t, fixture(t, map[string]string{
		"works/pl/plain/work.json":            workJSON(t, "plain", "Plain Story"),
		"works/pl/plain/recordings/only.json": recJSON(t, "only", "plain", withoutIdentifiers()),
	}))
	got := subclassOf(t, rep, ClassHygiene, hygRecNoIdentifier)
	if len(got) != 1 {
		t.Fatalf("want one record, got %d: %+v", len(got), classOf(t, rep, ClassHygiene))
	}
	if got[0].Recording != "only" || got[0].Key != "plain/only" {
		t.Errorf("record = key %q recording %q", got[0].Key, got[0].Recording)
	}
}

// Three of the field rules cannot be reached through a fixture TREE: the schema
// requires a language and at least one author on a work and one narrator on a
// recording, so pkg/check would never hand such a record over. They are defensive
// rules for a catalogue assembled some other way (pkg/check's Catalog is public
// API, and audiosilo-sidecars consumes it), so they are driven directly - and a
// schema relaxation would find them already here rather than needing them written.
func TestHygieneReportsTheSchemaUnreachableFieldGaps(t *testing.T) {
	rec := &model.Recording{ID: "only", Work: "plain", License: "CC0-1.0"}
	work := &model.Work{
		ID: "plain", Title: "Plain Story", License: "CC0-1.0",
		Recordings: []*model.Recording{rec},
	}
	ix := newIndex(&model.Catalog{Works: []*model.Work{work}})
	f, _ := detectHygiene(ix)

	byClass := map[string]int{}
	for _, r := range f.sorted() {
		byClass[r.Subclass]++
	}
	for _, want := range []string{hygWorkNoLanguage, hygWorkNoAuthors, hygRecNoNarrators, hygRecNoIdentifier} {
		if byClass[want] != 1 {
			t.Errorf("%s: got %d records, want 1 (all: %v)", want, byClass[want], byClass)
		}
	}
}

func TestHygieneReportsAWorkWithNoRecordings(t *testing.T) {
	rep := runFixture(t, fixture(t, map[string]string{
		"works/pl/plain/work.json": workJSON(t, "plain", "Plain Story"),
	}))
	if got := subclassOf(t, rep, ClassHygiene, hygWorkNoRecordings); len(got) != 1 {
		t.Fatalf("want one record, got %d: %+v", len(got), classOf(t, rep, ClassHygiene))
	}
}

func TestHygieneIsSilentOnACompleteRecord(t *testing.T) {
	rep := runFixture(t, fixture(t, map[string]string{
		"works/pl/plain/work.json":            workJSON(t, "plain", "Plain Story"),
		"works/pl/plain/recordings/only.json": recJSON(t, "only", "plain"),
	}))
	if got := classOf(t, rep, ClassHygiene); len(got) != 0 {
		t.Errorf("a complete record produced findings: %+v", got)
	}
}

func TestHygieneReportsADoubledSlugToken(t *testing.T) {
	rep := runFixture(t, fixture(t, map[string]string{
		"works/xx/simple-italian-italian-edition/work.json": workJSON(t,
			"simple-italian-italian-edition", "Conversations in Simple Italian: Italian Edition"),
		"works/xx/simple-italian-italian-edition/recordings/a.json": recJSON(t, "a", "simple-italian-italian-edition"),
	}))
	got := subclassOf(t, rep, ClassHygiene, hygSlugDoubledToken)
	if len(got) != 1 {
		t.Fatalf("want one record, got %d: %+v", len(got), classOf(t, rep, ClassHygiene))
	}
	if !strings.Contains(strings.Join(got[0].Notes, " "), "italian") {
		t.Errorf("the repeated run is not named: %v", got[0].Notes)
	}
	// A slug rule never proposes the rename: a slug is identity.
	if got[0].Want != "" {
		t.Errorf("want = %q; a doubled-token slug rule proposes no rename", got[0].Want)
	}
}

// A slug's numbers repeat innocently, and so do initials: neither is a title
// restating itself.
func TestHygieneIgnoresInnocentSlugRepetition(t *testing.T) {
	for _, c := range []struct{ id, title string }{
		{"10-000-000-marriage-proposal", "10,000,000 Marriage Proposal"},
		{"1-1", "1 1"},
		{"a-a-milnes-winnie-the-pooh", "A.A. Milne's Winnie-the-Pooh"},
	} {
		t.Run(c.id, func(t *testing.T) {
			rep := runFixture(t, fixture(t, map[string]string{
				"works/xx/" + c.id + "/work.json":         workJSON(t, c.id, c.title),
				"works/xx/" + c.id + "/recordings/a.json": recJSON(t, "a", c.id),
			}))
			for _, f := range classOf(t, rep, ClassHygiene) {
				if f.Subclass == hygSlugDoubledToken || f.Subclass == hygSlugLeadArticle {
					t.Errorf("%s: %s reported innocently (%v)", c.id, f.Subclass, f.Notes)
				}
			}
		})
	}
}

func TestHygieneReportsADanglingLeadingArticle(t *testing.T) {
	rep := runFixture(t, fixture(t, map[string]string{
		"works/xx/a-mageling/work.json":         workJSON(t, "a-mageling", "Mageling"),
		"works/xx/a-mageling/recordings/a.json": recJSON(t, "a", "a-mageling"),
	}))
	got := subclassOf(t, rep, ClassHygiene, hygSlugLeadArticle)
	if len(got) != 1 {
		t.Fatalf("want one record, got %d: %+v", len(got), classOf(t, rep, ClassHygiene))
	}
	if got[0].Want != "mageling" {
		t.Errorf("want = %q, expected the slug without the article", got[0].Want)
	}
}

func TestHygieneCountsTheAddedAtSplitWithoutListingIt(t *testing.T) {
	files := fixture(t, map[string]string{
		"works/xx/dated/work.json":           workJSON(t, "dated", "Dated"),
		"works/xx/dated/recordings/a.json":   recJSON(t, "a", "dated"),
		"works/xx/stamped/work.json":         withAddedAt(t, workJSON(t, "stamped", "Stamped"), "2026-01-01T10:00:00+00:00"),
		"works/xx/stamped/recordings/b.json": withAddedAt(t, recJSON(t, "b", "stamped"), "2026-01-01T10:00:00+00:00"),
	})
	rep := runFixture(t, files)
	if rep.Stats.WorkAddedDate != 1 || rep.Stats.WorkAddedStamp != 1 {
		t.Errorf("work added_at split = %d date / %d stamp, want 1 / 1",
			rep.Stats.WorkAddedDate, rep.Stats.WorkAddedStamp)
	}
	if rep.Stats.RecAddedDate != 1 || rep.Stats.RecAddedStamp != 1 {
		t.Errorf("recording added_at split = %d date / %d stamp, want 1 / 1",
			rep.Stats.RecAddedDate, rep.Stats.RecAddedStamp)
	}
	// It is a count-only advisory: no NDJSON record names an added_at.
	for _, f := range classOf(t, rep, ClassHygiene) {
		if strings.Contains(f.Field, "added_at") {
			t.Errorf("added_at produced a per-record finding: %+v", f)
		}
	}
	if !strings.Contains(summary(rep), "works with `added_at` as an RFC 3339 timestamp | 1") {
		t.Error("SUMMARY.md does not carry the added_at split")
	}
}

// A sidecar sitting on one half of a duplicate pair is the spoiler hazard the class
// exists for.
func TestRefSidecarFlagsASidecarOnADuplicateWork(t *testing.T) {
	rep := runFixture(t, fixture(t, map[string]string{
		"works/ha/hammered/work.json":                    workJSON(t, "hammered", "Hammered"),
		"works/ha/hammered/recordings/luke.json":         recJSON(t, "luke", "hammered"),
		"works/ha/hammered/characters.json":              charactersJSON(t, "hammered"),
		"works/ha/hammered-book-3/work.json":             workJSON(t, "hammered-book-3", "Hammered: The Druid Tales, Book 3"),
		"works/ha/hammered-book-3/recordings/chris.json": recJSON(t, "chris", "hammered-book-3"),
		"series/dr/druid-tales.json":                     seriesJSON(t, "druid-tales", "The Druid Tales", "hammered@3"),
	}))
	got := subclassOf(t, rep, ClassRefSidecar, sidecarInDupCluster)
	if len(got) != 1 {
		t.Fatalf("want one record, got %d: %+v", len(got), classOf(t, rep, ClassRefSidecar))
	}
	if got[0].Key != "hammered" {
		t.Errorf("key = %q, want the work carrying the sidecar", got[0].Key)
	}
	if want := []string{"hammered", "hammered-book-3"}; !reflect.DeepEqual(workIDs(got[0]), want) {
		t.Errorf("works = %v, want the sidecar's work and its cluster sibling %v", workIDs(got[0]), want)
	}
	note := strings.Join(got[0].Notes, " ")
	if !strings.Contains(note, "characters") {
		t.Errorf("the sidecar members are not named: %q", note)
	}
	if !strings.Contains(got[0].Action, "not a mechanical decision") {
		t.Errorf("action = %q, want it to refuse to decide", got[0].Action)
	}
}

func TestRefSidecarIsSilentWhenTheWorkIsUnambiguous(t *testing.T) {
	rep := runFixture(t, fixture(t, map[string]string{
		"works/ha/hammered/work.json":            workJSON(t, "hammered", "Hammered"),
		"works/ha/hammered/recordings/luke.json": recJSON(t, "luke", "hammered"),
		"works/ha/hammered/characters.json":      charactersJSON(t, "hammered"),
	}))
	if got := classOf(t, rep, ClassRefSidecar); len(got) != 0 {
		t.Errorf("an unambiguous sidecar was flagged: %+v", got)
	}
}

func TestRefSidecarReportsAnOrphanSidecar(t *testing.T) {
	rep, res := runFixtureAllowingProblems(t, fixture(t, map[string]string{
		"works/ha/hammered/work.json":            workJSON(t, "hammered", "Hammered"),
		"works/ha/hammered/recordings/luke.json": recJSON(t, "luke", "hammered"),
		"works/gh/ghost-work/characters.json":    charactersJSON(t, "ghost-work"),
	}))
	if len(res.Problems) == 0 {
		t.Fatal("an orphan sidecar was supposed to fail validation")
	}
	got := subclassOf(t, rep, ClassRefSidecar, sidecarOrphanWork)
	if len(got) != 1 {
		t.Fatalf("want one orphan-work record, got %d: %+v", len(got), classOf(t, rep, ClassRefSidecar))
	}
	if got[0].Have != "ghost-work" {
		t.Errorf("have = %q, want the missing work slug", got[0].Have)
	}
}

func TestAddedAtShapeReadsTheTwoAcceptedSpellings(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", addedNone},
		{"2026-08-14", addedDate},
		{"2026-08-14T10:11:12+01:00", addedStamp},
	}
	for _, c := range cases {
		if got := addedAtShape(c.in); got != c.want {
			t.Errorf("addedAtShape(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
