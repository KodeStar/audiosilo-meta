package importer

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/check"
)

// lvRegenRecJSON is recording rec of work, narrated by bea-reader, holding
// LibriVox project id: a recording a LibriVox tranche wrote.
func lvRegenRecJSON(work, rec string, id int) string {
	return fmt.Sprintf(`{"id":%q,"language":"en","librivox_id":%d,"license":"CC0-1.0","narrators":["bea-reader"],"sources":[{"type":"librivox","ref":"%d"}],"work":%q}`,
		rec, id, id, work)
}

// lvRegenProject is snapshot project id stating the named LibriVox genres.
func lvRegenProject(t *testing.T, id int, genres ...string) LibrivoxProject {
	t.Helper()
	p := retitled(t, id, "Anything")
	p.Genres = nil
	for _, g := range genres {
		p.Genres = append(p.Genres, LibrivoxGenre{Name: g})
	}
	return p
}

func runLibrivoxRegen(t *testing.T, dataDir string, projects ...LibrivoxProject) Summary {
	t.Helper()
	return runLibrivoxWith(t, writeSnapshot(t, projects...), dataDir, allLibrivox,
		Options{Mode: ModeRegenerateGenres, RowsAsOf: "2026-10-06"})
}

// TestRegenerateGenresFromALibrivoxSnapshot is how a LibriVox table change
// reaches the tranches already written (issue #2761): a work whose every
// recording is a LibriVox project takes the vote under TODAY's table - its
// Comedy a play (drama-plays, no longer comedy-humor), its children's Animals &
// Nature childrens (no longer nature) - while a work that also holds a libex
// recording has seen only part of its evidence and is only added to, and a
// project the catalogue does not hold matches nothing.
func TestRegenerateGenresFromALibrivoxSnapshot(t *testing.T) {
	dataDir := seedRegen(t, map[string]string{
		// What the old table wrote for projects 99801 and 99802.
		"works/pl/play/work.json":                regenWorkJSON("play", []string{"childrens", "comedy-humor", "nature"}, "librivox"),
		"works/pl/play/recordings/a.json":        lvRegenRecJSON("play", "a", 99801),
		"works/pl/play/recordings/b.json":        lvRegenRecJSON("play", "b", 99802),
		"works/mi/mixed/work.json":               regenWorkJSON("mixed", []string{"comedy-humor", "romance"}, "libex-import"),
		"works/mi/mixed/recordings/retail.json":  regenRecJSON("mixed", "retail", "B0MIXED001"),
		"works/mi/mixed/recordings/project.json": lvRegenRecJSON("mixed", "project", 99803),
	})
	projects := []LibrivoxProject{
		lvRegenProject(t, 99801, "Comedy", "Published 1800 -1900"),
		lvRegenProject(t, 99802, "Animals & Nature", "Children's Fiction"),
		lvRegenProject(t, 99803, "Comedy"),
		lvRegenProject(t, 99804, "Exploration"), // not catalogued
	}
	sum := runLibrivoxRegen(t, dataDir, projects...)
	if got, want := regenGenres(t, dataDir, "play"), []string{"childrens", "drama-plays"}; !reflect.DeepEqual(got, want) {
		t.Errorf("play genres = %v, want the new table's vote %v", got, want)
	}
	if got, want := regenGenres(t, dataDir, "mixed"), []string{"comedy-humor", "drama-plays", "romance"}; !reflect.DeepEqual(got, want) {
		t.Errorf("mixed genres = %v, want only added to %v", got, want)
	}
	want := []GenreChange{
		{Work: "mixed", Removed: []string{}, Added: []string{"drama-plays"}, Mode: GenreChangeAddOnly},
		{Work: "play", Removed: []string{"comedy-humor", "nature"}, Added: []string{"drama-plays"}, Mode: GenreChangeTrim},
	}
	if !reflect.DeepEqual(sum.GenreChanges, want) {
		t.Errorf("changes = %+v, want %+v", sum.GenreChanges, want)
	}
	if sum.Matched != 3 || sum.NotInCatalog != 1 {
		t.Errorf("matched/not in catalogue = %d/%d, want 3/1", sum.Matched, sum.NotInCatalog)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("tree failed validation: %v", res.Problems)
	}
	// Idempotent: the same snapshot again changes nothing.
	again := runLibrivoxRegen(t, dataDir, projects...)
	if len(again.GenreChanges) != 0 {
		t.Errorf("second run changed something: %+v", again.GenreChanges)
	}
}

// TestRegenerateGenresLibrivoxProjectMakesALibexRunIncomplete is the other
// direction of the completeness rule: a libex regeneration over a work that
// also holds a LibriVox project has no row for that recording, so it may only
// add - trimming to the libex vote would drop what only the project stated.
func TestRegenerateGenresLibrivoxProjectMakesALibexRunIncomplete(t *testing.T) {
	dataDir := seedRegen(t, map[string]string{
		"works/mi/mixed/work.json":               regenWorkJSON("mixed", []string{"drama-plays", "romance"}, "libex-import"),
		"works/mi/mixed/recordings/retail.json":  regenRecJSON("mixed", "retail", "B0MIXED001"),
		"works/mi/mixed/recordings/project.json": lvRegenRecJSON("mixed", "project", 99803),
	})
	sum := runRegen(t, dataDir, regenRow("B0MIXED001", "Romance", "Classics"))
	if got, want := regenGenres(t, dataDir, "mixed"), []string{"classics", "drama-plays", "romance"}; !reflect.DeepEqual(got, want) {
		t.Errorf("genres = %v, want the stored set only added to %v", got, want)
	}
	if len(sum.GenreChanges) != 1 || sum.GenreChanges[0].Mode != GenreChangeAddOnly {
		t.Errorf("changes = %+v, want one add-only", sum.GenreChanges)
	}
}

// TestRunLibrivoxRefusesTheLibexOnlyModes: the LibriVox source creates or
// regenerates genres; enrichment, recordings-only and relocation are libex's.
func TestRunLibrivoxRefusesTheLibexOnlyModes(t *testing.T) {
	for _, m := range []Mode{ModeEnrich, ModeRecordingsOnly, ModeRelocate} {
		if _, err := RunLibrivox(librivoxFixture, allLibrivox, Options{DataDir: t.TempDir(), Mode: m}); err == nil {
			t.Errorf("mode %s: RunLibrivox accepted it", m.runName())
		}
	}
}
