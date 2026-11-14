package query

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/artifacttest"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

func discoveryFixture() *model.Catalog {
	cat := &model.Catalog{}
	// IDs deliberately disagree with display order, with a case-insensitive tie.
	for i, title := range []string{"Zulu", "alpha", "Alpha", "9 Lives", "0 Start", "'Quoted' & <safe>", "Étoile", "東京", "[Bracket]", "Mixed Mistborn", "Mixed Stormlight", "Zenith"} {
		id := fmt.Sprintf("id-%02d", 20-i)
		cat.Works = append(cat.Works, &model.Work{ID: id, Title: title, Language: "en", License: "CC0-1.0"})
		cat.People = append(cat.People, &model.Person{ID: id, Name: title, License: "CC0-1.0"})
		cat.Series = append(cat.Series, &model.Series{ID: id, Name: title, License: "CC0-1.0"})
		if i%2 == 0 {
			cat.Characters = append(cat.Characters, &model.Characters{Work: id, License: "CC-BY-SA-4.0", Characters: []model.Character{{ID: "character", Name: "Character"}, {ID: "other", Name: "Other"}}})
			cat.Recaps = append(cat.Recaps, &model.Recaps{Work: id, License: "CC-BY-SA-4.0", InShort: "Summary", Recaps: []model.Recap{{Through: model.Position{Chapter: 1}, Text: "Recap"}}})
		}
	}
	return cat
}

func TestBrowseFilteredDisplayOrderAndBuckets(t *testing.T) {
	snap := openTestDB(t, artifacttest.Build(t, discoveryFixture()))
	for _, family := range []PageFamily{WorkPages, PersonPages, SeriesPages, CharacterPages, RecapPages} {
		t.Run(string(family), func(t *testing.T) {
			all, err := snap.BrowseFiltered(t.Context(), family, BrowseFilters{}, 100, 0)
			if err != nil {
				t.Fatal(err)
			}
			seen := map[string]bool{}
			letters := []string{"0-9", "Other"}
			for c := 'A'; c <= 'Z'; c++ {
				letters = append(letters, string(c))
			}
			for _, letter := range letters {
				page, err := snap.BrowseFiltered(t.Context(), family, BrowseFilters{Letter: letter}, 100, 0)
				if err != nil {
					t.Fatal(err)
				}
				if page.Total != len(page.Entries) {
					t.Fatalf("%s total=%d entries=%d", letter, page.Total, len(page.Entries))
				}
				for _, e := range page.Entries {
					if seen[e.ID] {
						t.Fatalf("%s appears in two buckets", e.ID)
					}
					seen[e.ID] = true
					initial := e.Title[0]
					if letter == "0-9" && (initial < '0' || initial > '9') {
						t.Fatal(e)
					}
					if len(letter) == 1 && strings.ToUpper(e.Title[:1]) != letter {
						t.Fatal(e)
					}
				}
			}
			if len(seen) != all.Total {
				t.Fatalf("buckets reach %d/%d", len(seen), all.Total)
			}
			if !slices.IsSortedFunc(all.Entries, func(a, b BrowseEntry) int {
				if c := strings.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title)); c != 0 {
					return c
				}
				return strings.Compare(a.ID, b.ID)
			}) {
				t.Fatal("display order is unstable")
			}
			first, _ := snap.BrowseFiltered(t.Context(), family, BrowseFilters{}, 3, 0)
			second, _ := snap.BrowseFiltered(t.Context(), family, BrowseFilters{}, 3, 3)
			if first.Limit != 3 || len(second.Entries) != 3 || first.Entries[2].ID == second.Entries[0].ID {
				t.Fatal("invalid bounded windows")
			}
			if !slices.Equal(first.Entries, all.Entries[:3]) || !slices.Equal(second.Entries, all.Entries[3:6]) {
				t.Fatal("window differs from full order")
			}
		})
	}
}

func TestBrowseFilteredSearchCompositionAndGuideScope(t *testing.T) {
	snap := openTestDB(t, artifacttest.Build(t, discoveryFixture()))
	for _, family := range []PageFamily{WorkPages, PersonPages, SeriesPages, CharacterPages, RecapPages} {
		page, err := snap.BrowseFiltered(t.Context(), family, BrowseFilters{Query: "Stormlight", Letter: "m"}, 1000, -1)
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != 1 || len(page.Entries) != 1 || page.Entries[0].Title != "Mixed Stormlight" || page.Limit != 100 || page.Offset != 0 {
			t.Fatalf("%s: %+v", family, page)
		}
		page, err = snap.BrowseFiltered(t.Context(), family, BrowseFilters{Query: "Mistborn"}, 50, 0)
		if err != nil {
			t.Fatal(err)
		}
		want := 1
		if family == CharacterPages || family == RecapPages {
			want = 0
		}
		if page.Total != want {
			t.Fatalf("%s guide scope: %+v", family, page)
		}
		for _, term := range []string{`'Quoted'`, `" OR *`, "東京", "Étoile", "[Bracket]", "no-such-title"} {
			if _, err := snap.BrowseFiltered(t.Context(), family, BrowseFilters{Query: term}, 50, 0); err != nil {
				t.Fatalf("%q: %v", term, err)
			}
		}
	}
	// Existing callers continue receiving the old stable ID ordering.
	old, err := snap.Browse(t.Context(), WorkPages, 100, 0)
	if err != nil || !slices.IsSortedFunc(old.Entries, func(a, b BrowseEntry) int { return strings.Compare(a.ID, b.ID) }) {
		t.Fatalf("Browse contract changed: %v", err)
	}
}

func TestBrowseFilterValidationAndLegacyIndexes(t *testing.T) {
	for _, f := range []BrowseFilters{{Query: strings.Repeat("x", 257)}, {Query: string([]byte{0xff})}, {Letter: "AA"}, {Letter: "é"}, {Letter: "0"}, {Letter: "%"}} {
		if _, err := NormalizeBrowseFilters(f); err == nil {
			t.Errorf("accepted %+v", f)
		}
	}
	if f, err := NormalizeBrowseFilters(BrowseFilters{Query: "  Mixed  Stormlight  ", Letter: "other"}); err != nil || f.Query != "Mixed Stormlight" || f.Letter != "Other" {
		t.Fatalf("normalization: %+v %v", f, err)
	}
	path := artifacttest.Altered(t, discoveryFixture(), 11, "DROP INDEX idx_works_title_nocase", "DROP INDEX idx_people_name_nocase", "DROP INDEX idx_series_name_nocase", "ALTER TABLE recordings DROP COLUMN abridgement")
	snap := openTestDB(t, path)
	for _, family := range []PageFamily{WorkPages, PersonPages, SeriesPages, CharacterPages, RecapPages} {
		page, err := snap.BrowseFiltered(t.Context(), family, BrowseFilters{Letter: "Z"}, 50, 0)
		if err != nil || page.Total == 0 {
			t.Fatalf("legacy %s: %+v %v", family, page, err)
		}
	}
}

func TestBrowseFilteredUsesDisplayIndexes(t *testing.T) {
	snap := openTestDB(t, artifacttest.Build(t, discoveryFixture()))
	for _, family := range []PageFamily{WorkPages, PersonPages, SeriesPages} {
		for _, letter := range []string{"", "M", "Z", "0-9"} {
			base, title, language, args := snap.browseFilteredSQL(family, BrowseFilters{Letter: letter})
			plan := strings.Join(queryPlan(t, snap, `SELECT id, `+title+`, `+language+base+` ORDER BY `+title+` COLLATE NOCASE, id LIMIT ? OFFSET ?`, append(args, 50, 0)...), " | ")
			if !strings.Contains(plan, "_nocase") || strings.Contains(plan, "TEMP B-TREE") {
				t.Errorf("%s %s: %s", family, letter, plan)
			}
		}
	}
}
