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
func walkEntries(t *testing.T, family string, fn func(path, key string, entry map[string]any)) {
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
// Part of that chain is CLAIM-dependent - a serial volume whose title cannot
// tell it from its siblings sits on a "book-<position>" slug, reachable only by
// a row stating the position - so the recomputation reads each work's claim out
// of the series family (dataSeriesClaims) rather than pretending a claim-less
// row is the only kind there is.
//
// It deliberately does not demand that of every record: a work slug is composed,
// so a record can be legitimately off-chain (the catalogue holds one whose title
// was later corrected). Scoping the assertion to the characters a rule change
// moved is what keeps this a proof of the two migrations rather than a standing
// complaint about composed identity in general.
func TestDataWorkAndSeriesSlugsAreCurrent(t *testing.T) {
	claims := dataSeriesClaims(t)
	walkEntries(t, "works", func(path, key string, e map[string]any) {
		title := str(e, "title")
		if !movedByTransliteration(title) && !movedByApostrophe(title) {
			return
		}
		author := ""
		if as, ok := e["authors"].([]any); ok && len(as) > 0 {
			author, _ = as[0].(string)
		}
		cands, _ := workCandidates(Slugify(cleanWorkTitle(title)),
			workAuthors{all: []string{author}, identity: []string{author}}, claims[key])
		for _, cand := range cands {
			if cand.slug == key {
				return
			}
		}
		t.Errorf("%s: work %q (title %q) is on no candidate of today's slug chain; "+
			"an import of this title would mint a duplicate work", path, key, title)
	})

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
		t.Errorf("%s: series %q (name %q) is on no candidate of today's slug chain; "+
			"an import naming this series would mint a duplicate", path, key, name)
	})
}
