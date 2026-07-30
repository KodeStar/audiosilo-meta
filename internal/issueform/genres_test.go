package issueform

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestGenreVocabulary proves the vocabulary is read from the embedded schema and
// is not vacuously empty (which would make every genre a submitter names look
// unknown, or - if the check were inverted - let anything through).
func TestGenreVocabulary(t *testing.T) {
	set, err := loadGenreVocabulary()
	if err != nil {
		t.Fatalf("embedded genre vocabulary does not load: %v", err)
	}
	if len(set) < 50 {
		t.Errorf("vocabulary has %d entries, want the schema's full list", len(set))
	}
	for _, g := range []string{"epic-fantasy", "coming-of-age"} {
		if !set[g] {
			t.Errorf("%q is missing from the loaded vocabulary", g)
		}
	}
	if set["spaceships"] {
		t.Error("an invented genre must not be in the vocabulary")
	}
	// The accessor used by composition returns the same set (and does not panic).
	if len(genreVocabulary()) != len(set) {
		t.Errorf("genreVocabulary() has %d entries, loadGenreVocabulary %d", len(genreVocabulary()), len(set))
	}
}

// workGenres reads the genres of a composed work record. ok is false when the
// record carries no genres key at all.
func workGenres(t *testing.T, dir, rel string) (genres []string, ok bool) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
	raw, present := doc["genres"]
	if !present {
		return nil, false
	}
	if err := json.Unmarshal(raw, &genres); err != nil {
		t.Fatalf("parse %s genres: %v", rel, err)
	}
	return genres, true
}

// addWorkBodyWithGenres builds an add-work body carrying a Genres field value.
func addWorkBodyWithGenres(t *testing.T, title, genres string) string {
	t.Helper()
	body := addWorkBody(title, "Gina Author", "en", "Gus Voice", "", "web", true)
	return strings.Replace(body, field(fWorkGenres, ""), field(fWorkGenres, genres), 1)
}

// TestAddWorkGenresNormalized covers the whole normalization contract in one
// submission: case folding, whitespace, deduplication, and the ascending sort
// checkGenresSorted requires (the input is deliberately out of order).
func TestAddWorkGenresNormalized(t *testing.T) {
	dir := seedTree(t)
	body := addWorkBodyWithGenres(t, "Genre Book", " Epic-Fantasy ,coming-of-age, EPIC-FANTASY ")
	res := Process(Options{DataDir: dir, Template: "add-work", Body: body})
	if res.Status != StatusOK {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}
	got, ok := workGenres(t, dir, "works/ge/genre-book/work.json")
	if !ok {
		t.Fatal("work carries no genres key")
	}
	if want := []string{"coming-of-age", "epic-fantasy"}; !reflect.DeepEqual(got, want) {
		t.Errorf("genres = %v, want %v", got, want)
	}
}

// TestAddWorkGenresOnePerLine covers the other list shape the form accepts
// (splitList takes commas or newlines).
func TestAddWorkGenresOnePerLine(t *testing.T) {
	dir := seedTree(t)
	body := addWorkBodyWithGenres(t, "Lined Genre Book", "epic-fantasy\ncoming-of-age")
	res := Process(Options{DataDir: dir, Template: "add-work", Body: body})
	if res.Status != StatusOK {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}
	got, ok := workGenres(t, dir, "works/li/lined-genre-book/work.json")
	if !ok {
		t.Fatal("work carries no genres key")
	}
	if want := []string{"coming-of-age", "epic-fantasy"}; !reflect.DeepEqual(got, want) {
		t.Errorf("genres = %v, want %v", got, want)
	}
}

// TestAddWorkGenresUnknownInvalid proves an off-vocabulary value fails the whole
// submission (never silently dropped) and that the message names the value.
func TestAddWorkGenresUnknownInvalid(t *testing.T) {
	dir := seedTree(t)
	body := addWorkBodyWithGenres(t, "Bad Genre Book", "epic-fantasy, spaceships")
	res := Process(Options{DataDir: dir, Template: "add-work", Body: body})
	if res.Status != StatusInvalid {
		t.Fatalf("status = %q, want invalid; messages = %v", res.Status, res.Messages)
	}
	joined := strings.Join(res.Messages, "\n")
	if !strings.Contains(joined, "spaceships") || !strings.Contains(joined, "common.schema.json") {
		t.Errorf("message must name the bad value and the vocabulary: %v", res.Messages)
	}
	// Nothing was written for a rejected submission.
	if _, err := os.Stat(filepath.Join(dir, "works", "ba", "bad-genre-book")); !os.IsNotExist(err) {
		t.Errorf("a rejected submission must not write records (stat err = %v)", err)
	}
}

// TestAddWorkNoGenres pins the absent-field behaviour: no genres key at all
// (the schema's genre_list requires at least one entry, so an empty array would
// fail validation).
func TestAddWorkNoGenres(t *testing.T) {
	dir := seedTree(t)
	body := addWorkBody("Genreless Book", "Gina Author", "en", "Gus Voice", "", "web", true)
	res := Process(Options{DataDir: dir, Template: "add-work", Body: body})
	if res.Status != StatusOK {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}
	if got, ok := workGenres(t, dir, "works/ge/genreless-book/work.json"); ok {
		t.Errorf("genres = %v, want the key to be absent", got)
	}
}
