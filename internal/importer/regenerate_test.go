package importer

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/check"
)

// regenWorkJSON is a work "<slug>" by Ada Mapmaker stating genres, with the
// given source types (each its own sources[] entry).
func regenWorkJSON(slug string, genres []string, sourceTypes ...string) string {
	var srcs []string
	for _, s := range sourceTypes {
		srcs = append(srcs, fmt.Sprintf(`{"type":%q}`, s))
	}
	g := ""
	if len(genres) > 0 {
		g = `"genres":["` + strings.Join(genres, `","`) + `"],`
	}
	return fmt.Sprintf(`{"authors":["ada-mapmaker"],%s"id":%q,"language":"en","license":"CC0-1.0","sources":[%s],"title":%q}`,
		g, slug, strings.Join(srcs, ","), slug)
}

// regenRecJSON is recording rec of work, narrated by bea-reader, carrying asins
// (in the us marketplace) - none for a recording that carries no ASIN.
func regenRecJSON(work, rec string, asins ...string) string {
	var as []string
	for _, a := range asins {
		as = append(as, fmt.Sprintf(`{"asin":%q,"region":"us"}`, a))
	}
	asin := ""
	if len(as) > 0 {
		asin = `"asin":[` + strings.Join(as, ",") + `],`
	}
	return fmt.Sprintf(`{%s"id":%q,"language":"en","license":"CC0-1.0","narrators":["bea-reader"],"sources":[{"type":"libex-import"}],"work":%q}`,
		asin, rec, work)
}

// regenRow is a libex row for asin stating the named genre claims.
func regenRow(asin string, genres ...string) string {
	var gs []string
	for _, g := range genres {
		id, name, ok := strings.Cut(g, "|")
		if ok {
			gs = append(gs, fmt.Sprintf(`{"asin":%q,"name":%q}`, id, name))
		} else {
			gs = append(gs, fmt.Sprintf(`{"name":%q}`, g))
		}
	}
	return fmt.Sprintf(`{"asin":%q,"title":"Anything","region":"us","language":"english","authors":[{"name":"Ada Mapmaker"}],`+
		`"narrators":[{"name":"Bea Reader"}],"genres":[%s]}`, asin, strings.Join(gs, ","))
}

func seedRegen(t *testing.T, files map[string]string) string {
	t.Helper()
	dataDir := t.TempDir()
	files["people/ad/ada-mapmaker.json"] = `{"id":"ada-mapmaker","license":"CC0-1.0","name":"Ada Mapmaker","sources":[{"type":"libex-import"}]}`
	files["people/be/bea-reader.json"] = `{"id":"bea-reader","license":"CC0-1.0","name":"Bea Reader","sources":[{"type":"libex-import"}]}`
	seedTree(t, dataDir, files)
	return dataDir
}

func regenGenres(t *testing.T, dataDir, slug string) []string {
	t.Helper()
	var w enrichedWork
	readEntity(t, dataDir, "works/"+slug[:2]+"/"+slug+"/work.json", &w)
	return w.Genres
}

func runRegen(t *testing.T, dataDir string, rows ...string) Summary {
	t.Helper()
	return runLibexWith(t, dataDir, Options{Mode: ModeRegenerateGenres}, rows...)
}

// TestRegenerateGenresTrimsAMirrorSet is the Marvelous Land of Oz shape: three
// recordings of a libex-only work, one of which (one publisher's tagging)
// states genres no other recording does. The work's set becomes the vote -
// trimmed of the outlier and given the genre the stored set had lost - with no
// source appended and every other byte of the entry untouched.
func TestRegenerateGenresTrimsAMirrorSet(t *testing.T) {
	dataDir := seedRegen(t, map[string]string{
		"works/oz/oz/work.json":            regenWorkJSON("oz", []string{"childrens", "classics", "education", "westerns"}, "libex-import"),
		"works/oz/oz/recordings/a.json":    regenRecJSON("oz", "a", "B0OZ000001", "B0OZ000002"),
		"works/oz/oz/recordings/b.json":    regenRecJSON("oz", "b", "B0OZ000003"),
		"works/oz/oz/recordings/c.json":    regenRecJSON("oz", "c", "B0OZ000004"),
		"works/oz/oz/recordings/d.json":    regenRecJSON("oz", "d"), // no ASIN: owes no row
		"works/ot/other/work.json":         regenWorkJSON("other", []string{"romance"}, "libex-import"),
		"works/ot/other/recordings/a.json": regenRecJSON("other", "a", "B0OTHER001"),
	})
	sum := runRegen(t, dataDir,
		// Recording a states Westerns and Education in BOTH its marketplaces: one vote.
		regenRow("B0OZ000001", "Classics", "Fantasy", "Westerns", "Education & Learning"),
		regenRow("B0OZ000002", "Westerns", "Education & Learning"),
		regenRow("B0OZ000003", "Classics", "Fantasy", "Children's Audiobooks"),
		regenRow("B0OZ000004", "Classics", "Children's Audiobooks"),
		regenRow("B0NOTHERE1", "Classics"), // not catalogued
	)
	if got, want := regenGenres(t, dataDir, "oz"), []string{"childrens", "classics", "fantasy"}; !reflect.DeepEqual(got, want) {
		t.Errorf("genres = %v, want the vote %v", got, want)
	}
	if sum.GenreWorksSet != 1 || sum.GenreWorksNoRow != 1 || sum.Matched != 4 || sum.NotInCatalog != 1 {
		t.Errorf("summary = %+v", sum)
	}
	if sum.GenresAdded != 1 || sum.GenresRemoved != 2 {
		t.Errorf("instances added/removed = %d/%d, want 1/2", sum.GenresAdded, sum.GenresRemoved)
	}
	want := []GenreChange{{Work: "oz", Removed: []string{"education", "westerns"}, Added: []string{"fantasy"}, Mode: GenreChangeTrim}}
	if !reflect.DeepEqual(sum.GenreChanges, want) {
		t.Errorf("changes = %+v, want %+v", sum.GenreChanges, want)
	}
	raw := readRaw(t, dataDir, "works/oz/oz/work.json")
	if want := `{"authors":["ada-mapmaker"],"genres":["childrens","classics","fantasy"],"id":"oz","language":"en","license":"CC0-1.0","sources":[{"type":"libex-import"}],"title":"oz"}`; raw != want {
		t.Errorf("work = %s\nwant only genres changed and no source stamped: %s", raw, want)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("tree failed validation: %v", res.Problems)
	}

	// Idempotent: the same rows again change nothing on disk.
	after := testpack.Snapshot(t, dataDir)
	again := runRegen(t, dataDir,
		regenRow("B0OZ000001", "Classics", "Fantasy", "Westerns", "Education & Learning"),
		regenRow("B0OZ000002", "Westerns", "Education & Learning"),
		regenRow("B0OZ000003", "Classics", "Fantasy", "Children's Audiobooks"),
		regenRow("B0OZ000004", "Classics", "Children's Audiobooks"),
	)
	if again.GenreWorksSet+again.GenreWorksAddedTo != 0 || len(again.GenreChanges) != 0 {
		t.Errorf("second run changed something: %+v", again)
	}
	if !reflect.DeepEqual(after, testpack.Snapshot(t, dataDir)) {
		t.Errorf("a second identical run rewrote the tree")
	}
}

// TestRegenerateGenresAddsOnlyWithoutTrimRights covers the two ways a work is
// only added to: a user-library source contributed to it (LICENSING.md rule 5),
// or a recording carrying an ASIN met no row (incomplete evidence). A reference
// tier source (community) does NOT block the trim.
func TestRegenerateGenresAddsOnlyWithoutTrimRights(t *testing.T) {
	dataDir := seedRegen(t, map[string]string{
		"works/us/user/work.json":            regenWorkJSON("user", []string{"westerns"}, "libex-import", "openaudible-import"),
		"works/us/user/recordings/a.json":    regenRecJSON("user", "a", "B0USER0001"),
		"works/pa/partial/work.json":         regenWorkJSON("partial", []string{"westerns"}, "libex-import"),
		"works/pa/partial/recordings/a.json": regenRecJSON("partial", "a", "B0PART0001"),
		"works/pa/partial/recordings/b.json": regenRecJSON("partial", "b", "B0PART0002"),
		"works/co/comm/work.json":            regenWorkJSON("comm", []string{"westerns"}, "libex-import", "community"),
		"works/co/comm/recordings/a.json":    regenRecJSON("comm", "a", "B0COMM0001"),
	})
	sum := runRegen(t, dataDir,
		regenRow("B0USER0001", "Mystery"),
		regenRow("B0PART0001", "Mystery"),
		regenRow("B0COMM0001", "Mystery"),
	)
	for slug, want := range map[string][]string{
		"user":    {"mystery", "westerns"},
		"partial": {"mystery", "westerns"},
		"comm":    {"mystery"},
	} {
		if got := regenGenres(t, dataDir, slug); !reflect.DeepEqual(got, want) {
			t.Errorf("%s genres = %v, want %v", slug, got, want)
		}
	}
	if sum.GenreWorksAddedTo != 2 || sum.GenreWorksSet != 1 || sum.GenresRemoved != 1 || sum.GenresAdded != 3 {
		t.Errorf("summary = %+v", sum)
	}
	if !hasNote(sum.Notes, "1 carry a user-library source, 1 have a recording with an ASIN no input row matched") {
		t.Errorf("notes = %v", sum.Notes)
	}
}

// TestRegenerateGenresLeavesASilentVoteAlone: a vote that states nothing - rows
// that map no genre, or three recordings agreeing on none - leaves even a
// trim-eligible work exactly as it is.
func TestRegenerateGenresLeavesASilentVoteAlone(t *testing.T) {
	dataDir := seedRegen(t, map[string]string{
		"works/qu/quiet/work.json":           regenWorkJSON("quiet", []string{"westerns"}, "libex-import"),
		"works/qu/quiet/recordings/a.json":   regenRecJSON("quiet", "a", "B0QUIET001"),
		"works/sp/split/work.json":           regenWorkJSON("split", []string{"westerns"}, "libex-import"),
		"works/sp/split/recordings/a.json":   regenRecJSON("split", "a", "B0SPLIT001"),
		"works/sp/split/recordings/b.json":   regenRecJSON("split", "b", "B0SPLIT002"),
		"works/sp/split/recordings/c.json":   regenRecJSON("split", "c", "B0SPLIT003"),
		"works/no/nogenre/work.json":         regenWorkJSON("nogenre", nil, "libex-import"),
		"works/no/nogenre/recordings/a.json": regenRecJSON("nogenre", "a", "B0NOGEN001"),
	})
	before := testpack.Snapshot(t, dataDir)
	sum := runRegen(t, dataDir,
		regenRow("B0QUIET001", "Literature & Fiction"),
		regenRow("B0SPLIT001", "Mystery"),
		regenRow("B0SPLIT002", "Fantasy"),
		regenRow("B0SPLIT003", "Romance"),
		regenRow("B0NOGEN001"),
	)
	if !reflect.DeepEqual(before, testpack.Snapshot(t, dataDir)) {
		t.Errorf("a silent vote changed the tree")
	}
	if sum.GenreWorksUnchanged != 3 || len(sum.GenreChanges) != 0 {
		t.Errorf("summary = %+v", sum)
	}
	if !hasNote(sum.Notes, "3 trim-eligible works kept the recorded set") {
		t.Errorf("notes = %v", sum.Notes)
	}
}

// TestRegenerateGenresAppliesTheFormatRule: the regeneration maps rows under
// today's table, so a work whose arts-entertainment came only from a radio
// dramatization's format ladder loses it.
func TestRegenerateGenresAppliesTheFormatRule(t *testing.T) {
	dataDir := seedRegen(t, map[string]string{
		"works/pi/pigs/work.json":         regenWorkJSON("pigs", []string{"arts-entertainment", "mystery"}, "libex-import"),
		"works/pi/pigs/recordings/a.json": regenRecJSON("pigs", "a", "B0PIGS0001"),
	})
	runRegen(t, dataDir, regenRow("B0PIGS0001",
		"18571910011|Arts & Entertainment", "18571919011|Audio Performances & Dramatizations",
		"18571920011|Dramatizations", "18574606011|Mystery"))
	if got, want := regenGenres(t, dataDir, "pigs"), []string{"mystery"}; !reflect.DeepEqual(got, want) {
		t.Errorf("genres = %v, want %v", got, want)
	}
}

// TestRegenerateGenresRequiresMirrorRows: the mode trims to what the MIRROR's
// rows vote, so it refuses any other source's rows.
func TestRegenerateGenresRequiresMirrorRows(t *testing.T) {
	_, err := Run(writeBooks(t, `[]`), Options{DataDir: t.TempDir(), ImportDate: testImportDate, Mode: ModeRegenerateGenres})
	if err == nil {
		t.Fatal("a user-library source ran the genre regeneration")
	}
}

func hasNote(notes []string, sub string) bool {
	for _, n := range notes {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}
