package query

import (
	"context"
	"fmt"
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
// SchemaVersion question. Measured on the 281k-work artifact (see the API server
// section of CLAUDE.md), and an absent filter emits exactly the SQL it always did.

// maxLangFilter bounds how many languages one request may name. Each is one more
// OR arm evaluated per matched row; the catalogue holds 17 languages, and a reader
// who wants more than 8 of them wants no filter.
const maxLangFilter = 8

// maxLangItems bounds the RAW items a value may carry before any is validated.
// The 8-language cap is taken after deduplication, so without this a request
// repeating `lang=de` a hundred thousand times (a megabyte of query string) ran
// the tag pattern a hundred thousand times to arrive at one language. It is wide
// enough for any honest spelling of eight languages.
const maxLangItems = 4 * maxLangFilter

// langFilter is a parsed language filter: primary subtags, deduplicated and
// sorted, so one set of languages has one spelling (and one SQL text). nil is no
// filter at all, which is what every caller passes when the request named none.
type langFilter []string

// parseLangFilter reads a ?lang= value (or an /abs/{lang}/search segment): a
// comma-separated list, each item trimmed and lowercased, matched against the
// schema's tag pattern (model.ValidLanguageTag, so a filter item is spelled
// exactly as a record's tag is) and reduced to its primary subtag. Empty items are skipped,
// so an absent or empty value is no filter; any item that is not a tag is an
// error naming it, because a typo that silently filtered to nothing would read as
// "the catalogue holds no such books". A language the catalogue does not hold is
// NOT an error - it is a valid tag that filters to nothing.
func parseLangFilter(raw string) (langFilter, error) {
	if raw == "" {
		return nil, nil
	}
	items := strings.Split(raw, ",")
	if len(items) > maxLangItems {
		return nil, fmt.Errorf("lang: at most %d languages, got %d items", maxLangFilter, len(items))
	}
	var out langFilter
	for _, item := range items {
		tag := strings.ToLower(strings.TrimSpace(item))
		if tag == "" {
			continue
		}
		if !model.ValidLanguageTag(tag) {
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
// unknownPasses adds the empty-string arm the FTS column needs: a person row and
// a tied series row carry an empty language there, and an unknown side is never
// judged. The works table needs no such arm - works.language is NOT NULL and
// schema-patterned. (Spelled out in words because gofmt rewrites a pair of
// single quotes in a doc comment into a typographic quote.)
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
//
// A plain ANALYZE at build time would not retire this. sqlite_stat1 records an
// AVERAGE rows-per-key for the index, so en at 87% and de at 7.6% look identical
// to the planner; only sqlite_stat4's per-key samples capture the skew. The
// driver is compiled with SQLITE_ENABLE_STAT4, but writing those samples is an
// artifact change (internal/build, so a data release) to have the planner
// estimate what the census in memory already states exactly. The census-driven
// switch is the design, not a stopgap.
func (s *DB) worksPredicate(f langFilter, col string) (string, []any) {
	if 2*s.worksInFilter(f) > s.stats.Works {
		col = "+" + col
	}
	return f.predicate(col, false)
}

// has reports whether f admits a record tagged tag: the filter's one membership
// rule, primary subtag against primary subtag, which is what predicate spells in
// SQL. f is sorted (parseLangFilter), so it is a binary search.
func (f langFilter) has(tag string) bool {
	_, found := slices.BinarySearch(f, model.PrimarySubtag(tag))
	return found
}

// worksInFilter counts the works f admits, off the stats languages census.
func (s *DB) worksInFilter(f langFilter) int {
	n := 0
	for _, lc := range s.stats.Languages {
		if f.has(lc.Language) {
			n += lc.Works
		}
	}
	return n
}

// liveLang is THE version gate for the filter, applied once per request at the
// transport (langFilterFor, or the ABS language route) and nowhere else: every
// snapshot method below it takes a filter that is already live, nil meaning none.
// Below languagesSchemaVersion the filter has been parsed (so garbage is still a
// 400) and is then IGNORED. An older search_fts has no language column to filter
// on, and dropping the filter is the "degrade to no data" every other languages
// read already does - the reader gets the unfiltered page the server could always
// serve, never a 500. The people scope drops it too: a person has no language, so
// there is nothing to narrow by.
func (s *DB) liveLang(f langFilter, kind searchKind) langFilter {
	if s.schemaVersion < languagesSchemaVersion || len(f) == 0 || kind == kindPerson {
		return nil
	}
	return f
}

// langFilterFor is a request's ?lang= value as this snapshot can apply it to kind:
// parsed (an error is the caller's 400) and then put through liveLang. The caller
// must issue its query against the SAME snapshot, since the gate is that
// snapshot's schema_version.
func (s *DB) langFilterFor(raw string, kind searchKind) (langFilter, error) {
	f, err := parseLangFilter(raw)
	if err != nil {
		return nil, err
	}
	return s.liveLang(f, kind), nil
}

// worksInLanguagesSQL keeps the ids of a set that a language predicate admits,
// over the works primary key. It is what the search boosts are filtered with:
// they resolve works OUTSIDE the FTS query, so its predicate never saw them.
func worksInLanguagesSQL(ph, pred string) string {
	return `SELECT id FROM works WHERE id IN (` + ph + `) AND ` + pred
}

// worksInLanguages filters ids to the works f (already live) admits, preserving
// their order. It costs one primary-key read per id and runs only when a boost
// fired under a filter - a handful of ids on a rare path.
func (s *DB) worksInLanguages(ctx context.Context, ids []string, f langFilter) ([]string, error) {
	if f == nil || len(ids) == 0 {
		return ids, nil
	}
	// The plain predicate, not worksPredicate: this reads a handful of ids by
	// primary key, so which index the language test could use does not arise.
	pred, predArgs := f.predicate("language", false)
	keep := map[string]bool{}
	err := eachChunk(ids, func(ph string, args []any) error {
		rows, err := s.db.QueryContext(ctx, worksInLanguagesSQL(ph, pred), append(args, predArgs...)...)
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
