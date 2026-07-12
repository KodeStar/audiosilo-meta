package check

import (
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/model"
)

// checkIntegrity verifies every cross-entity reference resolves.
func checkIntegrity(cat *model.Catalog, workByID map[string]*model.Work, recs []recordWithPath, idx *pathIndex, add addFunc) {
	people := map[string]bool{}
	for _, p := range cat.People {
		people[p.ID] = true
	}

	for _, w := range cat.Works {
		rel := idx.work[w]
		for _, a := range w.Authors {
			if !people[a] {
				add(rel, "author %q does not exist as a person", a)
			}
		}
	}

	for _, pr := range recs {
		rel := pr.path
		for _, n := range pr.rec.Narrators {
			if !people[n] {
				add(rel, "narrator %q does not exist as a person", n)
			}
		}
		if workByID[pr.workSlug] == nil {
			add(rel, "parent work %q does not exist", pr.workSlug)
		}
	}

	for _, s := range cat.Series {
		rel := idx.series[s]
		for _, a := range s.Authors {
			if !people[a] {
				add(rel, "series author %q does not exist as a person", a)
			}
		}
		for _, sw := range s.Works {
			if workByID[sw.Work] == nil {
				add(rel, "series work %q does not exist", sw.Work)
			}
		}
	}

	for _, c := range cat.Characters {
		if workByID[c.Work] == nil {
			add(idx.characters[c], "parent work %q does not exist", c.Work)
		}
	}
	for _, rc := range cat.Recaps {
		if workByID[rc.Work] == nil {
			add(idx.recaps[rc], "parent work %q does not exist", rc.Work)
		}
	}
}

// checkCharacters enforces that character ids are unique within each per-work
// characters file. (Ids need not be globally unique - different works may each
// have their own "bilbo-baggins" card.)
func checkCharacters(cat *model.Catalog, idx *pathIndex, add addFunc) {
	for _, c := range cat.Characters {
		rel := idx.characters[c]
		seen := map[string]bool{}
		for _, ch := range c.Characters {
			if seen[ch.ID] {
				add(rel, "duplicate character id %q", ch.ID)
			}
			seen[ch.ID] = true
		}
	}
}

// checkRecaps enforces that no two recaps in a per-work recaps file share the
// same through-position (one recap per catch-up point).
func checkRecaps(cat *model.Catalog, idx *pathIndex, add addFunc) {
	for _, rc := range cat.Recaps {
		rel := idx.recaps[rc]
		// Key on the whole Position (a comparable struct) so the rule stays
		// correct if the position schema ever gains a field beyond chapter.
		seen := map[model.Position]bool{}
		for _, r := range rc.Recaps {
			if seen[r.Through] {
				add(rel, "duplicate recap through chapter %d", r.Through.Chapter)
			}
			seen[r.Through] = true
		}
	}
}

// checkUniqueness enforces globally unique (region, asin) pairs and ISBNs across
// all recordings, and unique xref.wikidata within each entity type.
func checkUniqueness(cat *model.Catalog, recs []recordWithPath, idx *pathIndex, add addFunc) {
	asinSeen := map[string]string{} // "region/asin" -> path
	isbnSeen := map[string]string{} // isbn -> path
	for _, pr := range recs {
		rel := pr.path
		for _, a := range pr.rec.ASIN {
			key := a.Region + "/" + a.ASIN
			if prev, ok := asinSeen[key]; ok {
				add(rel, "duplicate ASIN %s (region %s) also in %s", a.ASIN, a.Region, prev)
			} else {
				asinSeen[key] = rel
			}
		}
		for _, isbn := range pr.rec.ISBN {
			norm := normISBN(isbn)
			if prev, ok := isbnSeen[norm]; ok {
				add(rel, "duplicate ISBN %s also in %s", isbn, prev)
			} else {
				isbnSeen[norm] = rel
			}
		}
	}

	wikiWork := map[string]string{}
	for _, w := range cat.Works {
		if w.Xref == nil || w.Xref.Wikidata == "" {
			continue
		}
		rel := idx.work[w]
		if prev, ok := wikiWork[w.Xref.Wikidata]; ok {
			add(rel, "duplicate work xref.wikidata %s also in %s", w.Xref.Wikidata, prev)
		} else {
			wikiWork[w.Xref.Wikidata] = rel
		}
	}

	wikiPerson := map[string]string{}
	for _, p := range cat.People {
		if p.Xref == nil || p.Xref.Wikidata == "" {
			continue
		}
		rel := idx.person[p]
		if prev, ok := wikiPerson[p.Xref.Wikidata]; ok {
			add(rel, "duplicate person xref.wikidata %s also in %s", p.Xref.Wikidata, prev)
		} else {
			wikiPerson[p.Xref.Wikidata] = rel
		}
	}

	wikiSeries := map[string]string{}
	for _, s := range cat.Series {
		if s.Xref == nil || s.Xref.Wikidata == "" {
			continue
		}
		rel := idx.series[s]
		if prev, ok := wikiSeries[s.Xref.Wikidata]; ok {
			add(rel, "duplicate series xref.wikidata %s also in %s", s.Xref.Wikidata, prev)
		} else {
			wikiSeries[s.Xref.Wikidata] = rel
		}
	}
}

// checkChapters enforces that a recording's chapters start at 0 and have
// strictly increasing start offsets.
func checkChapters(recs []recordWithPath, add addFunc) {
	for _, pr := range recs {
		ch := pr.rec.Chapters
		if len(ch) == 0 {
			continue
		}
		rel := pr.path
		if ch[0].StartMS != 0 {
			add(rel, "first chapter must start at 0, got %d", ch[0].StartMS)
		}
		for i := 1; i < len(ch); i++ {
			if ch[i].StartMS <= ch[i-1].StartMS {
				add(rel, "chapter %d start_ms %d is not greater than previous %d", i, ch[i].StartMS, ch[i-1].StartMS)
			}
		}
	}
}

// checkSeriesPositions enforces that no two works share a position in one series.
func checkSeriesPositions(cat *model.Catalog, idx *pathIndex, add addFunc) {
	for _, s := range cat.Series {
		rel := idx.series[s]
		seen := map[string]string{}
		for _, sw := range s.Works {
			if prev, ok := seen[sw.Position]; ok {
				add(rel, "duplicate series position %q (works %q and %q)", sw.Position, prev, sw.Work)
			} else {
				seen[sw.Position] = sw.Work
			}
		}
	}
}

// normISBN lowercases the check digit so 10-char ISBNs compare case-insensitively.
func normISBN(s string) string { return strings.ToUpper(s) }
