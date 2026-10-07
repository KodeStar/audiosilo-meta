package importer

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
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
	return runLibexWith(t, dataDir, Options{Mode: ModeRegenerateGenres, RowsAsOf: "2026-10-06"}, rows...)
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
	set, _, added, removed := sum.GenreTally()
	if set != 1 || sum.GenreWorksNoRow != 1 || sum.Matched != 4 || sum.NotInCatalog != 1 {
		t.Errorf("summary = %+v", sum)
	}
	if added != 1 || removed != 2 {
		t.Errorf("instances added/removed = %d/%d, want 1/2", added, removed)
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
	if len(again.GenreChanges) != 0 {
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
	if set, addedTo, added, removed := sum.GenreTally(); addedTo != 2 || set != 1 || removed != 1 || added != 3 {
		t.Errorf("summary = %+v", sum)
	}
	if !hasNote(sum.Notes, "1 carry a user-library source, 1 have a recording carrying an ASIN that no uncontradicted input row covered") {
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

// regenRowRuntime is regenRow stating a runtime, in minutes.
func regenRowRuntime(asin string, minutes int, genres ...string) string {
	return strings.Replace(regenRow(asin, genres...), `"region":"us",`, fmt.Sprintf(`"region":"us","lengthMinutes":%d,`, minutes), 1)
}

// regenRecRuntime is regenRecJSON recorded at runtime minutes.
func regenRecRuntime(work, rec string, minutes int, asins ...string) string {
	return strings.Replace(regenRecJSON(work, rec, asins...), `"license"`, fmt.Sprintf(`"runtime_min":%d,"license"`, minutes), 1)
}

// TestRegenerateGenresIgnoresContradictedRows: a row its recording contradicts
// on the RUNTIME (here a sixth of the recorded one - another production, an ASIN
// attached to the wrong recording) casts no vote, through the ASIN-merge scope
// of the one contradiction test, which never reads a release date (the
// regeneration hands it none: a regional re-release's date differs by right),
// so the "dated" row, whose date differs from its recording's, votes. Its stray genre is not
// voted in, and a recording ALL of whose rows were contradicted is not covered,
// so its work is never trimmed on the evidence that is left.
func TestRegenerateGenresIgnoresContradictedRows(t *testing.T) {
	dataDir := seedRegen(t, map[string]string{
		"works/st/stray/work.json":          regenWorkJSON("stray", nil, "libex-import"),
		"works/st/stray/recordings/a.json":  regenRecRuntime("stray", "a", 600, "B0STRAY0A1"),
		"works/st/stray/recordings/b.json":  regenRecRuntime("stray", "b", 600, "B0STRAY0B1", "B0STRAY0B2"),
		"works/co/contra/work.json":         regenWorkJSON("contra", []string{"mystery", "westerns"}, "libex-import"),
		"works/co/contra/recordings/a.json": regenRecRuntime("contra", "a", 600, "B0CONTRAA1"),
		"works/co/contra/recordings/b.json": regenRecRuntime("contra", "b", 600, "B0CONTRAB1"),
		"works/co/contra/recordings/c.json": regenRecRuntime("contra", "c", 600, "B0CONTRAC1"),
		"works/da/dated/work.json":          regenWorkJSON("dated", nil, "libex-import"),
		"works/da/dated/recordings/a.json": strings.Replace(regenRecRuntime("dated", "a", 600, "B0DATED0A1"),
			`"license"`, `"release_date":"2020-01-01","license"`, 1),
		"works/da/dated/recordings/b.json": regenRecRuntime("dated", "b", 600, "B0DATED0B1"),
	})
	sum := runRegen(t, dataDir,
		regenRowRuntime("B0STRAY0A1", 600, "Mystery"),
		regenRowRuntime("B0STRAY0B1", 600, "Mystery"),
		regenRowRuntime("B0STRAY0B2", 100, "Westerns"), // contradicted: casts no vote
		regenRowRuntime("B0CONTRAA1", 600, "Mystery"),
		regenRowRuntime("B0CONTRAB1", 600, "Mystery"),
		regenRowRuntime("B0CONTRAC1", 100, "Mystery", "Westerns"), // c's only row: c is not covered
		strings.Replace(regenRowRuntime("B0DATED0A1", 600, "Westerns"), `"region":"us",`, `"region":"us","releaseDate":"2021-05-05",`, 1),
		regenRowRuntime("B0DATED0B1", 600, "Mystery"),
	)
	if got, want := regenGenres(t, dataDir, "dated"), []string{"mystery", "westerns"}; !reflect.DeepEqual(got, want) {
		t.Errorf("dated genres = %v, want %v (a release date that differs is no contradiction)", got, want)
	}
	if got, want := regenGenres(t, dataDir, "stray"), []string{"mystery"}; !reflect.DeepEqual(got, want) {
		t.Errorf("stray genres = %v, want %v (the contradicted row's westerns not voted in)", got, want)
	}
	if got, want := regenGenres(t, dataDir, "contra"), []string{"mystery", "westerns"}; !reflect.DeepEqual(got, want) {
		t.Errorf("contra genres = %v, want %v (an uncovered recording leaves the work add-only)", got, want)
	}
	if sum.GenreRowsContradicted != 2 {
		t.Errorf("GenreRowsContradicted = %d, want 2", sum.GenreRowsContradicted)
	}
	if !hasNote(sum.Notes, "2 rows contradicted the recorded runtime") ||
		!hasNote(sum.Notes, "1 have a recording carrying an ASIN that no uncontradicted input row covered") {
		t.Errorf("notes = %v", sum.Notes)
	}
}

// TestRegenerateGenresSkipsWorksNewerThanTheRows: the regeneration never judges
// a record with evidence older than the record. A work whose newest provenance -
// its own added_at, or a recording's source imported_at - is after the rows'
// snapshot is left exactly as it is, counted and named; one dated before it is
// trimmed by the vote.
func TestRegenerateGenresSkipsWorksNewerThanTheRows(t *testing.T) {
	dated := func(slug, addedAt string) string {
		return strings.Replace(regenWorkJSON(slug, []string{"mystery", "westerns"}, "libex-import"),
			`"id"`, fmt.Sprintf(`"added_at":%q,"id"`, addedAt), 1)
	}
	files := map[string]string{
		"works/ea/early/work.json":    dated("early", "2026-07-01"),
		"works/la/late/work.json":     dated("late", "2026-08-15"),
		"works/re/rec-late/work.json": dated("rec-late", "2026-07-01"),
	}
	for _, slug := range []string{"early", "late", "rec-late"} {
		for _, rec := range []string{"a", "b", "c"} {
			asin := freshASIN(slug, rec)
			files["works/"+slug[:2]+"/"+slug+"/recordings/"+rec+".json"] = regenRecJSON(slug, rec, asin)
		}
	}
	files["works/re/rec-late/recordings/c.json"] = strings.Replace(files["works/re/rec-late/recordings/c.json"],
		`{"type":"libex-import"}`, `{"type":"libex-import","imported_at":"2026-09-01"}`, 1)
	dataDir := seedRegen(t, files)
	var rows []string
	for _, slug := range []string{"early", "late", "rec-late"} {
		for _, rec := range []string{"a", "b", "c"} {
			asin := freshASIN(slug, rec)
			genres := []string{"Mystery"}
			if rec == "a" {
				genres = append(genres, "Westerns")
			}
			rows = append(rows, regenRow(asin, genres...))
		}
	}
	late := readRaw(t, dataDir, "works/la/late/work.json")
	recLate := readRaw(t, dataDir, "works/re/rec-late/work.json")
	sum := runLibexWith(t, dataDir, Options{Mode: ModeRegenerateGenres, RowsAsOf: "2026-07-29"}, rows...)
	if got, want := regenGenres(t, dataDir, "early"), []string{"mystery"}; !reflect.DeepEqual(got, want) {
		t.Errorf("early genres = %v, want the vote %v", got, want)
	}
	if readRaw(t, dataDir, "works/la/late/work.json") != late || readRaw(t, dataDir, "works/re/rec-late/work.json") != recLate {
		t.Errorf("a work newer than the rows was judged")
	}
	if !hasNote(sum.Notes, "2 works carry provenance newer than the rows (--rows-as-of 2026-07-29) and were not judged (for example: late, rec-late)") {
		t.Errorf("notes = %v", sum.Notes)
	}
	if _, err := RunLibex(writeBooks(t, rows[0]+"\n"), Options{DataDir: dataDir, ImportDate: testImportDate, Mode: ModeRegenerateGenres}); err == nil {
		t.Errorf("a regeneration without RowsAsOf ran")
	}
}

func freshASIN(slug, rec string) string {
	return map[string]string{"early": "B0FEARLY", "late": "B0FLATE0", "rec-late": "B0FRECLT"}[slug] + strings.ToUpper(rec) + "1"
}

// TestNewestProvenanceDay pins the dating rule: the newest of the work's and its
// recordings' added_at and sources' imported_at, an RFC 3339 timestamp read in
// UTC (metabuild's own ordering, model.TimeKey) and cut to its day.
func TestNewestProvenanceDay(t *testing.T) {
	w := &model.Work{
		AddedAt: "2026-07-01",
		Sources: []model.Source{{Type: "libex-import", ImportedAt: "2026-07-02"}},
		Recordings: []*model.Recording{{
			AddedAt: "2026-07-29T23:30:00-02:00", // 2026-07-30 in UTC
			Sources: []model.Source{{Type: "libex-import", ImportedAt: "2026-07-03"}},
		}},
	}
	if got := newestProvenanceDay(w); got != "2026-07-30" {
		t.Errorf("newestProvenanceDay = %q, want 2026-07-30", got)
	}
	if got := newestProvenanceDay(&model.Work{}); got != "" {
		t.Errorf("an undated work = %q, want empty", got)
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
