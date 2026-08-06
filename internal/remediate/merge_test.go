package remediate

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
)

// blackPrismParts is the worked example throughout: a three-part GraphicAudio
// dramatization, the shape the live tree holds 175 of.
func blackPrismParts() []workSpec {
	return []workSpec{
		{Slug: "black-prism-1-of-3-dramatized-adaptation", Title: "Black Prism (1 of 3) [Dramatized Adaptation]",
			Authors: []string{"brent-weeks"}, Genres: []string{"epic-fantasy"}, AddedAt: "2026-07-25",
			Rec: recSpec{Key: "full-cast-2020", ASINs: []string{"us:B0PART0001"}, Runtime: 403, Release: "2020",
				Cover: "https://example.test/1.jpg", Chapters: true, AddedAt: "2026-07-25"}},
		{Slug: "black-prism-2-of-3-dramatized-adaptation", Title: "Black Prism (2 of 3) [Dramatized Adaptation]",
			Authors: []string{"brent-weeks"}, Genres: []string{"fantasy"}, AddedAt: "2026-07-26",
			Rec: recSpec{Key: "full-cast-2020", ASINs: []string{"us:B0PART0002"}, Runtime: 393, Release: "2020-06-01",
				Chapters: true, AddedAt: "2026-07-26"}},
		{Slug: "black-prism-3-of-3-dramatized-adaptation", Title: "Black Prism (3 of 3) [Dramatized Adaptation]",
			Authors: []string{"brent-weeks"}, AddedAt: "2026-07-27",
			Rec: recSpec{Key: "full-cast-2020", ASINs: []string{"us:B0PART0003"}, Runtime: 377, Release: "2020-07-01",
				Chapters: true, AddedAt: "2026-07-27"}},
	}
}

// TestMintsAWholeBookWorkFromACompleteSetRow is the enriched mint: the dump
// states the product's real title, identifier, length and chapters, so the
// merged work carries them rather than anything derived.
func TestMintsAWholeBookWorkFromACompleteSetRow(t *testing.T) {
	dir := t.TempDir()
	seedWorks(t, dir, blackPrismParts()...)
	sets := writeCompleteSets(t, map[string]any{
		"asin": "B09FRBS927", "title": "Black Prism [Dramatized Adaptation]", "region": "us",
		"publisher": "GraphicAudio", "releaseDate": "2019-11-05T00:00:00Z",
		"imageUrl": "https://example.test/set.jpg", "lengthMinutes": 1173,
		"authors":  []any{map[string]any{"name": "Brent Weeks"}},
		"chapters": []any{map[string]any{"title": "Part One", "startOffsetMs": 0, "lengthMs": 120000}},
	})

	rep := run(t, dir, true, sets)
	if rep.MatchedSets != 1 {
		t.Fatalf("matched %d complete-set rows, want 1", rep.MatchedSets)
	}

	const slug = "black-prism-dramatized-adaptation"
	work := readWork(t, dir, slug)
	if got := work.str("title"); got != "Black Prism [Dramatized Adaptation]" {
		t.Errorf("title = %q, want the dump row's verbatim", got)
	}
	if got := work.strs("genres"); !reflect.DeepEqual(got, []string{"epic-fantasy", "fantasy"}) {
		t.Errorf("genres = %v, want the parts' union, sorted", got)
	}
	if got := work.str("added_at"); got != "2026-07-25" {
		t.Errorf("added_at = %q, want the earliest part's", got)
	}

	_, rec := readRecording(t, dir, slug)
	if got, want := asinValues(rec), []string{"B09FRBS927", "B0PART0001", "B0PART0002", "B0PART0003"}; !reflect.DeepEqual(got, want) {
		t.Errorf("asin = %v, want the complete set first then the parts in order (%v)", got, want)
	}
	if got, _ := rec.intAt("runtime_min"); got != 1173 {
		t.Errorf("runtime_min = %d, want the dump row's 1173 rather than the parts' sum", got)
	}
	if got := rec.str("release_date"); got != "2019-11-05" {
		t.Errorf("release_date = %q, want the earliest stated", got)
	}
	// The whole-book product states its own chapters, so the merged recording
	// carries those - never the parts' timelines concatenated.
	var chapters []map[string]any
	if err := json.Unmarshal(rec["chapters"], &chapters); err != nil {
		t.Fatalf("chapters: %v", err)
	}
	if len(chapters) != 1 || chapters[0]["title"] != "Part One" {
		t.Errorf("chapters = %v, want the complete-set row's", chapters)
	}
	if !sourcesInclude(rec, "libex-import", "B09FRBS927", testToday) {
		t.Errorf("the recording must record the complete-set row's provenance: %s", rec["sources"])
	}

	for _, p := range blackPrismParts() {
		if testpack.Exists(t, dir, workAddr(p.Slug)) {
			t.Errorf("part %s survived the merge", p.Slug)
		}
	}
}

// TestMintsADerivedTitleWithoutACompleteSetRow is the same merge with no dump
// rows at all: a complete outcome, composed from the parts alone.
func TestMintsADerivedTitleWithoutACompleteSetRow(t *testing.T) {
	dir := t.TempDir()
	seedWorks(t, dir, blackPrismParts()...)

	run(t, dir, true, "")

	const slug = "black-prism-dramatized-adaptation"
	work := readWork(t, dir, slug)
	if got := work.str("title"); got != "Black Prism [Dramatized Adaptation]" {
		t.Errorf("title = %q, want the base title plus the edition marker", got)
	}
	_, rec := readRecording(t, dir, slug)
	if got, _ := rec.intAt("runtime_min"); got != 403+393+377 {
		t.Errorf("runtime_min = %d, want the complete set's sum", got)
	}
	if got, want := asinValues(rec), []string{"B0PART0001", "B0PART0002", "B0PART0003"}; !reflect.DeepEqual(got, want) {
		t.Errorf("asin = %v, want the parts in order (%v)", got, want)
	}
	// No whole-book source states a chapter timeline, so there is none.
	if rec.has("chapters") {
		t.Errorf("the merged recording must not carry concatenated part chapters: %s", rec["chapters"])
	}
	if got := rec.str("cover_url"); got != "https://example.test/1.jpg" {
		t.Errorf("cover_url = %q, want part 1's", got)
	}
}

// TestIncompletePartGroupOmitsRuntime pins the omit-never-guess rule: the sum
// of the parts that happen to be here is not the book's length.
func TestIncompletePartGroupOmitsRuntime(t *testing.T) {
	dir := t.TempDir()
	parts := blackPrismParts()
	seedWorks(t, dir, parts[0], parts[2]) // 1 and 3 of 3; part 2 is missing

	run(t, dir, true, "")

	_, rec := readRecording(t, dir, "black-prism-dramatized-adaptation")
	if rec.has("runtime_min") {
		t.Errorf("runtime_min = %s, want it omitted for an incomplete part set", rec["runtime_min"])
	}
	if got, want := asinValues(rec), []string{"B0PART0001", "B0PART0003"}; !reflect.DeepEqual(got, want) {
		t.Errorf("asin = %v, want %v", got, want)
	}
}

// TestMergesIntoAnExistingCompleteSetWork is the commonest live case: the
// catalogue already holds the whole-book product, so its record is the one that
// survives and its recorded facts win.
func TestMergesIntoAnExistingCompleteSetWork(t *testing.T) {
	dir := t.TempDir()
	works := append(blackPrismParts(), workSpec{
		Slug: "black-prism-dramatized-adaptation", Title: "Black Prism [Dramatized Adaptation]",
		Authors: []string{"brent-weeks"}, AddedAt: "2026-07-20",
		Rec: recSpec{Key: "full-cast-2019", ASINs: []string{"us:B09FRBS927"}, Runtime: 1173,
			Release: "2019-11-05", Cover: "https://example.test/set.jpg", AddedAt: "2026-07-20"},
	})
	seedWorks(t, dir, works...)

	rep := run(t, dir, true, "")
	if len(rep.Merged) != 1 || rep.Merged[0].Minted {
		t.Fatalf("merged = %+v, want one enrichment of the existing work", rep.Merged)
	}

	const slug = "black-prism-dramatized-adaptation"
	work := readWork(t, dir, slug)
	if got := work.str("title"); got != "Black Prism [Dramatized Adaptation]" {
		t.Errorf("title = %q, want the recorded one unchanged", got)
	}
	if got := work.str("added_at"); got != "2026-07-20" {
		t.Errorf("added_at = %q, want the earliest across the work and its parts", got)
	}
	key, rec := readRecording(t, dir, slug)
	if key != "full-cast-2019" {
		t.Errorf("recording key = %q, want the existing work's", key)
	}
	if got, _ := rec.intAt("runtime_min"); got != 1173 {
		t.Errorf("runtime_min = %d, want the recorded whole-book length", got)
	}
	if got, want := asinValues(rec), []string{"B09FRBS927", "B0PART0001", "B0PART0002", "B0PART0003"}; !reflect.DeepEqual(got, want) {
		t.Errorf("asin = %v, want %v", got, want)
	}
	if rec.has("chapters") {
		t.Errorf("the existing work stated no chapters, so the merge must not invent any: %s", rec["chapters"])
	}
	for _, p := range blackPrismParts() {
		if testpack.Exists(t, dir, workAddr(p.Slug)) {
			t.Errorf("part %s survived the merge", p.Slug)
		}
	}
}

// TestAbridgedIsCarriedOnlyWhenEveryPartStatesIt pins the tri-state: absence
// means unknown, and one silent part makes the whole set silent.
func TestAbridgedIsCarriedOnlyWhenEveryPartStatesIt(t *testing.T) {
	uniform := blackPrismParts()
	for i := range uniform {
		uniform[i].Rec.Abridged = ptr(false)
	}
	dir := t.TempDir()
	seedWorks(t, dir, uniform...)
	run(t, dir, true, "")
	_, rec := readRecording(t, dir, "black-prism-dramatized-adaptation")
	if got := string(rec["abridged"]); got != "false" {
		t.Errorf("abridged = %q, want the value every part states", got)
	}

	mixed := blackPrismParts()
	mixed[0].Rec.Abridged = ptr(false)
	other := t.TempDir()
	seedWorks(t, other, mixed...)
	run(t, other, true, "")
	_, rec2 := readRecording(t, other, "black-prism-dramatized-adaptation")
	if rec2.has("abridged") {
		t.Errorf("abridged = %s, want it omitted when a part states nothing", rec2["abridged"])
	}
}

// sourcesInclude reports whether a record's provenance names a source.
func sourcesInclude(o obj, kind, ref, at string) bool {
	for _, s := range o.sources() {
		if s.Type == kind && s.Ref == ref && s.ImportedAt == at {
			return true
		}
	}
	return false
}

// TestTheCompleteSetsCoverAndPublisherWinOnAMint: a part's cover pictures the
// part, and the whole-book product's pictures the book.
func TestTheCompleteSetsCoverAndPublisherWinOnAMint(t *testing.T) {
	dir := t.TempDir()
	seedWorks(t, dir, blackPrismParts()...)
	sets := writeCompleteSets(t, map[string]any{
		"asin": "B09FRBS927", "title": "Black Prism [Dramatized Adaptation]", "region": "us",
		"publisher": "Graphic Audio LLC", "imageUrl": "https://example.test/set.jpg",
		"authors": []any{map[string]any{"name": "Brent Weeks"}},
	})

	run(t, dir, true, sets)

	_, rec := readRecording(t, dir, "black-prism-dramatized-adaptation")
	if got := rec.str("cover_url"); got != "https://example.test/set.jpg" {
		t.Errorf("cover_url = %q, want the complete set's", got)
	}
	if got := rec.str("publisher"); got != "Graphic Audio LLC" {
		t.Errorf("publisher = %q, want the complete set's", got)
	}
}

// TestTheModalPublisherWinsWithoutACompleteSetRow: the imprint the parts agree
// on, not whichever spelling part one happened to carry.
func TestTheModalPublisherWinsWithoutACompleteSetRow(t *testing.T) {
	dir := t.TempDir()
	parts := blackPrismParts()
	parts[0].Rec.Publisher = "Graphic Audio LLC"
	parts[1].Rec.Publisher = "GraphicAudio"
	parts[2].Rec.Publisher = "GraphicAudio"
	seedWorks(t, dir, parts...)

	run(t, dir, true, "")

	_, rec := readRecording(t, dir, "black-prism-dramatized-adaptation")
	if got := rec.str("publisher"); got != "GraphicAudio" {
		t.Errorf("publisher = %q, want the spelling the parts mostly use", got)
	}
}
