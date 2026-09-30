package build

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/kodestar/audiosilo-meta/pkg/model"
	_ "modernc.org/sqlite"
)

// languagesCatalog is a small franchise carrying every schema_version 7 fact: an
// English original and its German translation (a work link AND a series link),
// a translated omnibus naming TWO originals (the set form, deliberately out of
// order in the record), a chronological variant ordering of the English series,
// and a series whose members tie on language (no derived language).
func languagesCatalog() *model.Catalog {
	p := &model.Person{ID: "jane-doe", Name: "Jane Doe", License: "CC0-1.0"}
	work := func(id, title, lang string, of ...string) *model.Work {
		return &model.Work{
			ID: id, Title: title, Language: lang, Authors: []string{"jane-doe"},
			License: "CC0-1.0", TranslationOf: of,
		}
	}
	one := work("book-one", "Book One", "en")
	two := work("book-two", "Book Two", "en")
	prequel := work("the-prequel", "The Prequel", "en")
	eins := work("buch-eins", "Buch Eins", "de", "book-one")
	omni := work("sammelband", "Sammelband", "de", "book-two", "book-one")
	french := work("livre-un", "Livre Un", "fr")

	members := func(ids ...string) []model.SeriesWork {
		var out []model.SeriesWork
		for i, id := range ids {
			out = append(out, model.SeriesWork{Work: id, Position: string(rune('1' + i))})
		}
		return out
	}
	return &model.Catalog{
		People: []*model.Person{p},
		Works:  []*model.Work{one, two, prequel, eins, omni, french},
		Series: []*model.Series{
			{ID: "saga", Name: "Saga", License: "CC0-1.0", Works: members("book-one", "book-two"), Ordering: model.OrderingPublication},
			{
				ID: "saga-chronological", Name: "Saga (Chronological)", License: "CC0-1.0",
				Works:    members("the-prequel", "book-one", "book-two"),
				Ordering: model.OrderingChronological, OrderingOf: "saga",
			},
			{ID: "saga-de", Name: "Saga (Deutsch)", License: "CC0-1.0", Works: members("buch-eins", "sammelband"), TranslationOf: []string{"saga"}},
			{ID: "mixed", Name: "Mixed", License: "CC0-1.0", Works: members("book-one", "livre-un")},
		},
	}
}

// TestBuildTranslations pins the translations table: one row per (translation,
// original) pair, in primary-key order (namespace, translation, target) - so a
// record's set is sorted here even when it arrived out of order.
func TestBuildTranslations(t *testing.T) {
	got := triples(t, buildDB(t, languagesCatalog()), `SELECT kind, id, target FROM translations`)
	want := [][3]string{
		{"series", "saga-de", "saga"},
		{"works", "buch-eins", "book-one"},
		{"works", "sammelband", "book-one"},
		{"works", "sammelband", "book-two"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("translations = %v, want %v", got, want)
	}
}

// TestBuildSeriesLanguagesAndOrderings pins the three new series columns: the
// DERIVED language (model.SeriesLanguage - NULL on a tie), and the ordering and
// ordering_of the record states (NULL when it states none).
func TestBuildSeriesLanguagesAndOrderings(t *testing.T) {
	db := buildDB(t, languagesCatalog())
	want := map[string][3]sql.NullString{
		"saga":               {valid("en"), valid(model.OrderingPublication), {}},
		"saga-chronological": {valid("en"), valid(model.OrderingChronological), valid("saga")},
		"saga-de":            {valid("de"), {}, {}},
		"mixed":              {{}, {}, {}}, // one English, one French member: a tie
	}
	for id, w := range want {
		var got [3]sql.NullString
		if err := db.QueryRow(`SELECT language, ordering, ordering_of FROM series WHERE id=?`, id).
			Scan(&got[0], &got[1], &got[2]); err != nil {
			t.Fatalf("series %s: %v", id, err)
		}
		if got != w {
			t.Errorf("series %s (language, ordering, ordering_of) = %v, want %v", id, got, w)
		}
	}
}

func valid(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }

// TestBuildSearchLanguage pins search_fts.language: a work row carries its own
// tag, a series row its derived language (empty on a tie) and a person row an
// empty one. It is the LAST column, so the four columns every existing reader
// names are where they always were.
func TestBuildSearchLanguage(t *testing.T) {
	db := buildDB(t, languagesCatalog())
	want := map[[2]string]string{
		{"work", "buch-eins"}:  "de",
		{"work", "livre-un"}:   "fr",
		{"series", "saga-de"}:  "de",
		{"series", "mixed"}:    "",
		{"person", "jane-doe"}: "",
	}
	for key, lang := range want {
		var got string
		if err := db.QueryRow(`SELECT language FROM search_fts WHERE kind=? AND id=?`, key[0], key[1]).Scan(&got); err != nil {
			t.Fatalf("search row %v: %v", key, err)
		}
		if got != lang {
			t.Errorf("search row %v language = %q, want %q", key, got, lang)
		}
	}
	var cols []string
	rows, err := db.Query(`SELECT name FROM pragma_table_info('search_fts')`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		cols = append(cols, c)
	}
	if want := []string{"kind", "id", "title", "names", "language"}; !slices.Equal(cols, want) {
		t.Errorf("search_fts columns = %v, want %v", cols, want)
	}
}

// TestBuildLanguageIndexes pins the three indexes the v7 readers rely on, by
// their COLUMNS: internal/serve EXPLAINs its own queries against them, and this is
// the builder's half. The shapes are the point - idx_translations_target carries
// the id so it covers the reverse translation read (without it the planner walks
// the primary key's kind prefix instead), and idx_works_language is the language
// alone, all the stats census reads.
func TestBuildLanguageIndexes(t *testing.T) {
	db := buildDB(t, languagesCatalog())
	for idx, want := range map[string][]string{
		"idx_series_ordering_of":  {"ordering_of"},
		"idx_translations_target": {"kind", "target", "id"},
		"idx_works_language":      {"language"},
	} {
		rows, err := db.Query(`SELECT name FROM pragma_index_info(?) ORDER BY seqno`, idx)
		if err != nil {
			t.Fatal(err)
		}
		var cols []string
		for rows.Next() {
			var c string
			if err := rows.Scan(&c); err != nil {
				t.Fatal(err)
			}
			cols = append(cols, c)
		}
		_ = rows.Close()
		if !slices.Equal(cols, want) {
			t.Errorf("index %s columns = %v, want %v", idx, cols, want)
		}
	}
}

// TestBuildLanguagesIsDeterministic builds the languages catalogue twice, with
// the input slices shuffled the second time, and requires the same bytes: the
// determinism rule every table follows, applied to the new rows.
func TestBuildLanguagesIsDeterministic(t *testing.T) {
	at := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	render := func(cat *model.Catalog) []byte {
		out := filepath.Join(t.TempDir(), "meta.sqlite")
		if err := Build(cat, out, at); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	first := render(languagesCatalog())
	rev := languagesCatalog()
	slices.Reverse(rev.Works)
	slices.Reverse(rev.Series)
	if !bytes.Equal(first, render(rev)) {
		t.Error("the artifact depends on the catalogue's input order")
	}
}
