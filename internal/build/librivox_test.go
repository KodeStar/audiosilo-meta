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
