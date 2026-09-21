package serve

import (
	"slices"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// TestBatchQueriesTolerateRepeatedIDs pins the dedupe that lives in eachChunk.
//
// Callers legitimately hand these helpers a repeated id - a narrator credited on
// two recordings of one work yields that work twice - and every helper APPENDS
// its rows into a map keyed by the id. A repeat inside one chunk is harmless
// (the IN list matches each row once), but a repeat that lands in a SECOND chunk
// runs the query again and appends the same rows on top of the first answer, so
// the work comes back with its narrators listed twice. The list is chunked
// precisely because it can be long, so this is reachable rather than theoretical
// - hence a repeat count that crosses idChunkSize.
func TestBatchQueriesTolerateRepeatedIDs(t *testing.T) {
	snap, err := openSnapshot(buildFixtureDB(t, fixtureCatalog()), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(snap.close)

	const workID = "the-way-of-kings" // the two-narrator recording
	once, err := snap.narratorsByWork([]string{workID})
	if err != nil {
		t.Fatal(err)
	}
	if len(once[workID]) != 2 {
		t.Fatalf("narratorsByWork(one id) = %v, want the recording's two narrators", once[workID])
	}

	// Enough repeats to span two chunks, which is the only shape that breaks.
	ids := make([]string, idChunkSize+1)
	for i := range ids {
		ids[i] = workID
	}
	repeated, err := snap.narratorsByWork(ids)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(repeated[workID], once[workID]) {
		t.Errorf("narratorsByWork(%d repeats) = %v, want the same as one id: %v",
			len(ids), repeated[workID], once[workID])
	}

	cards, err := snap.cardsByID(ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 || cards[workID] == nil || len(cards[workID].Authors) != 1 {
		t.Errorf("cardsByID(%d repeats) = %+v, want one card with one author", len(ids), cards)
	}
}

// TestNarratorsByWorkOrderIsTotal pins the tie-break in the ORDER BY. The
// narrators of one recording share a MIN(ord) across a work only as far as
// MIN(ord) can tell - a dual-narrator recording gives each of them ord 0 and 1,
// but a work with several recordings can hand two people the same minimum. With
// MIN(ord) alone the planner picks the order, so the same artifact could answer
// the same request two ways; the person id makes it total.
func TestNarratorsByWorkOrderIsTotal(t *testing.T) {
	if !strings.HasSuffix(narratorsByWorkSQL("?"), "MIN(rn.ord), p.id") {
		t.Errorf("narratorsByWorkSQL does not break MIN(ord) ties on the person id: %s", narratorsByWorkSQL("?"))
	}

	snap, err := openSnapshot(buildFixtureDB(t, fixtureCatalog()), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(snap.close)

	// Credit order still wins where it says something: the fixture's recording
	// credits Michael Kramer first.
	got, err := snap.narratorsByWork([]string{"the-way-of-kings"})
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, 2)
	for _, p := range got["the-way-of-kings"] {
		names = append(names, p.ID)
	}
	if !slices.Equal(names, []string{"michael-kramer", "kate-reading"}) {
		t.Errorf("narrators = %v, want credit order (michael-kramer, kate-reading)", names)
	}
}

// TestCardFactsPickTheEarliestReleaseDate pins the card's release_date rule: the
// minimum by plain STRING order over the recordings that state one, absent when
// none does. Both halves matter to the site - a work's entry on a series page
// is "when did this come out", and a date in the FUTURE is what marks a
// catalogued preorder - so the rule may not quietly become "the first
// recording's date" or "the newest".
//
// It also re-pins the cover rule, which now shares the query: the first
// NON-EMPTY cover in recording id order, unaffected by a recording that sorts
// earlier and carries none.
func TestCardFactsPickTheEarliestReleaseDate(t *testing.T) {
	rec := func(id, release, cover string) *model.Recording {
		return &model.Recording{
			ID: id, Work: "many-narrations", Language: "en", License: "CC0-1.0",
			ReleaseDate: release, CoverURL: cover, Narrators: []string{"ray-porter"},
		}
	}
	cat := fixtureCatalog()
	cat.Works = append(cat.Works,
		&model.Work{
			ID: "many-narrations", Title: "Many Narrations", Language: "en",
			Authors: []string{"andy-weir"}, License: "CC0-1.0",
			Recordings: []*model.Recording{
				// Deliberately NOT in date order, and the id order (which the
				// cover follows) disagrees with it: "a-" sorts first and states
				// the LATEST date and no cover.
				rec("a-2024", "2024-03-02", ""),
				rec("b-1999", "1999", "https://example.test/b.jpg"),
				rec("c-1999-07", "1999-07-01", "https://example.test/c.jpg"),
			},
		},
		&model.Work{
			ID: "no-dates", Title: "No Dates", Language: "en",
			Authors: []string{"andy-weir"}, License: "CC0-1.0",
			Recordings: []*model.Recording{{
				ID: "undated", Work: "no-dates", Language: "en", License: "CC0-1.0",
				Narrators: []string{"ray-porter"},
			}},
		},
	)

	snap, err := openSnapshot(buildFixtureDB(t, cat), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(snap.close)

	cards, err := snap.cardsByID([]string{"many-narrations", "no-dates", "project-hail-mary"})
	if err != nil {
		t.Fatal(err)
	}
	// "1999" beats "1999-07-01" by string order, which is the documented answer
	// for a year stated at two precisions: both describe the same year.
	if got := cards["many-narrations"].ReleaseDate; got != "1999" {
		t.Errorf("earliest release_date = %q, want 1999", got)
	}
	if got := cards["many-narrations"].CoverURL; got == nil || *got != "https://example.test/b.jpg" {
		t.Errorf("cover = %v, want the first non-empty one in recording id order", got)
	}
	if got := cards["no-dates"].ReleaseDate; got != "" {
		t.Errorf("release_date of a work whose recordings state none = %q, want empty", got)
	}
	if got := cards["project-hail-mary"].ReleaseDate; got != "2021-05-04" {
		t.Errorf("single-recording release_date = %q, want 2021-05-04", got)
	}
}
