package titlerule

import (
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

func TestReadSeriesQualifiers(t *testing.T) {
	for _, tc := range []struct {
		name string
		want SeriesQualifiers
	}{
		// Edition groups: exactly SplitEditionName's vocabulary, in any bracket style.
		{"Harry Potter [German Edition]", SeriesQualifiers{"Harry Potter", "de", ""}},
		{"Throne of Glass (Deutsche Ausgabe)", SeriesQualifiers{"Throne of Glass", "de", ""}},
		{"Throne of Glass[French Edition]", SeriesQualifiers{"Throne of Glass", "fr", ""}},
		{"Harry Hole - [Spanish Edition]", SeriesQualifiers{"Harry Hole", "es", ""}},
		// Ordering groups: the measured phrases, mapped onto the model enum.
		{"Drenai [publication order]", SeriesQualifiers{"Drenai", "", model.OrderingPublication}},
		{"Pip & Flinx (published order)", SeriesQualifiers{"Pip & Flinx", "", model.OrderingPublication}},
		{"Riftwar Cycle [Chronological Order]", SeriesQualifiers{"Riftwar Cycle", "", model.OrderingChronological}},
		{"Roma Sub Rosa (Chronological)", SeriesQualifiers{"Roma Sub Rosa", "", model.OrderingChronological}},
		{"The Chronicles of Narnia (Author's Preferred Order)", SeriesQualifiers{"The Chronicles of Narnia", "", model.OrderingRecommended}},
		{"Jackman & Evans (Recommended Listening Order)", SeriesQualifiers{"Jackman & Evans", "", model.OrderingRecommended}},
		{"Jack Ryan (in chronologischer Reihenfolge)", SeriesQualifiers{"Jack Ryan", "", model.OrderingChronological}},
		{"Erweitertes Jack-Ryan-Universum (in Veröffentlichungsreihenfolge)", SeriesQualifiers{"Erweitertes Jack-Ryan-Universum", "", model.OrderingPublication}},
		// Both, in either order.
		{"Pern (Chronological Order) [German Edition]", SeriesQualifiers{"Pern", "de", model.OrderingChronological}},
		{"Pern [German Edition] (Chronological Order)", SeriesQualifiers{"Pern", "de", model.OrderingChronological}},
	} {
		if got := ReadSeriesQualifiers(tc.name); got != tc.want {
			t.Errorf("ReadSeriesQualifiers(%q) = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

// The violating side: everything the closed vocabularies do not name is no
// qualifier, and the name is its own base.
func TestReadSeriesQualifiersDeclines(t *testing.T) {
	for _, name := range []string{
		"Harry Potter",
		"Throne of Glass (Abridged)",                        // a format group is not read
		"Star Wars: The New Jedi Order (abridged)",          // "Order" outside a group is a word
		"Galaxy's Edge: Order of the Centurion",             // a trailing phrase, not a group
		"Border [Mecca]",                                    // an unknown group
		"The Saga [German Edition] Book One",                // the group must END the name
		"[German Edition]",                                  // nothing left to qualify
		"(Chronological Order)",                             // likewise
		"Vorkosigan Saga (Recommended Reading Order)",       // not a measured phrase
		"Horatio Hornblower (publication order) (abridged)", // the LAST group decides first
		"[My Father's Dragon [German Edition]",              // a broken bracket reads as nothing
	} {
		if got := ReadSeriesQualifiers(name); got != (SeriesQualifiers{Base: name}) {
			t.Errorf("ReadSeriesQualifiers(%q) = %+v, want no qualifier", name, got)
		}
	}
	// Two groups of one kind: only the last is read, and the reading stops at the
	// second, so the base keeps the first.
	if got := ReadSeriesQualifiers("X [French Edition] [German Edition]"); got != (SeriesQualifiers{"X [French Edition]", "de", ""}) {
		t.Errorf("two edition groups = %+v", got)
	}
}

// Every edition decoration SplitEditionName reads is a qualifier with the same
// base and language: one vocabulary, read through one shape.
func TestReadSeriesQualifiersAgreesWithSplitEditionName(t *testing.T) {
	for phrase, lang := range editionLanguagePhrases {
		name := "Some Saga (" + phrase + ")"
		base, l, ok := SplitEditionName(name)
		q := ReadSeriesQualifiers(name)
		if !ok || l != lang || q.Language != lang || q.Base != base || q.Ordering != "" {
			t.Errorf("%q: SplitEditionName = %q, %q, %v; ReadSeriesQualifiers = %+v", name, base, l, ok, q)
		}
	}
}

// Every ordering the reader states is one of the schema's.
func TestOrderingPhrasesMapOntoTheModelEnum(t *testing.T) {
	enum := map[string]bool{}
	for _, o := range model.SeriesOrderings() {
		enum[o] = true
	}
	for phrase, o := range orderingPhrases {
		if !enum[o] {
			t.Errorf("%q maps to %q, not a model ordering", phrase, o)
		}
		if model.SlugifyWhole(phrase) != phrase {
			t.Errorf("%q is not in slug form", phrase)
		}
	}
}
