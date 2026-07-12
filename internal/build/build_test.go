package build

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/model"
	_ "modernc.org/sqlite"
)

func fixtureCatalog() *model.Catalog {
	author := &model.Person{ID: "andy-weir", Name: "Andy Weir", License: "CC0-1.0"}
	n1 := &model.Person{ID: "michael-kramer", Name: "Michael Kramer", License: "CC0-1.0"}
	n2 := &model.Person{ID: "kate-reading", Name: "Kate Reading", License: "CC0-1.0"}
	porter := &model.Person{ID: "ray-porter", Name: "Ray Porter", License: "CC0-1.0"}

	phm := &model.Work{
		ID: "project-hail-mary", Title: "Project Hail Mary", Language: "en",
		Authors: []string{"andy-weir"}, License: "CC0-1.0",
		Recordings: []*model.Recording{{
			ID: "ray-porter-2021", Work: "project-hail-mary", Abridged: false, Language: "en",
			RuntimeMin: 970, Publisher: "Audible Studios", License: "CC0-1.0",
			Narrators: []string{"ray-porter"},
			ASIN:      []model.ASIN{{Region: "us", ASIN: "B08G9PRS1K"}},
			Chapters: []model.Chapter{
				{Title: "Opening Credits", StartMS: 0, LengthMS: 5000},
				{Title: "Chapter 1", StartMS: 5000, LengthMS: 600000},
				{Title: "Chapter 2", StartMS: 605000, LengthMS: 600000},
			},
		}},
	}
	wok := &model.Work{
		ID: "the-way-of-kings", Title: "The Way of Kings", Language: "en",
		Authors: []string{"brandon-sanderson"}, License: "CC0-1.0",
		Recordings: []*model.Recording{{
			ID: "kramer-reading-2010", Work: "the-way-of-kings", Abridged: false, Language: "en",
			Narrators: []string{"michael-kramer", "kate-reading"}, License: "CC0-1.0",
			ASIN: []model.ASIN{{Region: "us", ASIN: "B003ZWFO7E"}},
			ISBN: []string{"9781427209269"},
		}},
	}
	sanderson := &model.Person{ID: "brandon-sanderson", Name: "Brandon Sanderson", License: "CC0-1.0"}

	series := &model.Series{
		ID: "the-stormlight-archive", Name: "The Stormlight Archive", License: "CC0-1.0",
		Authors: []string{"brandon-sanderson"},
		Works:   []model.SeriesWork{{Work: "the-way-of-kings", Position: "1"}},
	}

	chars := &model.Characters{
		Work: "project-hail-mary", License: "CC-BY-SA-3.0",
		Sources: []model.Source{{Type: "community"}},
		Characters: []model.Character{
			{
				ID: "ryland-grace", Name: "Ryland Grace", Role: "protagonist",
				Aliases: []string{"Dr. Grace"}, Reveal: model.Position{Chapter: 1},
				Description: "A junior-high science teacher who wakes alone aboard an interstellar ship with amnesia.",
				Xref:        &model.CharacterXref{Wikidata: "Q110001"},
			},
			{
				ID: "rocky", Name: "Rocky", Role: "supporting",
				Reveal:      model.Position{Chapter: 8},
				Description: "An Eridian engineer Grace meets far from home.",
			},
		},
	}
	// Deliberately out of position order to prove the build sorts recaps.
	recaps := &model.Recaps{
		Work: "project-hail-mary", License: "CC-BY-SA-3.0",
		Sources: []model.Source{{Type: "community"}},
		Recaps: []model.Recap{
			{Through: model.Position{Chapter: 9}, Scope: "book", Text: "First contact is made."},
			{Through: model.Position{Chapter: 2}, Scope: "book", Text: "Grace wakes with amnesia and takes stock of the ship."},
		},
	}

	return &model.Catalog{
		Works:      []*model.Work{phm, wok},
		People:     []*model.Person{author, n1, n2, porter, sanderson},
		Series:     []*model.Series{series},
		Characters: []*model.Characters{chars},
		Recaps:     []*model.Recaps{recaps},
	}
}

func buildFixture(t *testing.T) *sql.DB {
	t.Helper()
	out := filepath.Join(t.TempDir(), "meta.sqlite")
	added := map[string]string{"project-hail-mary": "2026-07-10T00:00:00Z"}
	if err := Build(fixtureCatalog(), out, time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC), added); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", out)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestBuildMeta(t *testing.T) {
	db := buildFixture(t)
	want := map[string]string{
		"schema_version":   "2",
		"built_at":         "2026-07-11T00:00:00Z",
		"count_works":      "2",
		"count_recordings": "2",
		"count_people":     "5",
		"count_series":     "1",
		"count_characters": "2",
		"count_recaps":     "2",
	}
	for k, w := range want {
		var got string
		if err := db.QueryRow(`SELECT value FROM meta WHERE key=?`, k).Scan(&got); err != nil {
			t.Fatalf("meta[%s]: %v", k, err)
		}
		if got != w {
			t.Errorf("meta[%s] = %q, want %q", k, got, w)
		}
	}
}

func TestBuildASINLookup(t *testing.T) {
	db := buildFixture(t)
	var workID, recID, region string
	err := db.QueryRow(`SELECT work_id, recording_id, region FROM recording_asins WHERE asin=?`, "B08G9PRS1K").
		Scan(&workID, &recID, &region)
	if err != nil {
		t.Fatal(err)
	}
	if workID != "project-hail-mary" || recID != "ray-porter-2021" || region != "us" {
		t.Errorf("ASIN lookup = %s/%s/%s", region, workID, recID)
	}

	// The (region, asin) uniqueness constraint must exist.
	if _, err := db.Exec(`INSERT INTO recording_asins(region, asin, work_id, recording_id) VALUES('us','B08G9PRS1K','x','y')`); err == nil {
		t.Errorf("expected UNIQUE(region, asin) to reject a duplicate")
	}
}

func TestBuildISBNLookup(t *testing.T) {
	db := buildFixture(t)
	var workID string
	if err := db.QueryRow(`SELECT work_id FROM recording_isbns WHERE isbn=?`, "9781427209269").Scan(&workID); err != nil {
		t.Fatal(err)
	}
	if workID != "the-way-of-kings" {
		t.Errorf("ISBN lookup work = %q", workID)
	}
}

func TestBuildFTS(t *testing.T) {
	cases := []struct {
		query    string
		wantKind string
		wantID   string
	}{
		{"hail mary", "work", "project-hail-mary"},                 // title
		{"weir", "work", "project-hail-mary"},                      // author name
		{"kate reading", "work", "the-way-of-kings"},               // narrator name
		{"stormlight", "work", "the-way-of-kings"},                 // series name on the work row
		{"sanderson", "person", "brandon-sanderson"},               // person row
		{"stormlight archive", "series", "the-stormlight-archive"}, // series row
	}
	db := buildFixture(t)
	for _, c := range cases {
		rows, err := db.Query(`SELECT kind, id FROM search_fts WHERE search_fts MATCH ?`, c.query)
		if err != nil {
			t.Fatalf("MATCH %q: %v", c.query, err)
		}
		found := false
		for rows.Next() {
			var kind, id string
			if err := rows.Scan(&kind, &id); err != nil {
				t.Fatal(err)
			}
			if kind == c.wantKind && id == c.wantID {
				found = true
			}
		}
		_ = rows.Close()
		if !found {
			t.Errorf("FTS %q did not return %s/%s", c.query, c.wantKind, c.wantID)
		}
	}
}

func TestBuildAddedAt(t *testing.T) {
	// project-hail-mary is in the added map (file-derived date wins); the-way-of
	// -kings is absent and has no sources[].imported_at, so it stays NULL.
	db := buildFixture(t)
	var phm sql.NullString
	if err := db.QueryRow(`SELECT added_at FROM works WHERE id=?`, "project-hail-mary").Scan(&phm); err != nil {
		t.Fatal(err)
	}
	if !phm.Valid || phm.String != "2026-07-10T00:00:00Z" {
		t.Errorf("phm added_at = %v, want file date", phm)
	}
	var wok sql.NullString
	if err := db.QueryRow(`SELECT added_at FROM works WHERE id=?`, "the-way-of-kings").Scan(&wok); err != nil {
		t.Fatal(err)
	}
	if wok.Valid {
		t.Errorf("way-of-kings added_at = %q, want NULL", wok.String)
	}
}

func TestBuildAddedAtFallback(t *testing.T) {
	// With no file-derived map, a work falls back to the newest imported_at.
	out := filepath.Join(t.TempDir(), "meta.sqlite")
	cat := &model.Catalog{
		People: []*model.Person{{ID: "andy-weir", Name: "Andy Weir", License: "CC0-1.0"}},
		Works: []*model.Work{{
			ID: "project-hail-mary", Title: "Project Hail Mary", Language: "en",
			Authors: []string{"andy-weir"}, License: "CC0-1.0",
			Sources: []model.Source{
				{Type: "openaudible-import", ImportedAt: "2026-01-01"},
				{Type: "openaudible-import", ImportedAt: "2026-06-15"},
			},
		}},
	}
	if err := Build(cat, out, time.Time{}, nil); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", out)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var got string
	if err := db.QueryRow(`SELECT added_at FROM works WHERE id=?`, "project-hail-mary").Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "2026-06-15" {
		t.Errorf("added_at fallback = %q, want newest imported_at 2026-06-15", got)
	}
}

func TestBuildChaptersOrdered(t *testing.T) {
	db := buildFixture(t)
	rows, err := db.Query(`SELECT idx, title, start_ms FROM chapters WHERE work_id=? AND recording_id=? ORDER BY idx`,
		"project-hail-mary", "ray-porter-2021")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var idxs []int
	var lastStart int64 = -1
	for rows.Next() {
		var idx int
		var title string
		var start int64
		if err := rows.Scan(&idx, &title, &start); err != nil {
			t.Fatal(err)
		}
		idxs = append(idxs, idx)
		if start <= lastStart {
			t.Errorf("chapter %d start %d not increasing (prev %d)", idx, start, lastStart)
		}
		lastStart = start
	}
	if len(idxs) != 3 || idxs[0] != 0 || idxs[2] != 2 {
		t.Errorf("chapter idx sequence = %v, want [0 1 2]", idxs)
	}
}

func TestBuildCharacters(t *testing.T) {
	db := buildFixture(t)
	rows, err := db.Query(`SELECT id, name, role, reveal_chapter, wikidata, ord, license FROM characters WHERE work_id=? ORDER BY ord`, "project-hail-mary")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	type row struct {
		id, name, role, wiki, license string
		reveal, ord                   int
	}
	var got []row
	for rows.Next() {
		var r row
		var role, wiki sql.NullString
		if err := rows.Scan(&r.id, &r.name, &role, &r.reveal, &wiki, &r.ord, &r.license); err != nil {
			t.Fatal(err)
		}
		r.role, r.wiki = role.String, wiki.String
		got = append(got, r)
	}
	if len(got) != 2 {
		t.Fatalf("got %d characters, want 2", len(got))
	}
	if got[0].id != "ryland-grace" || got[0].ord != 0 || got[0].reveal != 1 || got[0].role != "protagonist" ||
		got[0].wiki != "Q110001" || got[0].license != "CC-BY-SA-3.0" {
		t.Errorf("character[0] = %+v", got[0])
	}
	if got[1].id != "rocky" || got[1].ord != 1 || got[1].reveal != 8 {
		t.Errorf("character[1] = %+v", got[1])
	}

	// Aliases land in authored order and belong to the right character.
	var alias string
	if err := db.QueryRow(`SELECT alias FROM character_aliases WHERE work_id=? AND character_id=? ORDER BY ord`,
		"project-hail-mary", "ryland-grace").Scan(&alias); err != nil {
		t.Fatal(err)
	}
	if alias != "Dr. Grace" {
		t.Errorf("alias = %q, want Dr. Grace", alias)
	}
	// The character with no aliases has none.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM character_aliases WHERE work_id=? AND character_id=?`,
		"project-hail-mary", "rocky").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("rocky alias count = %d, want 0", n)
	}
}

func TestBuildRecapsServedByPosition(t *testing.T) {
	db := buildFixture(t)
	// The fixture supplies chapters 9 then 2 (out of order); recaps are keyed and
	// read by through_chapter, so ORDER BY through_chapter yields position order.
	rows, err := db.Query(`SELECT through_chapter, scope, license FROM recaps WHERE work_id=? ORDER BY through_chapter`, "project-hail-mary")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var chapters []int
	for rows.Next() {
		var ch int
		var scope, license string
		if err := rows.Scan(&ch, &scope, &license); err != nil {
			t.Fatal(err)
		}
		if scope != "book" || license != "CC-BY-SA-3.0" {
			t.Errorf("recap through %d: scope=%q license=%q", ch, scope, license)
		}
		chapters = append(chapters, ch)
	}
	if len(chapters) != 2 || chapters[0] != 2 || chapters[1] != 9 {
		t.Errorf("recap chapters = %v, want [2 9]", chapters)
	}
}

func TestBuildNarratorOrder(t *testing.T) {
	db := buildFixture(t)
	rows, err := db.Query(`SELECT person_id FROM recording_narrators WHERE recording_id=? ORDER BY ord`, "kramer-reading-2010")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatal(err)
		}
		got = append(got, p)
	}
	if len(got) != 2 || got[0] != "michael-kramer" || got[1] != "kate-reading" {
		t.Errorf("narrator order = %v, want [michael-kramer kate-reading]", got)
	}
}
