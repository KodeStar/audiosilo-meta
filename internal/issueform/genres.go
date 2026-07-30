package issueform

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	meta "github.com/kodestar/audiosilo-meta"
)

// genreVocabURL is where a contributor reads the controlled vocabulary. It is
// named in the invalid-genre message so a rejected submission tells the
// submitter exactly which list their value has to come from.
const genreVocabURL = "https://github.com/kodestar/audiosilo-meta/blob/main/schema/common.schema.json"

// genreVocabulary is the controlled genre vocabulary, read from the EMBEDDED
// schema (common.schema.json #/$defs/genre) rather than a copy of it, so a
// schema amendment is picked up here with no second list to update. A parse
// failure is a build-time programming error (the schema is embedded and covered
// by TestGenreVocabulary), so it panics rather than silently rejecting every
// genre a submitter names.
var genreVocabulary = sync.OnceValue(func() map[string]bool {
	set, err := loadGenreVocabulary()
	if err != nil {
		panic(fmt.Sprintf("issueform: embedded genre vocabulary is unusable: %v", err))
	}
	return set
})

// loadGenreVocabulary parses the genre enum out of the embedded schema. Split
// out from genreVocabulary so a test can assert the parse without a panic.
func loadGenreVocabulary() (map[string]bool, error) {
	raw, err := meta.SchemaFS.ReadFile("schema/common.schema.json")
	if err != nil {
		return nil, err
	}
	var doc struct {
		Defs struct {
			Genre struct {
				Enum []string `json:"enum"`
			} `json:"genre"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	if len(doc.Defs.Genre.Enum) == 0 {
		return nil, fmt.Errorf("schema/common.schema.json #/$defs/genre has an empty enum")
	}
	set := make(map[string]bool, len(doc.Defs.Genre.Enum))
	for _, g := range doc.Defs.Genre.Enum {
		set[g] = true
	}
	return set, nil
}

// parseGenres turns the form's comma-separated (or one-per-line) Genres field
// into the work record's genres list: trimmed, lower-cased, deduplicated, and
// sorted ascending (checkGenresSorted pins that order).
//
// An entry outside the controlled vocabulary FAILS the submission rather than
// being dropped with a note: the vocabulary is a closed list this project owns,
// so an unrecognized value is either a typo or a genre we would want to discuss
// adding - both of which a contributor should see, not have silently discarded.
// ok is false when the submission has been failed; an empty/absent field is
// (nil, true), so no genres key is emitted.
func (c *composer) parseGenres(block string) ([]string, bool) {
	seen := map[string]bool{}
	var out []string
	for _, raw := range splitList(block) {
		g := strings.ToLower(strings.TrimSpace(raw))
		if !genreVocabulary()[g] {
			c.fail(StatusInvalid, "genre %q is not in the controlled vocabulary - pick from the genre list in %s", raw, genreVocabURL)
			return nil, false
		}
		if seen[g] {
			continue
		}
		seen[g] = true
		out = append(out, g)
	}
	sort.Strings(out)
	return out, true
}
