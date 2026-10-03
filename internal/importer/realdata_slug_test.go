package importer

// realdata_slug_test.go pins the two id migrations that put every record on the
// address today's Slugify composes: the transliteration change (no record may
// sit at an address the letter-DROPPING fold produced) and its follow-up, the
// pre-apostrophe-rule migration (no record may sit at an address that spells an
// apostrophe as a word boundary - "angela-s-ashes" for "Angela's Ashes").
//
// It is not a restatement of metacheck. metacheck owns the person rule
// (checkPersonSlug) and runs as its own CI step; what is only tested here is the
// works and series half, which no rule covers - a work slug is composed
// (cleanWorkTitle -> Slugify -> workSlugAt), so it cannot be a pure function of
// the title and metacheck cannot check it. That composed chain is also how the
// importer FINDS an existing work, which is what makes a stale id expensive: the
// next import composes the new base, misses the record, and mints a duplicate
// work instead of extending the one that is there.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"golang.org/x/text/unicode/norm"
)

// dataDir is the committed database, relative to this package.
const dataDir = "../../data"

// addedApostropheGlyphs are the apostrophe-position glyphs Slugify's replacer
// gained in the same change. They are listed rather than derived because a
// stripped glyph leaves no trace in the output to derive it from - Slugify turns
// all of them into nothing, exactly like the glyphs it always stripped.
var addedApostropheGlyphs = []rune{'‘', '´', 'ʻ'}

// aposAsSeparator spells every apostrophe-position glyph as a space, which is
// exactly what Slugify did with them before it learned to strip them: they fell
// through to the separator branch. Comparing the two renderings is how
// movedByApostrophe decides a title is affected, so the predicate is computed
// FROM Slugify rather than restating the glyph set a second time.
var aposAsSeparator = strings.NewReplacer(
	"'", " ", "’", " ", "‘", " ", "´", " ", "`", " ", "ʼ", " ", "ʻ", " ",
)

// movedByApostrophe reports whether s is spelled differently by today's
// apostrophe-stripping rule than by the word-boundary rule that preceded it -
// "Angela's Ashes" (angelas-ashes today, angela-s-ashes then), but not "Angela
// Ashes".
func movedByApostrophe(s string) bool {
	return model.Slugify(aposAsSeparator.Replace(s)) != model.Slugify(s)
}

// movedByTransliteration reports whether s carries a character the
// transliteration change moved.
//
// The letter half is computed FROM Slugify rather than restated, so it cannot
// drift from the table: a transliterated letter is one NFD leaves whole (its
// decomposition does not start with an ASCII letter, which is what an accented
// ASCII letter decomposes to) that Slugify nonetheless gets ASCII out of.
func movedByTransliteration(s string) bool {
	for _, r := range s {
		if r < utf8.RuneSelf {
			continue
		}
		for _, g := range addedApostropheGlyphs {
			if r == g {
				return true
			}
		}
		d := []rune(norm.NFD.String(string(r)))
		if len(d) > 0 && d[0] < utf8.RuneSelf {
			continue // an accented ASCII letter; the fold always handled it
		}
		if model.Slugify(string(r)) != "" {
			return true
		}
	}
	return false
}

// walkEntries calls fn for every entry of a pack family.
//
// It skips under -race, which is every real-data test in this file: the walk
// starts no goroutine, so the detector has nothing to find, and it cost ~3.5
// minutes there against ~16s without it. The non-race run still walks the tree.
func walkEntries(t *testing.T, family string, fn func(path, key string, entry map[string]any)) {
	if raceEnabled {
		t.Skip("skipped under -race: the real-data walk is sequential, so it adds no race coverage; the non-race run covers it")
	}
	root := filepath.Join(dataDir, family)
	if _, err := os.Stat(root); err != nil {
		t.Skipf("no data tree at %s: %v", root, err)
	}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".json") {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		var pk struct {
			Entries map[string]map[string]any `json:"entries"`
		}
		if err := json.Unmarshal(b, &pk); err != nil {
			return err
		}
		for k, e := range pk.Entries {
			fn(p, k, e)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func str(entry map[string]any, field string) string {
	s, _ := entry[field].(string)
	return s
}

// strList reads a string array field off a decoded entry.
func strList(entry map[string]any, field string) []string {
	raw, _ := entry[field].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// entryWorkAuthors is the row a work record describes, reconstructed from the
// record itself: its authors[] in stored order as the full credit list, and the
// role-credited people of its credits[] excluded from the identity list -
// diskIdentityAuthors' rule, kept ORDERED because the chain's suffix is built
// from identity[0] and a set has no first element.
//
// Reconstructing the whole list rather than taking authors[0] is what makes the
// recomputation a model of the importer instead of a weaker caricature of it.
// The importer walks the chain with the row's WHOLE credit list: the mintable
// suffix comes from the first IDENTITY author, and every other author of both
// lists is probed (workCandidates). A one-author model composes neither. It
// agreed with the real chain only for as long as every in-scope record's suffix
// happened to be its authors[0], which the credited-contributor exclusion breaks
// by design: "Kiki's Delivery Service" lists its translator first, so the work
// sits at kikis-delivery-service-eiko-kadono - candidate 1 of the real chain,
// and nowhere on a chain rooted at emily-balistrieri.
//
// A work with no authors at all keeps the empty-string root the one-author model
// used, so such a record is judged exactly as before rather than panicking on
// identity[0].
func entryWorkAuthors(entry map[string]any) workAuthors {
	all := strList(entry, "authors")
	if len(all) == 0 {
		all = []string{""}
	}
	roleCredited := map[string]bool{}
	credits, _ := entry["credits"].([]any)
	for _, c := range credits {
		if m, ok := c.(map[string]any); ok {
			roleCredited[str(m, "person")] = true
		}
	}
	return workAuthors{all: all, identity: identityAuthors(all, roleCredited)}
}

// dataSeriesClaims maps every committed work to the series claim a row about
// that book states: the series it belongs to and the position it sits at.
//
// The recomputation below needs it because part of the chain is claim-dependent.
// A work whose title cannot tell it from its siblings sits on a
// "book-<position>" slug (workidentity.go), and the only rows that reach that
// slug are the ones stating the position - so recomputing the chain from the
// work record ALONE would declare a legitimately-reachable work unreachable. The
// claim is not in the work record; a work does not know its series, the series
// knows its works. Reading it from the series family is therefore the same fact
// the importing row carried, recorded durably, and not an assumption the test
// invented to make itself pass: a row that does not state it genuinely cannot
// find these works, which is exactly what the importer enforces.
//
// First membership wins, matching the importer's own first-claim-wins rule.
func dataSeriesClaims(t *testing.T) map[string]positionClaim {
	out := map[string]positionClaim{}
	walkEntries(t, "series", func(path, key string, e map[string]any) {
		name := str(e, "name")
		works, _ := e["works"].([]any)
		for _, w := range works {
			m, ok := w.(map[string]any)
			if !ok {
				continue
			}
			slug, pos := str(m, "work"), str(m, "position")
			if slug == "" || pos == "" {
				continue
			}
			if _, seen := out[slug]; !seen {
				out[slug] = positionClaim{series: name, pos: pos}
			}
		}
	})
	return out
}

// TestDataPeopleSlugsAreTransliterated is the migration's completeness proof for
// the people family: every committed person's id is the slug of their name under
// today's Slugify. A record the migration missed fails here with the name that
// no longer slugs to its address.
func TestDataPeopleSlugsAreTransliterated(t *testing.T) {
	walkEntries(t, "people", func(path, key string, e map[string]any) {
		name := str(e, "name")
		want, _ := model.PersonSlug(name)
		if key != want {
			t.Errorf("%s: person %q has name %q, which slugs to %q", path, key, name, want)
		}
		if id := str(e, "id"); id != key {
			t.Errorf("%s: person entry key %q disagrees with its id %q", path, key, id)
		}
	})
}

// TestDataWorkAndSeriesSlugsAreCurrent is the half no rule covers. It asks about
// every record whose title or name is spelled differently by a slug rule that
// has CHANGED - a transliterated letter, or an apostrophe - and for each of
// those the id must sit on the candidate chain the importer walks TODAY, or the
// next wave mints a duplicate beside it.
//
// The recomputation models the importing ROW from the record itself, and both
// halves of that matter. Part of the chain is CLAIM-dependent - a serial volume
// whose title cannot tell it from its siblings sits on a "book-<position>" slug,
// reachable only by a row stating the position - so each work's claim is read
// out of the series family (dataSeriesClaims) rather than pretending a
// claim-less row is the only kind there is. The rest is CREDIT-dependent: the
// suffix is built from the first IDENTITY author and every other credited person
// is probed, so the row is reconstructed with its whole author list and its role
// credits (entryWorkAuthors) rather than with authors[0] standing in for all of
// them.
//
// It deliberately does not demand that of every record: a work slug is composed,
// so a record can be legitimately off-chain (the catalogue holds one whose title
// was later corrected). Scoping the assertion to the characters a rule change
// moved is what keeps this a proof of the two migrations rather than a standing
// complaint about composed identity in general.
//
// The SERIES arm asks the importer's real reach question, not the chain alone:
// a series off its name's chain is current when SeriesAuthorIndex.Resolve over
// the loaded catalogue still joins it - which the qualifier index does for a
// linked edition series renamed to its base name (seriesqualified.go). Only a
// series nothing reaches fails, and the message carries the resolution's answer.
// The chain test runs first as a cost shortcut over the same question (a series
// on its chain is reached), so the catalogue load (~25s) runs only when some
// series is off its chain.
func TestDataWorkAndSeriesSlugsAreCurrent(t *testing.T) {
	claims := dataSeriesClaims(t)
	walkEntries(t, "works", func(path, key string, e map[string]any) {
		title := str(e, "title")
		if !movedByTransliteration(title) && !movedByApostrophe(title) {
			return
		}
		cands, _ := workCandidates(Slugify(cleanWorkTitle(title)),
			entryWorkAuthors(e), claims[key])
		for _, cand := range cands {
			if cand.slug == key {
				return
			}
		}
		t.Errorf("%s: work %q (title %q) is on no candidate of today's slug chain; "+
			"an import of this title would mint a duplicate work", path, key, title)
	})

	type offChain struct{ path, key, name string }
	var off []offChain
	walkEntries(t, "series", func(path, key string, e map[string]any) {
		name := str(e, "name")
		if !movedByTransliteration(name) && !movedByApostrophe(name) {
			return
		}
		base := Slugify(name)
		for i := 0; i < 50; i++ {
			if NumberedSlugAt(base, i) == key {
				return
			}
		}
		off = append(off, offChain{path, key, name})
	})
	// The chain test above is a COST shortcut over the same question, not a
	// different rule: a series on its name's chain is one the resolution
	// reaches, so only the series off it pay for the catalogue load (~25s).
	if len(off) == 0 {
		return
	}

	// A series off its name's chain is still current when the importer's
	// resolution REACHES it: a linked edition series renamed to its base name
	// ("Dreamer's Throne" at dreamers-throne-german-edition) is joined through
	// the qualifier index by a claim naming that base in its language. So the
	// question is asked of the real resolution (SeriesAuthorIndex.Resolve over
	// the loaded catalogue) - see reachesSeries.
	cat := loadSlugCatalogue(t)
	for _, o := range off {
		s := cat.series[o.key]
		if s == nil {
			t.Errorf("%s: series %q is not in the loaded catalogue", o.path, o.key)
			continue
		}
		m, ok := cat.reachesSeries(s)
		if ok {
			continue
		}
		why := m.Why()
		if why == "" {
			why = "nothing stepped past"
		}
		t.Errorf("%s: series %q (name %q) is on no candidate of today's slug chain "+
			"and the importer's resolution does not reach it from any member's row "+
			"(the last claim resolves to %q, found=%v: %s); "+
			"an import naming this series would mint a duplicate",
			o.path, o.key, o.name, m.Slug, m.Found, why)
	}
}

// slugCatalogue is the loaded catalogue the series arm resolves against: the
// importer's series index over it plus the id lookups a row is built from.
type slugCatalogue struct {
	cat    *model.Catalog
	ix     *SeriesAuthorIndex
	works  map[string]*model.Work
	series map[string]*model.Series
	names  map[string]string // person id -> name
}

func loadSlugCatalogue(t *testing.T) *slugCatalogue {
	t.Helper()
	res := check.Load(dataDir)
	if !res.OK() {
		t.Fatalf("loading %s: %d problem(s), first: %s", dataDir, len(res.Problems), res.Problems[0])
	}
	cat := res.Catalog
	c := &slugCatalogue{
		cat:    cat,
		ix:     NewSeriesAuthorIndex(cat),
		works:  make(map[string]*model.Work, len(cat.Works)),
		series: make(map[string]*model.Series, len(cat.Series)),
		names:  make(map[string]string, len(cat.People)),
	}
	for _, w := range cat.Works {
		c.works[w.ID] = w
	}
	for _, s := range cat.Series {
		c.series[s.ID] = s
	}
	for _, p := range cat.People {
		c.names[p.ID] = p.Name
	}
	return c
}

// rowForWork is the row an import of w states: its author credits at their
// person slugs, its title and subtitle, its first stated publisher and the
// WORK's own language. The work's language, not the series' derived one, so the
// language closure is really asked: a row in the series' language by
// construction would pass it whatever the closure did.
func (c *slugCatalogue) rowForWork(w *model.Work) *SeriesRow {
	credits := make([]string, 0, len(w.Authors))
	slugOf := make(map[string]string, len(w.Authors))
	for _, a := range w.Authors {
		credits = append(credits, c.names[a])
		slugOf[c.names[a]] = a
	}
	var publisher string
	for _, r := range w.Recordings {
		if r.Publisher != "" {
			publisher = r.Publisher
			break
		}
	}
	return SeriesRowFor(credits, []string{w.Title, w.Subtitle}, publisher, w.Language,
		func(name string) string { return slugOf[name] })
}

// reachesSeries reports whether a claim naming s by its stored name, from a row
// modeled on any one of its member works, resolves to s - an import of one of
// its books naming the series. The match is the first hit, else the last miss
// (for the failure message).
func (c *slugCatalogue) reachesSeries(s *model.Series) (SeriesMatch, bool) {
	stored := func(slug string) (string, bool) {
		if x := c.series[slug]; x != nil {
			return x.Name, true
		}
		return "", false
	}
	var m SeriesMatch
	for _, sw := range s.Works {
		w := c.works[sw.Work]
		if w == nil {
			continue
		}
		m = c.ix.Resolve(s.Name, c.cat.Redirects, stored, c.rowForWork(w))
		if m.Found && m.Slug == s.ID {
			return m, true
		}
	}
	return m, false
}
