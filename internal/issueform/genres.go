package issueform

import (
	"sort"
	"strings"
	"sync"

	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// genreVocabulary is the controlled genre vocabulary: the item enum of the work
// schema's genres field, read through recordFields - the one reader of the
// embedded schemas here - so a schema amendment is picked up with no second list
// to update.
var genreVocabulary = sync.OnceValue(func() map[string]bool {
	return importer.ToSet(recordFields()[model.KindWork]["genres"].ItemEnum)
})

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
			c.fail(StatusInvalid, "genre %q is not in the controlled vocabulary - pick from the genre list in %s", raw, schemaVocabURL)
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
