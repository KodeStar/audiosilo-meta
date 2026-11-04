package build

import (
	"database/sql"
	"slices"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// librivoxCatalog is a two-recording catalogue: a LibriVox reading carrying every
// schema_version 8 fact, and a plain recording of the same work stating none.
func librivoxCatalog() *model.Catalog {
	return &model.Catalog{
		People: []*model.Person{
			{ID: "franz-kafka", Name: "Franz Kafka", LibrivoxAuthorID: 100, BirthYear: 1883, DeathYear: 1924, License: "CC0-1.0"},
			{ID: "david-barnes", Name: "David Barnes", LibrivoxReaderID: 94, License: "CC0-1.0"},
			{ID: "someone-else", Name: "Someone Else", License: "CC0-1.0"},
		},
		Works: []*model.Work{{
			ID: "metamorphosis", Title: "Metamorphosis", Authors: []string{"franz-kafka"}, Language: "en", License: "CC0-1.0",
			Recordings: []*model.Recording{
				{
					ID: "david-barnes", Work: "metamorphosis", Narrators: []string{"david-barnes"}, Language: "en", License: "CC0-1.0",
					LibrivoxID: 527, ArchiveID: "metamorphosis_librivox", CopyrightYear: 1915,
					Links: &model.RecordingLinks{
						Librivox: "https://librivox.org/the-metamorphosis-by-franz-kafka/",
						Zip:      "https://archive.org/compress/metamorphosis_librivox/x.zip",
						RSS:      "https://librivox.org/rss/527",
						Text:     "https://www.kafka.org/x",
					},
					Chapters: []model.Chapter{{Title: "I", StartMS: 0, LengthMS: 2992000, AudioURL: "https://www.archive.org/download/m/1.mp3"}},
					AddedAt:  "2026-10-27",
					// librivox twice (two imports) is one row.
					Sources: []model.Source{{Type: "librivox", Ref: "527"}, {Type: "user"}, {Type: "librivox", Ref: "527"}},
				},
				{
					ID: "someone-else", Work: "metamorphosis", Narrators: []string{"someone-else"}, Language: "en", License: "CC0-1.0",
					Sources: []model.Source{{Type: "libex-import", ImportedAt: "2026-09-01"}},
				},
			},
		}},
	}
}

func TestBuildLibrivoxLayer(t *testing.T) {
	db := buildDB(t, librivoxCatalog())

	var lvID, cy sql.NullInt64
	var archive, lvLink, zip, rss, text, added sql.NullString
	if err := db.QueryRow(`SELECT librivox_id, archive_id, link_librivox, link_zip, link_rss, link_text, copyright_year, added_at
		FROM recordings WHERE work_id='metamorphosis' AND id='david-barnes'`).
		Scan(&lvID, &archive, &lvLink, &zip, &rss, &text, &cy, &added); err != nil {
		t.Fatal(err)
	}
	if lvID.Int64 != 527 || archive.String != "metamorphosis_librivox" || cy.Int64 != 1915 || added.String != "2026-10-27" ||
		lvLink.String != "https://librivox.org/the-metamorphosis-by-franz-kafka/" || rss.String != "https://librivox.org/rss/527" ||
		zip.String != "https://archive.org/compress/metamorphosis_librivox/x.zip" || text.String != "https://www.kafka.org/x" {
		t.Errorf("LibriVox recording columns = %v %v %v %v %v %v %v %v", lvID, archive, lvLink, zip, rss, text, cy, added)
	}

	// The plain recording states none of them: NULL, not 0 or "". Its added_at
	// falls back to the newest imported_at, as a work's does.
	if err := db.QueryRow(`SELECT librivox_id, archive_id, link_text, copyright_year, added_at
		FROM recordings WHERE work_id='metamorphosis' AND id='someone-else'`).
		Scan(&lvID, &archive, &text, &cy, &added); err != nil {
		t.Fatal(err)
	}
	if lvID.Valid || archive.Valid || text.Valid || cy.Valid || added.String != "2026-09-01" {
		t.Errorf("plain recording columns = %v %v %v %v %v", lvID, archive, text, cy, added)
	}

	var audio sql.NullString
	if err := db.QueryRow(`SELECT audio_url FROM chapters WHERE recording_id='david-barnes'`).Scan(&audio); err != nil {
		t.Fatal(err)
	}
	if audio.String != "https://www.archive.org/download/m/1.mp3" {
		t.Errorf("chapter audio_url = %v", audio)
	}

	var author, birth, death, reader sql.NullInt64
	if err := db.QueryRow(`SELECT librivox_author_id, birth_year, death_year, librivox_reader_id FROM people WHERE id='franz-kafka'`).
		Scan(&author, &birth, &death, &reader); err != nil {
		t.Fatal(err)
	}
	if author.Int64 != 100 || birth.Int64 != 1883 || death.Int64 != 1924 || reader.Valid {
		t.Errorf("person columns = %v %v %v %v", author, birth, death, reader)
	}

	// Each row carries its recording's added_at (addedAtOf), schema_version 9.
	if got := triples(t, db, `SELECT recording_id, type, coalesce(added_at, 'NULL') FROM recording_sources ORDER BY recording_id, type`); !slices.Equal(got, [][3]string{
		{"david-barnes", "librivox", "2026-10-27"}, {"david-barnes", "user", "2026-10-27"}, {"someone-else", "libex-import", "2026-09-01"},
	}) {
		t.Errorf("recording_sources added_at = %v", got)
	}
	got := triples(t, db, `SELECT work_id, recording_id, type FROM recording_sources`)
	want := [][3]string{
		{"metamorphosis", "david-barnes", "librivox"},
		{"metamorphosis", "david-barnes", "user"},
		{"metamorphosis", "someone-else", "libex-import"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("recording_sources = %v, want %v", got, want)
	}
}

// TestBuildLibrivoxRecordingLayer: the schema_version 9 facts - the project's own
// title, its translators as recording credits, its genre names, and each
// section's number and readers - land in their columns and tables, in the
// record's order; a recording stating none adds no row and a NULL column.
func TestBuildLibrivoxRecordingLayer(t *testing.T) {
	cat := librivoxCatalog()
	cat.People = append(cat.People,
		&model.Person{ID: "ian-johnston", Name: "Ian Johnston", LibrivoxAuthorID: 1019, License: "CC0-1.0"},
		&model.Person{ID: "tom-yates", Name: "Tom Yates", LibrivoxReaderID: 454, License: "CC0-1.0"})
	rec := cat.Works[0].Recordings[0]
	rec.Narrators = []string{"david-barnes", "tom-yates"}
	rec.LibrivoxTitle = "Metamorphosis (version 2)"
	rec.LibrivoxGenres = []string{"General Fiction", "Classics (Greek & Latin Antiquity)"}
	rec.Credits = []model.Credit{{Person: "ian-johnston", Role: model.RoleTranslator}}
	zero := 0
	rec.Chapters = []model.Chapter{
		{Title: "0", StartMS: 0, LengthMS: 1000, SectionNumber: &zero, Readers: []string{"tom-yates", "david-barnes"}},
		{Title: "1", StartMS: 1000, LengthMS: 1000},
	}
	db := buildDB(t, cat)

	var title sql.NullString
	if err := db.QueryRow(`SELECT librivox_title FROM recordings WHERE id='david-barnes'`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title.String != "Metamorphosis (version 2)" {
		t.Errorf("librivox_title = %v", title)
	}
	if err := db.QueryRow(`SELECT librivox_title FROM recordings WHERE id='someone-else'`).Scan(&title); err != nil || title.Valid {
		t.Errorf("plain recording librivox_title = %v (%v), want NULL", title, err)
	}
	got := triples(t, db, `SELECT person_id, role, ord FROM recording_credits WHERE work_id='metamorphosis' AND recording_id='david-barnes' ORDER BY ord`)
	if want := [][3]string{{"ian-johnston", "translator", "0"}}; !slices.Equal(got, want) {
		t.Errorf("recording_credits = %v, want %v", got, want)
	}
	got = triples(t, db, `SELECT recording_id, genre, ord FROM recording_librivox_genres ORDER BY ord`)
	if want := [][3]string{{"david-barnes", "General Fiction", "0"}, {"david-barnes", "Classics (Greek & Latin Antiquity)", "1"}}; !slices.Equal(got, want) {
		t.Errorf("recording_librivox_genres = %v, want %v", got, want)
	}
	got = triples(t, db, `SELECT chapter_idx, person_id, ord FROM chapter_readers WHERE recording_id='david-barnes' ORDER BY chapter_idx, ord`)
	if want := [][3]string{{"0", "tom-yates", "0"}, {"0", "david-barnes", "1"}}; !slices.Equal(got, want) {
		t.Errorf("chapter_readers = %v, want %v", got, want)
	}
	got = triples(t, db, `SELECT idx, coalesce(section_number, 'NULL'), title FROM chapters WHERE recording_id='david-barnes' ORDER BY idx`)
	if want := [][3]string{{"0", "0", "0"}, {"1", "NULL", "1"}}; !slices.Equal(got, want) {
		t.Errorf("chapter section numbers = %v, want %v", got, want)
	}
}
