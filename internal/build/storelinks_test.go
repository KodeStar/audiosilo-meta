package build

import (
	"database/sql"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// TestBuildStoreLinks: a recording's store pages land in their schema_version 11
// columns, and a recording stating none (the LibriVox one included) has NULLs.
func TestBuildStoreLinks(t *testing.T) {
	cat := librivoxCatalog()
	cat.Works[0].Recordings = append(cat.Works[0].Recordings, &model.Recording{
		ID: "full-cast", Work: "metamorphosis", Narrators: []string{"someone-else"}, Language: "en", License: "CC0-1.0",
		Links: &model.RecordingLinks{
			AudiobookGuild: "https://audiobookguild.com/products/metamorphosis",
			GraphicAudio:   "https://www.graphicaudio.net/metamorphosis.html",
		},
		Sources: []model.Source{{Type: model.SourceStoreListing, Ref: "https://audiobookguild.com/products/metamorphosis", ImportedAt: "2026-11-06"}},
	})
	db := buildDB(t, cat)

	read := func(id string) (guild, ga, lvLink sql.NullString) {
		t.Helper()
		if err := db.QueryRow(`SELECT link_audiobookguild, link_graphicaudio, link_librivox FROM recordings WHERE work_id='metamorphosis' AND id=?`, id).
			Scan(&guild, &ga, &lvLink); err != nil {
			t.Fatal(err)
		}
		return guild, ga, lvLink
	}
	guild, ga, lvLink := read("full-cast")
	if guild.String != "https://audiobookguild.com/products/metamorphosis" || ga.String != "https://www.graphicaudio.net/metamorphosis.html" || lvLink.Valid {
		t.Errorf("store link columns = %v %v (librivox %v)", guild, ga, lvLink)
	}
	for _, id := range []string{"david-barnes", "someone-else"} {
		if guild, ga, _ := read(id); guild.Valid || ga.Valid {
			t.Errorf("%s: a recording stating no store link has columns %v %v", id, guild, ga)
		}
	}
	var types []string
	rows, err := db.Query(`SELECT type FROM recording_sources WHERE recording_id='full-cast'`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var tp string
		if err := rows.Scan(&tp); err != nil {
			t.Fatal(err)
		}
		types = append(types, tp)
	}
	if len(types) != 1 || types[0] != model.SourceStoreListing {
		t.Errorf("recording_sources types = %v, want [store-listing] (the ?source= filter reads it)", types)
	}
}
