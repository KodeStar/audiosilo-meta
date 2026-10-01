package serve

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// The LANGUAGE FILTER (?lang=, and the {lang} segment of /abs/{lang}/search): a
// reader who only wants books in German asks for `lang=de`, or `lang=de,en` for
// two. It narrows the list surfaces - the combined and work/series searches with
// both of their boosts, works/latest and the coverage browser - and nothing else:
// a record page, a lookup or a feed names its records outright, and hiding one
// there would be a 404 for a book the catalogue holds.
//
// MATCHING IS ON THE PRIMARY SUBTAG (RFC 4647 basic filtering): `de` matches a
// work tagged `de` and one tagged `de-at`. Every tag in today's artifact is a bare
// primary subtag, but the schema allows a regional one, and a reader asking for
// German means Austrian German too - model.PrimarySubtag is the comparison every
// language rule in this project makes. A filter item that is itself regional
// (`de-at`) is reduced to its primary subtag for the same reason: the catalogue
// does not state regions consistently enough for a narrower filter to mean
// anything but "fewer of the books you asked for".
//
// AN UNKNOWN SIDE IS NEVER JUDGED (model.SameLanguage's rule): a series whose
// members tie derives no language, and a person has none, so both pass every
// filter. Hiding a tied series from a German reader would hide the very series
// that holds German volumes.
//
// The filter is a plain predicate on columns the v7 artifact already carries -
// search_fts.language (UNINDEXED: a column read per matched row) and
// works.language (idx_works_language) - so there is no artifact change and no
// SchemaVersion question. Measured on the 281k-work artifact (see the internal/serve
// section of CLAUDE.md), and an absent filter emits exactly the SQL it always did.

// maxLangFilter bounds how many languages one request may name. Each is one more
// OR arm evaluated per matched row; the catalogue holds 17 languages, and a reader
// who wants more than 8 of them wants no filter.
const maxLangFilter = 8

// langTagRE is the schema's language tag pattern (common.schema.json
// #/$defs/language), so a filter item is spelled exactly as a record's tag is.
var langTagRE = regexp.MustCompile(`^[a-z]{2,3}(-[a-z0-9]{2,8})*$`)

// langFilter is a parsed language filter: primary subtags, deduplicated and
// sorted, so one set of languages has one spelling (and one SQL text). nil is no
// filter at all, which is what every caller passes when the request named none.
type langFilter []string

// parseLangFilter reads a ?lang= value (or an /abs/{lang}/search segment): a
// comma-separated list, each item trimmed and lowercased, matched against the
// schema's tag pattern and reduced to its primary subtag. Empty items are skipped,
// so an absent or empty value is no filter; any item that is not a tag is an
// error naming it, because a typo that silently filtered to nothing would read as
// "the catalogue holds no such books". A language the catalogue does not hold is
// NOT an error - it is a valid tag that filters to nothing.
func parseLangFilter(raw string) (langFilter, error) {
	var out langFilter
	for _, item := range strings.Split(raw, ",") {
		tag := strings.ToLower(strings.TrimSpace(item))
		if tag == "" {
			continue
		}
		if !langTagRE.MatchString(tag) {
			return nil, fmt.Errorf("lang: %q is not a language tag (expected a code like de or pt-br)", strings.TrimSpace(item))
		}
		out = append(out, model.PrimarySubtag(tag))
	}
	slices.Sort(out)
	out = slices.Compact(out)
	if len(out) > maxLangFilter {
		return nil, fmt.Errorf("lang: at most %d languages, got %d", maxLangFilter, len(out))
	}
	return out, nil
}

// predicate renders the filter as SQL over col, with its arguments in order: per
// language `(col = ? OR (col > ? AND col < ?))`, OR-joined and parenthesized. The
// two bounds are the tag followed by '-' and by '.', the next byte up, so the
// range holds exactly the regional variants of that primary subtag ("de-at",
// "de-ch") and never another language ("del", "dex" sort past '.').
//
// It is ONE spelling for both columns on purpose, and it is the RANGE form rather
// than `col LIKE 'de-%'` because the works-table form must stay index-friendly:
// LIKE defeats idx_works_language (measured: works/latest de 50ms against 20ms for
// the range), while the equality and the range each SEARCH the index. On the FTS
// column the two forms cost the same - nothing there is indexed either way.
//
// unknownPasses adds the `col = ”` arm the FTS column needs: a person row and a
// tied series row carry ” there, and an unknown side is never judged. The works
// table needs no such arm - works.language is NOT NULL and schema-patterned.
func (f langFilter) predicate(col string, unknownPasses bool) (string, []any) {
	arms := make([]string, 0, len(f)+1)
	args := make([]any, 0, 3*len(f))
	if unknownPasses {
		arms = append(arms, col+" = ''")
	}
	for _, lang := range f {
		arms = append(arms, "("+col+" = ? OR ("+col+" > ? AND "+col+" < ?))")
		args = append(args, lang, lang+"-", lang+".")
	}
	return "(" + strings.Join(arms, " OR ") + ")", args
}

// worksPredicate is predicate over a works-table language column, in the form
// the planner should read it: through idx_works_language for a filter that
// selects a MINORITY of the catalogue, and with the index switched off (`+col`,
// SQLite's unary-plus hint) for one that selects most of it. A built artifact
// carries no sqlite_stat1, so the planner always takes the index when it can -
// and walking an index over most of the table, then reading every row it names,
// costs more than reading the table once. Measured over the 281k-work artifact
// (works/latest's phase one / the coverage count): de (7.6%) 18 / 24ms through
// the index against 49ms scanned; en (87%) 135 / 275ms through the index against
// 92 / 197ms scanned - the unfiltered 88 / 200ms. The crossover sits a little
// under half the catalogue, so the rule is "more than half": the share is read
// off the stats census the snapshot already holds, matched by primary subtag as
// the predicate matches.
func (s *snapshot) worksPredicate(f langFilter, col string) (string, []any) {
	if 2*s.worksInFilter(f) > s.stats.Works {
		col = "+" + col
	}
	return f.predicate(col, false)
}

// worksInFilter counts the works f admits, off the stats languages census.
func (s *snapshot) worksInFilter(f langFilter) int {
	n := 0
	for _, lc := range s.stats.Languages {
		if _, found := slices.BinarySearch(f, model.PrimarySubtag(lc.Language)); found {
			n += lc.Works
		}
	}
	return n
}

// liveLang is THE version gate for the filter, asked by every surface it narrows
// and nowhere else: below languagesSchemaVersion the filter has been parsed (so
// garbage is still a 400) and is then IGNORED. An older search_fts has no language
// column to filter on, and dropping the filter is the "degrade to no data"
// every other languages read already does - the reader gets the unfiltered page
// the server could always serve, never a 500.
func (s *snapshot) liveLang(f langFilter) langFilter {
	if s.schemaVersion < languagesSchemaVersion || len(f) == 0 {
		return nil
	}
	return f
}

// worksInLanguagesSQL keeps the ids of a set that a language predicate admits,
// over the works primary key. It is what the search boosts are filtered with:
// they resolve works OUTSIDE the FTS query, so its predicate never saw them.
func worksInLanguagesSQL(ph, pred string) string {
	return `SELECT id FROM works WHERE id IN (` + ph + `) AND ` + pred
}

// worksInLanguages filters ids to the works f admits, preserving their order. It
// costs one primary-key read per id and runs only when a boost fired under a
// live filter - a handful of ids on a rare path.
func (s *snapshot) worksInLanguages(ids []string, f langFilter) ([]string, error) {
	if f == nil || len(ids) == 0 {
		return ids, nil
	}
	pred, predArgs := f.predicate("language", false)
	keep := map[string]bool{}
	err := eachChunk(ids, func(ph string, args []any) error {
		rows, err := s.db.Query(worksInLanguagesSQL(ph, pred), append(args, predArgs...)...)
		if err != nil {
			return err
		}
		kept, err := scanIDs(rows)
		for _, id := range kept {
			keep[id] = true
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if keep[id] {
			out = append(out, id)
		}
	}
	return out, nil
}
