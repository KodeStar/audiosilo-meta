package check

import (
	"sort"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
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
		// A credit names a person exactly as an author or a narrator does, so it
		// is the same integrity rule - and it is load-bearing for the importer's
		// enrichment pass, which may only credit people the catalogue already
		// holds (enrichment never creates a record).
		for _, c := range w.Credits {
			if !people[c.Person] {
				add(rel, "credit %q (%s) does not exist as a person", c.Person, c.Role)
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

// checkSidecarUniqueness enforces that a work has at most ONE characters
// sidecar and at most one recaps sidecar.
//
// Nothing about the file layout guarantees it during the dual-layout window: a
// work whose sidecars were packed into works-community while its legacy
// works/<shard>/<slug>/characters.json still exists loads both copies, and the
// two walkers each think they are the only source. Without this rule the pair
// reaches metabuild, which fails on a raw UNIQUE constraint naming no file.
func checkSidecarUniqueness(cat *model.Catalog, idx *pathIndex, add addFunc) {
	// Reported in path order rather than load order, so which of the two copies
	// is named as the duplicate does not depend on which walker ran first.
	type sidecar struct{ work, path string }
	report := func(kind string, items []sidecar) {
		sort.Slice(items, func(i, j int) bool {
			if items[i].work != items[j].work {
				return items[i].work < items[j].work
			}
			return items[i].path < items[j].path
		})
		seen := map[string]string{}
		for _, it := range items {
			if prev, dup := seen[it.work]; dup {
				add(it.path, "work %q already has a %s sidecar in %s: a work may have only one", it.work, kind, prev)
				continue
			}
			seen[it.work] = it.path
		}
	}

	chars := make([]sidecar, 0, len(cat.Characters))
	for _, c := range cat.Characters {
		chars = append(chars, sidecar{work: c.Work, path: idx.characters[c]})
	}
	report("characters", chars)

	recaps := make([]sidecar, 0, len(cat.Recaps))
	for _, rc := range cat.Recaps {
		recaps = append(recaps, sidecar{work: rc.Work, path: idx.recaps[rc]})
	}
	report("recaps", recaps)
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

// checkGenresSorted enforces that a work's genres are in ascending order, so
// the same set of genres always serializes identically. The schema already
// rejects duplicates (uniqueItems) and unknown values (the controlled enum);
// this rule only pins the order.
func checkGenresSorted(cat *model.Catalog, idx *pathIndex, add addFunc) {
	for _, w := range cat.Works {
		if len(w.Genres) < 2 {
			continue
		}
		rel := idx.work[w]
		for i := 1; i < len(w.Genres); i++ {
			if w.Genres[i] < w.Genres[i-1] {
				add(rel, "genres must be sorted: %q comes after %q", w.Genres[i], w.Genres[i-1])
			}
		}
	}
}

// checkCreditPairs enforces that a work never credits one person twice in the
// SAME role. A person legitimately appears several times with DIFFERENT roles
// (a source really does credit one person as "editor and translator", which the
// importer emits as two entries), so the uniqueness is on the PAIR, not on the
// person.
//
// The schema's uniqueItems already rejects an exactly-repeated object, which is
// the same defect - but it reports it as "items at index 1 and 3 are equal",
// naming neither the person nor the role. This rule exists so the message a
// contributor reads names both.
//
// Each offending (person, role) pair is reported ONCE per work, however many
// times it repeats: the fix for five copies is the same single edit as for two,
// so repeating an identical line per extra occurrence only buries the other
// problems in the report.
func checkCreditPairs(cat *model.Catalog, idx *pathIndex, add addFunc) {
	for _, w := range cat.Works {
		if len(w.Credits) < 2 {
			continue
		}
		rel := idx.work[w]
		seen := make(map[model.Credit]bool, len(w.Credits))
		reported := make(map[model.Credit]bool)
		for _, c := range w.Credits {
			if seen[c] {
				if !reported[c] {
					reported[c] = true
					add(rel, "credit %q is listed twice as %q; one person holds a role once", c.Person, c.Role)
				}
				continue
			}
			seen[c] = true
		}
	}
}

// normISBN lowercases the check digit so 10-char ISBNs compare case-insensitively.
func normISBN(s string) string { return strings.ToUpper(s) }

// checkPersonSlug enforces that a person's id IS the slug of their name.
//
// "Person slug is the identity" is the importer's central dedup rule: two
// spellings of one name slug the same and merge into one record. The rule only
// holds if every record's id is reachable FROM its name - a record whose id was
// minted under an older slug rule sits at an address nothing will ever probe
// again, so the next import of that person mints a second record instead of
// finding this one. That is exactly how "Madeleine L'Engle" came to exist twice
// (once as madeleine-l-engle, minted before Slugify learned to strip
// apostrophes, and once as madeleine-lengle).
//
// It is a cheap, exact rule, and it is what makes any future change to Slugify
// self-reporting: the day the folding rules move, every record the change
// strands fails here instead of silently forking on the next import.
//
// The id is derived through model.PersonSlug - the SAME call the importer mints
// it with - so the rule can never drift from the minting it polices. That also
// covers the one record whose id is legitimately not its name's slug: the
// shared catch-all a fully-folding name falls back to.
func checkPersonSlug(cat *model.Catalog, idx *pathIndex, add addFunc) {
	for _, p := range cat.People {
		want, _ := model.PersonSlug(p.Name)
		if p.ID != want {
			add(idx.person[p], "person id %q does not match its name %q (want %q); the slug of the name IS the identity", p.ID, p.Name, want)
		}
	}
}
