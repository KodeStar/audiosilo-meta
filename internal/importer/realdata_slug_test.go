package importer

// realdata_slug_test.go pins the data migration that shipped with the Slugify
// transliteration change: no committed record may still sit at an address the
// letter-DROPPING fold produced.
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

// TestDataWorkAndSeriesSlugsAreTransliterated is the half no rule covers. It
// asks only about records whose title or name carries a character this change
// moved - for every one of those, the id must sit on the candidate chain the
// importer walks TODAY, or the next wave mints a duplicate beside it.
//
// It deliberately does not demand that of every record. 105 works and 5 series
// are off-chain for an OLDER reason: their ids were minted before Slugify
// learned to strip apostrophes ("angela-s-ashes" for "Angela's Ashes",
// "ranger-s-apprentice" for "Ranger's Apprentice"), the same defect
// checkPersonSlug's comment records for madeleine-l-engle. That is a real
// duplicate-mint risk and its own migration; scoping this test to the characters
// THIS change moved is what keeps it a proof of this migration rather than a
// failing assertion about a different one.
func TestDataWorkAndSeriesSlugsAreTransliterated(t *testing.T) {
	walkEntries(t, "works", func(path, key string, e map[string]any) {
		title := str(e, "title")
		if !movedByTransliteration(title) {
			return
		}
		author := ""
		if as, ok := e["authors"].([]any); ok && len(as) > 0 {
			author, _ = as[0].(string)
		}
		for _, cand := range workCandidates(Slugify(cleanWorkTitle(title)), author) {
			if cand == key {
				return
			}
		}
		t.Errorf("%s: work %q (title %q) is on no candidate of today's slug chain; "+
			"an import of this title would mint a duplicate work", path, key, title)
	})

	walkEntries(t, "series", func(path, key string, e map[string]any) {
		name := str(e, "name")
		if !movedByTransliteration(name) {
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
