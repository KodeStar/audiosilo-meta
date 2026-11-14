package build

import (
	"database/sql"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

func TestBuildAbridgementPresence(t *testing.T) {
	cat := fixtureCatalog()
	cat.Works[0].Recordings = []*model.Recording{
		{ID: "unknown", Language: "en", License: "CC0-1.0"},
		{ID: "full", Language: "en", AbridgedKnown: true, License: "CC0-1.0"},
		{ID: "short", Language: "en", Abridged: true, License: "CC0-1.0"},
	}
	db := buildDB(t, cat)
	for _, tt := range []struct {
		id, want string
		abridged bool
	}{{"unknown", "", false}, {"full", "unabridged", false}, {"short", "abridged", true}} {
		var presence sql.NullString
		var legacy bool
		if err := db.QueryRow(`SELECT abridged, abridgement FROM recordings WHERE work_id=? AND id=?`, cat.Works[0].ID, tt.id).Scan(&legacy, &presence); err != nil {
			t.Fatal(err)
		}
		if presence.String != tt.want || legacy != tt.abridged || presence.Valid != (tt.want != "") {
			t.Fatalf("%s: legacy=%v presence=%+v", tt.id, legacy, presence)
		}
	}
}
