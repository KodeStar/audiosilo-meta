package importer

import (
	"reflect"
	"strings"
	"testing"
)

func TestLibrivoxGenreMappings(t *testing.T) {
	// A sub-genre with an evident vocabulary entry maps onto it; a period or
	// form tag is dropped and named in the run's unmapped line.
	pr := retitled(t, 99700, "A Genre Sampler")
	pr.Genres = []LibrivoxGenre{
		{Name: "Sonnets"},
		{Name: "Essays"},
		{Name: "King James Version"},
		{Name: "Suspense, Espionage, Political & Thrillers"},
		{Name: "Action & Adventure"},
		{Name: "War & Military"},
		{Name: "Published 1800 -1900"},
		{Name: "Single author"},
		{Name: "Short works"},
	}
	dataDir := t.TempDir()
	sum := runLibrivox(t, writeSnapshot(t, pr), dataDir, allLibrivox)
	var w lvWork
	readEntity(t, dataDir, workAddr("a-genre-sampler"), &w)
	want := []string{"action-adventure", "essays", "military-history", "poetry", "religion-spirituality", "thriller-suspense"}
	if !reflect.DeepEqual(w.Genres, want) {
		t.Errorf("genres = %v, want %v", w.Genres, want)
	}
	var unmapped string
	for _, line := range sum.Warnings {
		if strings.Contains(line, "unmapped genre strings:") {
			unmapped = line
		}
	}
	for _, dropped := range []string{"Published 1800 -1900", "Single author", "Short works"} {
		if !strings.Contains(unmapped, dropped) {
			t.Errorf("%q is not reported unmapped: %q", dropped, unmapped)
		}
	}
	for _, mapped := range []string{"Sonnets", "King James Version", "War & Military"} {
		if strings.Contains(unmapped, mapped) {
			t.Errorf("%q is reported unmapped: %q", mapped, unmapped)
		}
	}
}

func TestLibrivoxGenreDroppedTagsStayUnmapped(t *testing.T) {
	// The period and form tags the table leaves out on purpose.
	for _, name := range []string{
		"Published before 1800", "Published 1800 -1900", "Published 1900 onward",
		"Single author", "Short works", "General Fiction", "Anthologies", "Modern (19th C)",
	} {
		if g, ok := librivoxGenres[strings.ToLower(name)]; ok {
			t.Errorf("%q maps to %q; a period or form tag is not a genre", name, g)
		}
	}
}
