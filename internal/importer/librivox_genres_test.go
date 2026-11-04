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
		{Name: "Nautical & Marine Fiction"},
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
	for _, dropped := range []string{"Published 1800 -1900", "Single author", "Nautical & Marine Fiction"} {
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

// TestLibrivoxGenreTableDecisions pins issue #2761's decisions, one row per
// LibriVox name: the clear fits onto EXISTING vocabulary terms, the remaps of
// the mappings the review called guesses, and the mappings kept.
func TestLibrivoxGenreTableDecisions(t *testing.T) {
	for name, want := range map[string]string{
		// Added: non-fiction sub-genres with an Audible-table precedent, and
		// Exploration with its parent Travel & Geography.
		"Literary Criticism":    "writing-publishing",
		"Atheism & Agnosticism": "religion-spirituality",
		"Crafts & Hobbies":      "home-garden",
		"Exploration":           "travel",
		"Animals":               "nature",
		// Added: the names only LibriVox's children's branches carry.
		"Family":      "childrens",
		"School":      "childrens",
		"General":     "childrens",
		"Historical":  "childrens",
		"Short works": "childrens",
		// Remapped: a LibriVox Comedy is a play, and Animals & Nature is
		// children's fiction (nature is a non-fiction entry).
		"Comedy":           "drama-plays",
		"Animals & Nature": "childrens",
		// Kept.
		"Drama":       "drama-plays",
		"Tragedy":     "drama-plays",
		"Mathematics": "science",
	} {
		if got := librivoxGenres[strings.ToLower(name)]; got != want {
			t.Errorf("%q maps to %q, want %q", name, got, want)
		}
	}
}

func TestLibrivoxGenreDroppedTagsStayUnmapped(t *testing.T) {
	// The period and form tags the table leaves out on purpose, and the genres
	// whose nearest vocabulary entry would be a guess (issue #2761).
	for _, name := range []string{
		"Published before 1800", "Published 1800 -1900", "Published 1900 onward",
		"Single author", "General Fiction", "Anthologies", "Modern (19th C)",
		"Culture & Heritage Fiction", "Family Life", "Fictional Biographies & Memoirs",
		"Nautical & Marine Fiction", "Nature & Animal Fiction", "Travel Fiction",
		"Sagas", "Writing & Linguistics", "Transportation", "Games",
	} {
		if g, ok := librivoxGenres[strings.ToLower(name)]; ok {
			t.Errorf("%q maps to %q; it names no single vocabulary entry", name, g)
		}
	}
}
