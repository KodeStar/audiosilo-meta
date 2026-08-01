package migrate

import (
	"encoding/json"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// The git-log shape the walk parses, with the two things it has to get right:
// a path's FIRST appearance wins (dune's work.json is added, deleted and added
// again), and a recording is dated on its own path rather than its work's.
const sampleLog = "COMMIT\t2026-01-05T10:00:00+01:00\n" +
	"data/works/du/dune/work.json\n" +
	"data/works/du/dune/recordings/ann-doe-2021.json\n" +
	"\n" +
	"COMMIT\t2026-02-06T09:30:00Z\n" +
	"data/works/du/dune/recordings/bob-roe-2020.json\n" +
	"data/works/em/emma/work.json\n" +
	"\n" +
	"COMMIT\t2026-03-07T12:00:00-05:00\n" +
	"data/works/du/dune/work.json\n" +
	"data/works/du/dune/characters.json\n"

func TestParseAddedLog(t *testing.T) {
	dates := parseAddedLog([]byte(sampleLog))

	// The author date is taken VERBATIM, offset and all: the artifact's bytes
	// after the migration have to equal the ones the retired release.yml walk
	// produced, which is what the equivalence proof compares.
	if got := dates.works["dune"]; got != "2026-01-05T10:00:00+01:00" {
		t.Errorf("dune added_at = %q, want the FIRST commit's date, verbatim", got)
	}
	if got := dates.works["emma"]; got != "2026-02-06T09:30:00Z" {
		t.Errorf("emma added_at = %q", got)
	}
	if got := dates.rec("dune", "ann-doe-2021"); got != "2026-01-05T10:00:00+01:00" {
		t.Errorf("ann-doe-2021 added_at = %q", got)
	}
	if got := dates.rec("dune", "bob-roe-2020"); got != "2026-02-06T09:30:00Z" {
		t.Errorf("bob-roe-2020 added_at = %q, want its OWN commit's date", got)
	}
	// Sidecars carry no added_at: the field is on works and recordings only.
	if len(dates.works) != 2 {
		t.Errorf("works dated = %v, want dune and emma only", dates.works)
	}
	if n := len(dates.recs["dune"]); n != 2 {
		t.Errorf("dune recordings dated = %d, want 2", n)
	}
}

// A path listed before any COMMIT line has no date to take, and must not take
// the next one: git only ever emits paths after their commit, so this is a
// malformed log, not a record to guess at.
func TestParseAddedLogIgnoresPathsBeforeACommit(t *testing.T) {
	dates := parseAddedLog([]byte("data/works/du/dune/work.json\nCOMMIT\t2026-01-05T10:00:00Z\n"))
	if len(dates.works) != 0 {
		t.Errorf("dated %v from a log with no commit first", dates.works)
	}
}

// TestBackfillSplicesTheDatesIn runs the whole conversion with a pre-resolved
// date set and checks where the values land: on the work, on each recording,
// and nowhere else.
func TestBackfillSplicesTheDatesIn(t *testing.T) {
	dir := seedLegacy(t, legacyFixture())
	tree, err := scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	dates := parseAddedLog([]byte(sampleLog))

	var sum Summary
	raw, dated, err := workEntry(tree.works["dune"], dates)
	if err != nil {
		t.Fatal(err)
	}
	sum.DatedWorks, sum.DatedRecordings = dated.works, dated.recs
	if sum.DatedWorks != 1 || sum.DatedRecordings != 2 {
		t.Errorf("dated %d works and %d recordings, want 1 and 2", sum.DatedWorks, sum.DatedRecordings)
	}

	entry := decodeRaw(t, raw)
	if entry["added_at"] != "2026-01-05T10:00:00+01:00" {
		t.Errorf("work added_at = %v", entry["added_at"])
	}
	recs, _ := entry["recordings"].(map[string]any)
	first, _ := recs["ann-doe-2021"].(map[string]any)
	second, _ := recs["bob-roe-2020"].(map[string]any)
	if first["added_at"] != "2026-01-05T10:00:00+01:00" || second["added_at"] != "2026-02-06T09:30:00Z" {
		t.Errorf("recording added_at values = %v / %v", first["added_at"], second["added_at"])
	}

	// A work the walk never dated keeps whatever it had, which here is nothing.
	emma, _, err := workEntry(tree.works["emma"], newAddedDates())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := decodeRaw(t, emma)["added_at"]; ok {
		t.Error("an undated work was given an added_at")
	}
}

// The git walk is the AUTHORITY for the one-time backfill: a record that already
// carries an added_at (an importer stamp) takes the history's value, because
// that is the value the retired release.yml step fed into the artifact.
func TestBackfillOverwritesAnExistingAddedAt(t *testing.T) {
	files := legacyFixture()
	files["works/du/dune/work.json"] = `{"added_at":"2026-06-06","authors":["ann-doe"],"id":"dune","language":"en",` +
		`"license":"CC0-1.0","sources":[{"type":"user"}],"title":"Dune"}`
	dir := seedLegacy(t, files)
	tree, err := scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := workEntry(tree.works["dune"], parseAddedLog([]byte(sampleLog)))
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeRaw(t, raw)["added_at"]; got != "2026-01-05T10:00:00+01:00" {
		t.Errorf("added_at = %v, want the git-history value", got)
	}
}

// A backfill that finds no history at all is a failed run, not a silent
// conversion with every added_at missing: the artifact would lose every date.
func TestBackfillRefusesAHistorylessTree(t *testing.T) {
	dir := seedLegacy(t, legacyFixture())
	_, err := Run(Options{DataDir: dir, Backfill: true, RepoDir: t.TempDir()})
	if err == nil {
		t.Fatal("a backfill with no history converted anyway")
	}
	if len(treeSnapshot(t, dir)) != len(legacyFixture()) {
		t.Error("the failed run changed the tree")
	}
}

func decodeRaw(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	m, err := pack.DecodeEntry(raw)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
