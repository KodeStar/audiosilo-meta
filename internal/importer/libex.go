package importer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kodestar/audiosilo-meta/pkg/model"
	"golang.org/x/text/unicode/norm"
)

// libex.go maps a libex (libex.lostcartographer.xyz) Audible-metadata export
// into the same internal sourceBook the OpenAudible and Libation paths produce,
// so every mapping, dedup, person, and series rule is shared (see
// openaudible.go / mapping.go / importer.go).
//
// This is the NEW-IMPORT source: it creates works/recordings/people/series that
// are not in the catalogue yet. The ASIN-matched enrichment of records already
// here (enrich.go, reached with --enrich), and the bounded subset selection
// LICENSING.md's import posture requires, build on the same parse layer, which
// stays a plain []byte -> rows function with no I/O.
//
// RECOMMENDED INPUT, both modes: a PRE-FILTERED row set - what the libex-select
// tool emits (a series completion, a contributor's shelf), or rows filtered to
// the catalogue's ASINs at export time - not libex's raw 1.06M-row dump. For the
// CREATE mode that is the import posture itself: importing the dump would be
// mirroring it, which LICENSING.md refuses. For the ENRICHMENT mode the posture
// is satisfied (the run is bounded by our catalogue, not by the source's), and
// the remaining limit is purely mechanical: this parse layer is all-in-memory
// and slurps every row before the ASIN match discards the ones that do not
// apply, measured at roughly 6GB of live heap for the 1.06M-row dump. Feeding it
// the whole dump therefore works only on a machine sized for it - filtering
// first is cheaper for everyone.
//
// Only factual fields are read. Per LICENSING.md the row's description /
// summary / rating / copyright fields are never touched at all, and the row's
// retailer genre strings are mapped onto this project's own vocabulary in code
// (audiblegenres.go), never stored verbatim.
//
// The export shape (libex BookResponse) accepted here:
//
//	asin, title, subtitle, region, regions[], publisher, isbn, language
//	("english"), bookFormat ("unabridged"/"abridged"), releaseDate (an ISO
//	timestamp), imageUrl, lengthMinutes, authors[{name}], narrators[{name}],
//	genres[{asin,name,type}], series[{name,position}], and optional inline
//	chapters[{title, startOffsetMs, lengthMs}].
//
// The file itself may be a top-level JSON array of rows, NDJSON (one row per
// line), or a {"books":[...]} wrapper (parseLibex sniffs which).

// utf8BOM is stripped before sniffing: a Windows-side export tool can prepend
// it, and it would otherwise make every shape check fail on the first byte.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// RunLibex imports exportPath (a libex export) into opts.DataDir. Rows are
// normalized into the shared sourceBook, so behaviour is otherwise identical to
// Run / RunLibation. Parse-time warnings (a row with no usable ASIN or no
// marketplace, an invalid ISBN, an unusable cover URL) are prepended to the
// run's warnings so the caller prints them together - per row when creating, in
// aggregate in the catalogue-bounded modes (see libexParse.warningLines).
func RunLibex(exportPath string, opts Options) (Summary, error) {
	raw, err := os.ReadFile(exportPath)
	if err != nil {
		return Summary{}, fmt.Errorf("read %s: %w", exportPath, err)
	}
	parsed, err := parseLibex(raw)
	if err != nil {
		return Summary{}, err
	}
	sum, runErr := runBooks(parsed.books, sourceLibex, opts)
	// += rather than =: runBooks has its own parse-layer refusal (the shared
	// AI-credit gate), which is a no-op for libex because refuseLibexCredits has
	// already dropped those rows - but the two counts are of the same thing, and
	// clobbering would be a bug the day that stops being true.
	sum.SkippedRows += parsed.skipped
	sum.Warnings = append(parsed.warningLines(opts.Mode.boundedByCatalogue()), sum.Warnings...)
	return sum, runErr
}

// libexWarnClass buckets a parse-layer warning by the KIND of problem it
// reports, so an enrichment run over a large export can say "1234 rows skipped:
// no well-formed ASIN" instead of printing 1234 near-identical lines about rows
// it was never going to touch. The declaration order is the report order.
type libexWarnClass int

const (
	warnNoASIN libexWarnClass = iota
	warnUnknownRegion
	warnAINarrator
	warnJunkCredit
	warnListCredit
	warnPlaceholderCredit
	warnUnnamedCredit
	warnCoverNotHTTPS
	warnMalformedISBN
)

// aggregateForm is the class's one-line summary, taking the count of rows in the
// bucket. Kept beside the class list so adding a class without a summary form is
// a compile error, not a silently unreported bucket.
func (c libexWarnClass) aggregateForm(n int) string {
	switch c {
	case warnNoASIN:
		return fmt.Sprintf("%d rows skipped: no well-formed ASIN", n)
	case warnUnknownRegion:
		return fmt.Sprintf("%d rows skipped: region is not a known marketplace", n)
	case warnAINarrator:
		return fmt.Sprintf("%d rows skipped: a credited name is an AI voice or system", n)
	case warnJunkCredit:
		return fmt.Sprintf("%d rows skipped: a credited name is a platform account", n)
	case warnListCredit:
		return fmt.Sprintf("%d rows skipped: a credited name is a semicolon-joined list of people", n)
	case warnPlaceholderCredit:
		return fmt.Sprintf("%d rows skipped: a credited name is a cast placeholder", n)
	case warnUnnamedCredit:
		return fmt.Sprintf("%d rows skipped: a credited name does not identify a person", n)
	case warnCoverNotHTTPS:
		return fmt.Sprintf("%d cover URLs were not https; dropped", n)
	case warnMalformedISBN:
		return fmt.Sprintf("%d ISBNs were malformed; dropped", n)
	}
	return fmt.Sprintf("%d rows warned", n)
}

// libexWarning is one parse-layer warning: its class, the row it came from (the
// ASIN when the row has one, else its title), and the full per-row line.
type libexWarning struct {
	class libexWarnClass
	label string
	text  string
}

// libexParse is the outcome of decoding an export: the usable rows, the count of
// rows the parse layer refused, and every parse-layer warning.
type libexParse struct {
	books    []sourceBook
	skipped  int
	warnings []libexWarning
}

// add records a parse-layer warning. label names the row (its ASIN, else its
// title) so an aggregate report can still point at concrete examples.
func (lp *libexParse) add(class libexWarnClass, label, format string, args ...any) {
	lp.warnings = append(lp.warnings, libexWarning{class: class, label: label, text: fmt.Sprintf(format, args...)})
}

// rowWarn returns the per-row warning sink for one accepted row, bound to a
// class and the row's ASIN, so the deeper field parsers (covers, ISBNs) keep the
// plain warn-func shape every other parser uses.
func (lp *libexParse) rowWarn(class libexWarnClass, asin string) func(string, ...any) {
	return func(format string, args ...any) { lp.add(class, asin, format, args...) }
}

// maxWarnExamples caps how many rows an aggregated warning names. A handful is
// enough to go and look at the data; a full list would be the per-row output the
// aggregation exists to avoid.
const maxWarnExamples = 5

// withExamples appends a "(for example: a, b, c)" tail to an aggregated warning
// line, naming at most maxWarnExamples rows. Every aggregated warning the
// importer emits - the parse layer's per-class buckets and the recordings-only
// pass's unmatched rows - renders through here, so they read identically and a
// caller cannot forget the cap.
func withExamples(line string, examples []string) string {
	if len(examples) == 0 {
		return line
	}
	if len(examples) > maxWarnExamples {
		examples = examples[:maxWarnExamples]
	}
	return line + " (for example: " + strings.Join(examples, ", ") + ")"
}

// warningLines renders the parse-layer warnings for the caller to print. In
// create mode every row's own line is kept (a curated tranche is small, and the
// detail is what a contributor acts on). In the catalogue-bounded modes
// (Mode.boundedByCatalogue: enrichment and recordings-only) the input is an
// export whose rows are overwhelmingly irrelevant to this catalogue, so the
// lines are folded into one per class with a count and a few example rows -
// otherwise a full export buries the run's real output under six figures of
// warnings about rows it never touched.
func (lp libexParse) warningLines(aggregate bool) []string {
	if !aggregate {
		lines := make([]string, 0, len(lp.warnings))
		for _, w := range lp.warnings {
			lines = append(lines, "libex: "+w.text)
		}
		return lines
	}
	counts := map[libexWarnClass]int{}
	examples := map[libexWarnClass][]string{}
	var order []libexWarnClass
	for _, w := range lp.warnings {
		if counts[w.class] == 0 {
			order = append(order, w.class)
		}
		counts[w.class]++
		// Distinct labels only: one row can warn twice in a class (two malformed
		// ISBNs), and a class labelled by CREDIT rather than by row repeats a
		// spelling across rows by nature. Five copies of one example is no example
		// at all.
		if w.label != "" && len(examples[w.class]) < maxWarnExamples && !slices.Contains(examples[w.class], w.label) {
			examples[w.class] = append(examples[w.class], w.label)
		}
	}
	lines := make([]string, 0, len(order))
	for _, class := range order {
		lines = append(lines, withExamples("libex: "+class.aggregateForm(counts[class]), examples[class]))
	}
	return lines
}

// parseLibex decodes a libex export and converts every usable row into the
// OpenAudible-shaped sourceBook the shared pipeline consumes. A row is skipped
// here (with a warning) when it lacks either of the two things the parse layer
// can decide on its own: a well-formed ASIN (this source's identity and dedup
// key) or a marketplace the recording schema knows. Rows missing a title,
// author, narrator or language are left to addBook, which owns those rules for
// every source.
func parseLibex(data []byte) (libexParse, error) {
	entries, err := decodeLibexEntries(data)
	if err != nil {
		return libexParse{}, err
	}
	lp := libexParse{books: make([]sourceBook, 0, len(entries))}
	for _, e := range entries {
		asin := NormalizeASIN(e.str("asin"))
		if asin == "" {
			label := firstNonEmpty(e.str("title"), e.str("asin"), "(unknown row)")
			lp.skipped++
			lp.add(warnNoASIN, label, "row %q has no well-formed ASIN (%q); skipped", label, e.str("asin"))
			continue
		}
		// A row whose marketplace does not map would import as a work and a
		// recording carrying NO asin[] at all: unreachable by lookup and
		// invisible to the ASIN dedup, so a later row for the same book would
		// collide with it. Refusing the row here keeps the ASIN available for a
		// sibling row that does state a marketplace.
		region, rawRegion, ok := libexRegion(e)
		if !ok {
			lp.skipped++
			lp.add(warnUnknownRegion, asin, "%s: region %q is not a known marketplace; row skipped (an ASIN must be marketplace-scoped)", asin, rawRegion)
			continue
		}
		// The credit-side refusals: a synthetic narrator, and a credit naming
		// nobody this catalogue can identify (see refuseLibexCredits). Refusing
		// them HERE rather than in a planner is what makes both rules hold for
		// every libex mode - create, enrich and recordings-only alike - and keeps
		// them out of the run's ASIN accounting entirely. The credit lists are read
		// once and handed to libexToBook, which would otherwise re-read them.
		authors, narrators := libexNames(e["authors"]), libexNames(e["narrators"])
		if r, refused := refuseLibexCredits(authors, narrators); refused {
			lp.skipped++
			lp.add(r.class, r.label(asin), "%s: %s; row skipped", asin, r.detail)
			continue
		}
		// Only now are the names unescaped: the list refusal above reads the
		// SEPARATORS, and an entity reference is a place a ';' hides, so it has
		// to see the escaped spelling. See unescapeCredits.
		authors, narrators = unescapeCredits(authors), unescapeCredits(narrators)
		lp.books = append(lp.books, libexToBook(e, asin, region, authors, narrators, &lp))
	}
	return lp, nil
}

// decodeLibexEntries accepts the three shapes a libex export can arrive in: a
// top-level JSON array of rows, NDJSON (a stream of one row per line), or a
// wrapper object holding the array under one of wrapperKeys. The first
// non-whitespace byte distinguishes the array; '{' is decoded as a STREAM of
// top-level objects, which covers NDJSON and (for a single object) the wrapper
// and one-row-per-file cases alike.
//
// A LONE object is the ambiguous case, and the row's own "asin" key resolves it:
// with one, it is a row (even if it happens to carry a "library" field); without
// one, it can only be an envelope, so it goes through decodeEntries - which
// finds the array under a recognized key or fails loudly. Guessing "row" there
// silently imported zero books from a valid-looking file.
func decodeLibexEntries(data []byte) ([]rawBook, error) {
	trimmed := bytes.TrimLeft(bytes.TrimPrefix(data, utf8BOM), " \t\r\n")
	switch {
	case len(trimmed) == 0:
		return nil, errors.New("parse libex export: empty input")
	case trimmed[0] == '[':
		return decodeEntries(trimmed, "libex export")
	case trimmed[0] != '{':
		return nil, errors.New("parse libex export: expected a JSON array of rows, NDJSON, or a wrapper object holding an array")
	}

	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var objs []rawBook
	for {
		var obj map[string]any
		if err := dec.Decode(&obj); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("parse libex export: %w", err)
		}
		objs = append(objs, rawBook(obj))
	}
	if len(objs) == 1 {
		if _, isRow := objs[0]["asin"]; !isRow {
			return decodeEntries(trimmed, "libex export")
		}
	}
	return objs, nil
}

// libexToBook normalizes one libex row into a sourceBook, translating field
// names and shapes to the OpenAudible keys addBook understands and reading only
// the factual fields (description / summary / rating / copyright are never
// touched). asin, region, authors and narrators are already resolved by
// parseLibex (the credit lists because the credit-side refusals have to read
// them first). The credits,
// runtime, abridged flag, series claims, genre claims, ISBNs and chapters are
// parse-time facts carried as typed fields on the sourceBook, never smuggled
// through raw in another source's key shape. Its warnings go to the parse
// collector class-tagged, so a large enrichment run can report them in aggregate.
func libexToBook(e rawBook, asin, region string, authors, narrators []string, lp *libexParse) sourceBook {
	raw := rawBook{}
	sb := sourceBook{raw: raw}
	raw["asin"] = asin
	raw["region"] = region

	// title_short is the work title; title is the fuller "Title: Subtitle"
	// (mirrors the OpenAudible/Libation short/full split). The subtitle is never
	// emitted as work.subtitle: an Audible subtitle is usually marketing or a
	// series designation rather than the edition's own subtitle, so we do not
	// record it as a fact. It does participate in identity though - the composed
	// full title is what tells two same-titled volumes of a series apart, and it
	// therefore appears INSIDE work.title on a work that disambiguation renamed
	// (see resolveWorkTitles / getOrCreateWork).
	title := e.str("title")
	raw["title_short"] = title
	if sub := e.str("subtitle"); title != "" && sub != "" && !strings.Contains(title, sub) {
		raw["title"] = title + ": " + sub
	} else {
		raw["title"] = title
	}

	sb.authors = authors
	sb.narrators = narrators
	if lang := e.str("language"); lang != "" {
		raw["language"] = lang // a word ("english"); mapLanguage resolves it
	}
	if pub := e.str("publisher"); pub != "" {
		raw["publisher"] = pub
	}
	if rd := isoDatePart(e.str("releaseDate")); rd != "" {
		raw["release_date"] = rd
	}
	if img := e.str("imageUrl"); img != "" {
		if strings.HasPrefix(img, "https://") {
			raw["image_url"] = img
		} else {
			// The schema requires an https cover URL, so an http one cannot be
			// recorded - but dropping a stated fact silently hides that the row
			// HAD a cover.
			lp.add(warnCoverNotHTTPS, asin, "%s: cover URL %q is not https; dropped", asin, img)
		}
	}
	if mins, ok := coerceInt(e["lengthMinutes"]); ok && mins > 0 {
		sb.runtimeMin = int(mins)
	}
	// bookFormat states the edition when libex knows it; anything else leaves
	// abridged unknown for runBooks' title-marker derivation to seed.
	sb.abridged = libexAbridged(e.str("bookFormat"))
	sb.series = libexSeries(e["series"])
	sb.genres = libexGenreClaims(e["genres"])
	sb.isbns = libexISBNs(e["isbn"], asin, lp.rowWarn(warnMalformedISBN, asin))
	sb.chapters = e.chapters()
	return sb
}

// libexNames reads a credits array ([{name: "..."}]) as the list of names
// addBook consumes verbatim, deduplicating repeated names (libex rows can list a
// person twice). The names are NOT joined into a comma-separated string: a name
// that itself contains a comma ("Alexandre Dumas, pere") would be re-split into
// two people. A plain string array is accepted too, and a bare string falls back
// to the comma-split every source shares.
//
// Names come back in the source's own spelling on BOTH shapes: the array path
// never cleaned them, and the bare-string path must not either (splitRawNames,
// not SplitNames). Cleaning here would strip a trailing role qualifier before
// sourceCredits could read it, so a hand-made row using the joined form would
// lose every contributor role while the array form kept it - the same file,
// two different sets of facts. No row in the received dump uses the bare form,
// so this is a latent difference being closed, not one observed in the seed.
func libexNames(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return splitRawNames(coerceStr(v))
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(arr))
	for _, el := range arr {
		name := ""
		if m, isMap := el.(map[string]any); isMap {
			name = coerceStr(m["name"])
		} else {
			name = coerceStr(el)
		}
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// libexRegion resolves a row's marketplace: the "region" field, else the first
// entry of "regions". It returns the canonical code with ok=true, or the raw
// value with ok=false when it is not a known marketplace (mapRegion resolves the
// aliases, so an ISO-3166 "gb" reaches the "uk" marketplace).
func libexRegion(e rawBook) (region, rawRegion string, ok bool) {
	rawRegion = e.str("region")
	if rawRegion == "" {
		if arr, isArr := e["regions"].([]any); isArr {
			for _, el := range arr {
				if s := coerceStr(el); s != "" {
					rawRegion = s
					break
				}
			}
		}
	}
	if rawRegion == "" {
		return "", "", false
	}
	region, ok = mapRegion(rawRegion)
	return region, rawRegion, ok
}

// libexAbridged reads the tri-state abridged flag from a bookFormat value.
// "unabridged"/"abridged" are statements of fact; anything else (including an
// absent field) is unknown.
func libexAbridged(format string) *bool {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "unabridged":
		f := false
		return &f
	case "abridged":
		t := true
		return &t
	}
	return nil
}

// libexSeries turns a row's series array ([{name, position}]) into series refs.
// A position arrives as a string or a number and is validated by the shared
// makeSeriesRef; an entry with no name is skipped (the sourceBook invariant
// that every ref carries a non-empty name).
func libexSeries(v any) []seriesRef {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var refs []seriesRef
	for _, el := range arr {
		m, isMap := el.(map[string]any)
		if !isMap {
			continue
		}
		name := coerceStr(m["name"])
		if name == "" {
			continue
		}
		refs = append(refs, makeSeriesRef(name, coerceStr(m["position"])))
	}
	return refs
}

// libexGenreClaims lifts a row's genres array ([{asin, name, type}]) into raw
// genre claims (the row's "asin" on a genre entry is a browse-node id, not a
// product ASIN). The node type is deliberately not filtered: both Audible
// "Genres" and "Tags" nodes are eligible, and the mapping table decides which
// map onto the project vocabulary (see audiblegenres.go).
func libexGenreClaims(v any) []genreClaim {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var claims []genreClaim
	for _, el := range arr {
		switch x := el.(type) {
		case map[string]any:
			c := genreClaim{node: coerceStr(x["asin"]), name: coerceStr(x["name"])}
			if c.node == "" && c.name == "" {
				continue
			}
			claims = append(claims, c)
		default:
			if name := coerceStr(el); name != "" {
				claims = append(claims, genreClaim{name: name})
			}
		}
	}
	return claims
}

// libexISBNs reads a row's ISBN field (a single string, or an array of them),
// keeping only well-formed, deduplicated values in their normalized (hyphenless)
// form. A malformed value is dropped with a warning rather than emitted - an
// ISBN that fails the schema pattern would fail the post-import validation for
// the whole tree.
func libexISBNs(v any, asin string, warn func(string, ...any)) []string {
	var candidates []string
	if arr, ok := v.([]any); ok {
		for _, el := range arr {
			candidates = append(candidates, coerceStr(el))
		}
	} else if s := coerceStr(v); s != "" {
		candidates = []string{s}
	}
	seen := map[string]bool{}
	var out []string
	for _, c := range candidates {
		if strings.TrimSpace(c) == "" {
			continue
		}
		isbn, ok := NormalizeISBN(c)
		if !ok {
			warn("%s: ISBN %q is malformed; dropped", asin, c)
			continue
		}
		key := strings.ToUpper(isbn)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, isbn)
	}
	return out
}

// ---------------------------------------------------------------------------
// The credit-side row refusals
//
// Three rules refuse a libex row outright on the strength of who it credits: the
// AI-narration exclusion, the junk-credit exclusion and the unidentifiable-name
// exclusion below. All live
// at the PARSE layer so they hold for every libex mode - create, --enrich,
// --recordings-only - and all are applied through refuseLibexCredits, which the
// bounded-subset selector (selectLibexRow) calls too. A row the importer will
// refuse must never be selected into a tranche as if it were importable: it
// would inflate the selection report and, worse, claim a series position slot a
// genuinely importable sibling row could have completed.

// creditRefusal is one credit-side refusal, in the two vocabularies its two
// consumers need: the parse layer's warning class (plus the clause its per-row
// line prints) and the selector's exclusion reason. They travel on one value so
// a further credit rule is named for both consumers or for neither.
type creditRefusal struct {
	class  libexWarnClass
	reason string // the selector's exclusion reason (see reasonOrder)
	name   string // the credit that earned the refusal
	detail string // the middle clause of the parse layer's per-row line
}

// label is what an aggregated warning names as an example of this refusal. For
// the AI and junk-credit rules it is the row (its ASIN): the vocabulary is
// settled, and the row is
// what an operator goes and looks at. For the unidentifiable-name rule the NAME
// is the evidence - the fix is upstream in Slugify, not in any one row - so the
// line carries both, the same reason reportUnnamedCredits names its examples.
func (r creditRefusal) label(asin string) string {
	switch r.class {
	case warnUnnamedCredit, warnListCredit, warnPlaceholderCredit:
		return asin + " " + strconv.Quote(r.name)
	}
	return asin
}

// refuseLibexCredits applies the credit-side row refusals in order and reports
// the first one the row earns.
func refuseLibexCredits(authors, narrators []string) (creditRefusal, bool) {
	if role, name, why, isAI := firstAICredit(authors, narrators); isAI {
		return creditRefusal{
			class:  warnAINarrator,
			reason: reasonAINarrator,
			name:   name,
			detail: fmt.Sprintf("%s %q is %s", role, name, why),
		}, true
	}
	if name, junk := firstJunkCredit(authors, narrators); junk {
		return creditRefusal{
			class:  warnJunkCredit,
			reason: reasonJunkCredit,
			name:   name,
			detail: fmt.Sprintf("credit %q is a platform account, not a person", name),
		}, true
	}
	if name, isList := firstListCredit(authors, narrators); isList {
		return creditRefusal{
			class:  warnListCredit,
			reason: reasonListCredit,
			name:   name,
			detail: fmt.Sprintf("credit %q is a semicolon-joined list of people, not one person", name),
		}, true
	}
	if name, placeholder := firstPlaceholderCredit(authors, narrators); placeholder {
		return creditRefusal{
			class:  warnPlaceholderCredit,
			reason: reasonPlaceholder,
			name:   name,
			detail: fmt.Sprintf("credit %q is a cast placeholder, not a person", name),
		}, true
	}
	if name, unnamed := firstUnnamedCredit(authors, narrators); unnamed {
		return creditRefusal{
			class:  warnUnnamedCredit,
			reason: reasonUnnamedCredit,
			name:   name,
			detail: fmt.Sprintf("credit %q does not identify a person", name),
		}, true
	}
	return creditRefusal{}, false
}

// ---------------------------------------------------------------------------
// Unidentifiable-credit exclusion
//
// A person's identity in this catalogue is their SLUG, and Slugify keeps only
// ASCII letters and digits (accented Latin folds onto its base letter). A name
// written entirely in a script that has no such folding - Korean, Cyrillic,
// Chinese, Japanese, Greek, Arabic - therefore slugs away to nothing, and
// personSlug substitutes the shared catch-all "person" record for it.
//
// On a bulk libex seed that catch-all is not a gap, it is a falsehood at scale:
// the first wave's dry run produced 6,633 empty-slug warnings, which would have
// made ONE person record the credited author or narrator of thousands of
// unrelated books. Measured over the 142,550-row seed selection, 2,075 rows
// (1.5%) carry at least one such credit, across 417 distinct names - mostly
// Japanese and Arabic authors, several of them prolific (328 rows credit one
// Arabic author alone). Refusing the row is the honest outcome - the book is simply
// absent, which is true, rather than present with an invented shared author,
// which is not - and it is cheap to reverse: when Slugify learns to transliterate
// (or the model grows a non-slug identity), the refused rows can be re-imported
// from the same dump.
//
// The refusal is WHOLE-ROW and covers BOTH credit lists. A row is one book: its
// author list and its narrator list are equally load-bearing facts, and importing
// a book while silently dropping one of its narrators would record a production
// that never existed.
//
// DELIBERATELY LIBEX-ONLY. The other importers (openaudible.go, libation.go,
// audiosilobooks.go) read a USER's own library, where the catch-all conflation
// keeps today's behaviour: a user who owns a Korean audiobook wants it in their
// catalogue, the conflation is visible to them, and refusing their book to keep a
// shared database tidy would be the wrong trade. What to do for user libraries is
// a separate open decision; this rule takes no position on it.
//
// There is no SQL twin of this rule in scripts/libex-export-rows.sql, unlike the
// AI-narration vocabulary. Slugify's behaviour is Unicode folding in Go, not a
// name list, and re-spelling it in SQL would be a second implementation that
// could disagree with the first. The export SQL stays the AI rule's belt and
// braces only.

// firstUnnamedCredit reports whether ANY credit in the row's author or narrator
// list fails to resolve to a person identity of its own, naming the first one it
// finds (for the warning, in the source's own spelling).
func firstUnnamedCredit(authors, narrators []string) (name string, unnamed bool) {
	for _, list := range [][]string{authors, narrators} {
		for _, n := range list {
			if !creditIdentifies(n) {
				return n, true
			}
		}
	}
	return "", false
}

// creditIdentifies reports whether one source credit name resolves to a person
// of its own. The name goes through sourceCredits and personSlug - the exact
// pair the import itself uses - rather than straight to Slugify, so the judgment
// is made on the CLEANED name that would become the record: "Created by 田中"
// slugs to a non-empty "created-by" raw, and to nothing at all once the prefix
// credit is stripped, which is the form that matters.
//
// A name that is only whitespace identifies nobody but conflates nobody either:
// sourceCredits drops it outright, so no record is created and the row is not
// refused for it (an absent author is addBook's rule to enforce, for every
// source).
func creditIdentifies(name string) bool {
	for _, c := range sourceCredits([]string{name}, "", creditCensus{}) {
		if _, fellBack := personSlug(c.name); fellBack {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// AI-narration exclusion
//
// AI-narrated productions do not belong in this catalogue: the data model is
// about who narrated a book, and a synthetic voice is not a person - importing
// one mints a person record for a text-to-speech engine (the full-dump
// recordings-only run really did create eleven "ai-voice-*" people before this
// rule existed).
//
// libex carries an is_vvab ("virtual voice") flag, and scripts/libex-export-rows.sql
// filters on it - but the flag cannot be relied on. Measured over the full
// 1.13M-row dump: 145,558 books credit an AI voice, and is_vvab is FALSE on
// 145,550 of them and true on only 8. The evidence that a production is
// AI-narrated therefore lives in the narrator credit, not in the flag, which is
// why this rule exists at all.
//
// The vocabulary below is EVIDENCE-DRIVEN, measured over that dump, in the same
// spirit as mapping.go's roleQualifiers: every entry is a form the data actually
// contains, and nothing is a guess at what a retailer might emit. That restraint
// is load-bearing here - a substring search for "tts" or a bare "ai"/"ki" token
// matches Watts, Pitts, Ricketts, Ki Hong Lee and Ai-jen Poo, all real people.
// The four shapes below are the only ones that discriminate cleanly.
//
// KEEP IN STEP with the SQL-side list in scripts/libex-export-rows.sql, which
// applies the same four shapes at export time (belt and braces, so a future
// dump is filtered before it ever reaches this parser).

// aiNarratorNames are credits that are WHOLLY an AI-voice label, in every
// localization the dump carries. Matched as a whole name (foldCredit form),
// never as a substring. Counts are credits in the 1.13M-row dump.
var aiNarratorNames = map[string]bool{
	"virtual voice":    true, // 143,559 - English, and the overwhelming majority
	"voz virtual":      true, // 281 - Spanish
	"voix virtuelle":   true, // 171 - French
	"voce virtuale":    true, // 134 - Italian
	"voz sintetica":    true, // 10 - Spanish "synthetic voice" (diacritics folded)
	"voce artificiale": true, // 2 - Italian "artificial voice"
	"virtuelle stimme": true, // 1 - German
	"voz virual":       true, // 1 - a Spanish typo the dump really carries
	"digital voices":   true, // 1 - Loudly, an AI audio publisher
}

// aiNarratorPrefix is the one prefix family: Audible's "AI Voice <persona>"
// credits (236 distinct names, 1,139 credits), including the bare "AI Voice".
// A prefix rather than a set because the persona is free text. The boundary
// check after it is what keeps a hypothetical real name ("Ai Voicu") out.
const aiNarratorPrefix = "ai voice"

// aiNarratorSuffix is the one suffix family: Audible's authorized voice-replica
// program, which credits a synthetic clone of a real narrator as "<that
// narrator>'s voice replica" (2,203 credits, 81 distinct names, 2,202 books in
// the 1.13M-row dump - is_vvab is FALSE on every single one). The person named
// is real, but the credit is not them: it is a model of their voice, and minting
// a person record for it would put "Steve Stewart's Voice Replica" in the
// catalogue beside Steve Stewart.
//
// A suffix rather than a set because the cloned narrator is free text, and 79 of
// the 81 names in the dump end with the phrase. The other two - "Anne Lance
// (Authorized Voice Replica)" and "AI Voice Veda Skye (Authorized Voice
// Replica)" - are already refused by aiNarratorMarkers and aiNarratorPrefix
// respectively, so the three shapes together cover the program exactly.
//
// The boundary check before it is what keeps a hypothetical real surname
// ("Replica") out: the phrase must start a word. The possessive that precedes it
// in the data ("...'s voice replica") ends in an apostrophe-s that foldCredit
// keeps, so the space before "voice" is the boundary that matches. Nothing in
// the dump ending in this phrase is a person: the near neighbours a looser
// "'s voice" rule would eat are "The Captain's Voice" (4 credits), "April's
// Voice" (2) and "Debra Shieber's Voice Talent" (1), all real credits.
const aiNarratorSuffix = "voice replica"

// aiNarratorMarkers are trailing parenthetical (or bracketed) markers that
// declare the credit synthetic: "Santiago (Voz de IA)", "Elise (AI)", "Mar Cabra
// (Réplica de voz autorizada)". Compared as whole markers against this set,
// because the same position also carries perfectly human qualifiers the dump
// shows - "(Skyboat Media)", "(TheVoiceOgre)", "(The Captain's Voice)".
var aiNarratorMarkers = map[string]bool{
	"voz de ia":                 true, // 500 credits, 33 names - Spanish "AI voice"
	"ai":                        true, // 7
	"replica de voz autorizada": true, // 3 - an authorized synthetic voice replica
	"authorized voice replica":  true, // 2 - the same, in English
	"virtual voice":             true, // 2
	"ki sprecher":               true, // 1 - German "AI narrator"
	"kokoro tts":                true, // 1 - a named TTS engine
}

// aiSystemTokens name a generative-AI SYSTEM rather than a person, and appear in
// the dump as AUTHORS: a book written by a model, credited to the product that
// wrote it. The voice vocabulary above cannot see them - "CLAUDE.AI" is not a
// narration credit and matches none of its three shapes - and gating narrators
// only let one through as an author in a seed wave, minting a person record for
// a language model.
//
// Matched as WORD TOKENS inside the folded credit (see namesAISystem), because
// the dump's spellings are compounds of a real name and a product name ("A.
// ChatGPT", "Sparky From ChatGPT", "Jim Mitchell and Google NotebookLM"), and a
// whole-name set would need one entry per compound.
//
// Every token is measured over the full 1.13M-row dump and every token is a
// PRODUCT name no human carries: across authors AND narrators the list matches 24
// distinct names, all of them AI systems, and zero people. Counts are books.
//
// What is deliberately NOT here matters as much:
//
//   - bare "ai" and bare "claude" - "Ai" is a published poet's mononym and Claude
//     is an ordinary given name (the dump has translators called Claude); only the
//     dotted product spelling "claude.ai" is unambiguous.
//   - bare "gpt" - it would take "Daddy GPT", "Mommy GPT" and "Parents GPT" with
//     it, which are person-shaped pen names rather than a product credited as an
//     author. The versioned product spellings are unambiguous; the bare token is
//     not.
//   - "gemini" and bare "grok" - neither is attested in the dump as a credit at
//     all ("Grok Ai" is), and both are ordinary words a pen name could use.
var aiSystemTokens = []string{
	"chatgpt",    // 1,187 - by far the commonest, incl. "ChatGPT ChatGPT" (1,140)
	"chat gpt",   // 12 - the spaced spelling
	"gpt-5",      // 28
	"gpt-4",      // 5
	"copilot",    // 3 - "Copilot AI", "Copilot Artifical Intelligence" (sic)
	"openai",     // 2
	"open ai",    // 1 - the spaced spelling
	"notebooklm", // 2
	"grok ai",    // 2
	"claude.ai",  // 1
}

// firstAICredit reports whether ANY credit in the row's author or narrator list
// is an AI, naming the first one it finds, which list it came from, and why (both
// for the warning).
//
// ANY rather than "all of them", deliberately. The two rules are
// indistinguishable on the measured narration data - of the 145,558 AI-narrated
// books in the dump, ZERO credit a human alongside the synthetic voice - and on
// the authorship side they diverge on exactly one book, "Jim Mitchell and Google
// NotebookLM". The failure asymmetry picks the rule: refusing a row costs nothing
// (it is counted, reported, and can be added by hand), while admitting one mints
// a person record for a TTS engine or a language model, which is exactly what
// this rule exists to prevent.
//
// BOTH credit lists, and both vocabularies against each. A synthetic voice
// credited as the author is no more a person than one credited as the narrator,
// and the dump proves the model is not tidy about which column its non-people
// land in.
func firstAICredit(authors, narrators []string) (role, name, why string, isAI bool) {
	for _, list := range []struct {
		role  string
		names []string
	}{{"narrator", narrators}, {"author", authors}} {
		for _, n := range list.names {
			if why, ok := aiCreditReason(n); ok {
				return list.role, n, why, true
			}
			// A credit qualifier can hide the marker from the trailing-paren test
			// ("Elise (AI) - narrator"), so the cleaned form gets a look too - that is
			// the name that would become the person record. No such form is in the
			// dump today; this is the cheap guard against the day one appears.
			if cleaned := CleanCreditName(n); cleaned != n {
				if why, ok := aiCreditReason(cleaned); ok {
					return list.role, n, why, true
				}
			}
		}
	}
	return "", "", "", false
}

// aiCreditReason judges one credit name against both AI vocabularies and reports
// which one refused it, phrased for the warning line.
func aiCreditReason(name string) (why string, isAI bool) {
	switch {
	case isAINarratorName(name):
		return "an AI voice", true
	case namesAISystem(name):
		return "an AI system, not a person", true
	}
	return "", false
}

// namesAISystem reports whether the credit contains an aiSystemTokens token as a
// whole word. Token containment rather than a whole-name match because the
// product name arrives welded to human text ("Sparky From ChatGPT"); the word
// boundaries are what keep it off a name that merely spells a token inside a
// longer word.
func namesAISystem(name string) bool {
	canon := foldCredit(name)
	if canon == "" {
		return false
	}
	for _, token := range aiSystemTokens {
		if containsToken(canon, token) {
			return true
		}
	}
	return false
}

// containsToken reports whether token occurs in s bounded by non-word runes on
// both sides, where a word rune is a letter or a digit - the same boundary test
// the AI-voice prefix and suffix families use (continuesWord/endsWord), applied
// to both ends.
func containsToken(s, token string) bool {
	for at := 0; ; {
		i := strings.Index(s[at:], token)
		if i < 0 {
			return false
		}
		start := at + i
		end := start + len(token)
		if !endsWord(s[:start]) && !continuesWord(s[end:]) {
			return true
		}
		at = start + 1
	}
}

// isAINarratorName applies the three measured VOICE shapes to one credit name.
func isAINarratorName(name string) bool {
	canon := foldCredit(name)
	if canon == "" {
		return false
	}
	if aiNarratorNames[canon] {
		return true
	}
	if rest, found := strings.CutPrefix(canon, aiNarratorPrefix); found && !continuesWord(rest) {
		return true
	}
	if rest, found := strings.CutSuffix(canon, aiNarratorSuffix); found && !endsWord(rest) {
		return true
	}
	if m := trailingParenRE.FindStringSubmatchIndex(name); m != nil {
		return aiNarratorMarkers[foldCredit(name[m[2]:m[3]])]
	}
	return false
}

// continuesWord reports whether s picks up mid-word - whether the text right
// after the "ai voice" prefix is more of the same word rather than a boundary.
// It is what makes the prefix a WORD prefix: "AI Voice Nina" matches the family,
// a hypothetical "Ai Voicu" does not. An empty remainder is the bare "AI Voice"
// credit, which is a boundary.
func continuesWord(s string) bool {
	if s == "" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// endsWord is continuesWord's mirror for the suffix family: whether the text
// right BEFORE "voice replica" ends mid-word rather than at a boundary. It is
// what makes the suffix a WORD suffix: "Steve Stewart's voice replica" matches
// the family, a hypothetical surname "Replica" preceded by nothing but letters
// ("Ivoice Replica") does not. An empty prefix is the bare "voice replica"
// credit, which is a boundary.
func endsWord(s string) bool {
	if s == "" {
		return false
	}
	r, _ := utf8.DecodeLastRuneInString(s)
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// ---------------------------------------------------------------------------
// List-credit exclusion
//
// A credit name containing a SEMICOLON is not a person, it is a LIST of people
// that the source (or a step before it) joined into one field. Nothing else in
// the credit pipeline can see that: the name slugs cleanly, it names no AI, and
// it identifies "somebody", so it was imported as one person - a Perry Rhodan
// row joined FIFTEEN authors with semicolons and minted a single person record
// whose 100-character slug was cut mid-word.
//
// Splitting the list instead was considered and rejected. The comma-split every
// source shares is deliberately not applied to a structured credit array
// (libexNames: "Alexandre Dumas, pere" is one person), and a row that arrives
// this mangled has already lost the guarantee that its OTHER fields are sound -
// the same Perry Rhodan block carries garbled titles and series claims. Refusing
// the row is the honest outcome, on exactly the terms the AI and
// unidentifiable-name rules use: the book is absent, which is true, rather than
// present with an invented author, which is not.
//
// The separator is a single character rather than a vocabulary, but it is NOT a
// bare substring test, and the difference is measured. The full dump carries 17
// distinct credit names containing ';' and they split cleanly in two:
//
//	10 are real lists   "Roman; Thomas; Michael G.; Dieter; Ulf; Olaf; Kai;
//	                     Madeleine Schleifer; Frick; Rosenberg; Bohn; ..."
//	                    (the 15-author Perry Rhodan credit), "Adrienne Fleming;
//	                    Tor Thom", "Nick; Colleen Sampson; Delany", ...
//	 7 are HTML ENTITIES "Erika B&aacute;lint", "Leo Kni&#382;ka", "Maxine
//	                    Mitchell &amp; Jason Clarke" - real people whose names
//	                    reached the dump HTML-escaped, where the ';' merely
//	                    terminates the entity.
//
// So the entity references are removed before the test. Escaped names are a
// separate defect (they import under a mangled slug), and refusing them here
// would hide it behind a rule about something else.
//
// Two of the ten are a person with a STRAY semicolon rather than a list
// ("Morton Levell;", "Dr. Mohamed E;-Reedy", a typo for El-Reedy). Both are
// refused too, deliberately: each is corrupt as it stands and would mint a
// bogus person record, which is the outcome this rule exists to prevent.
//
// ';' is deliberately the ONLY separator refused. '&', '/' and ' und ' all
// occur inside real names and real duo credits.
var htmlEntityRE = regexp.MustCompile(`&(#[0-9]+|#x[0-9a-fA-F]+|[a-zA-Z][a-zA-Z0-9]*);`)

// unescapeCredits resolves HTML entity references in a row's credit names.
//
// Seven of the dump's credit names reach it HTML-escaped - "Erika B&aacute;lint",
// "Leo Kni&#382;ka", "Maxine Mitchell &amp; Jason Clarke" - and every one is a
// real person whose record was minted under a slug spelling the ENTITY
// ("erika-b-aacute-lint"). That is not a merge or a refusal, it is a name
// written wrong, so the fix is to read the escape rather than to drop the row.
//
// It runs AFTER the credit-side refusals, which is load-bearing in one
// direction only: the list rule reads the ';' an entity reference ends with, so
// it must see the escaped spelling to tell that ';' from a list separator.
// Nothing downstream depends on the escaped form.
//
// html.UnescapeString leaves an unknown reference exactly as it found it, so a
// name that merely contains an ampersand ("Marley &Me Productions") is
// untouched.
func unescapeCredits(names []string) []string {
	for i, n := range names {
		if strings.ContainsRune(n, '&') {
			names[i] = html.UnescapeString(n)
		}
	}
	return names
}

// firstListCredit reports whether ANY credit in the row's author or narrator
// list is a semicolon-joined list of people, naming the first one it finds.
func firstListCredit(authors, narrators []string) (name string, isList bool) {
	for _, list := range [][]string{authors, narrators} {
		for _, n := range list {
			if !strings.Contains(n, ";") {
				continue
			}
			if strings.Contains(htmlEntityRE.ReplaceAllString(n, ""), ";") {
				return n, true
			}
		}
	}
	return "", false
}

// ---------------------------------------------------------------------------
// Placeholder-credit exclusion
//
// A credit can name neither a person nor a collective but a PLACEHOLDER: the
// retailer's "to be announced" standing in for a cast that had not been booked
// when the listing went up. Nothing else refuses them - they slug cleanly, they
// name no AI, and they are not platform accounts - so they imported as people,
// and "to be announced" became the credited narrator of 251 books.
//
// The line this vocabulary draws is between a placeholder and a COLLECTIVE
// credit. A collective names a real (if unnamed) party who really did the work,
// and the project keeps those - it now keeps them under ONE record per
// statement, whatever language the source stated it in (collective.go): "Full
// Cast", "Various", "Anonymous", "Uncredited" and the unknown-identity family
// ("Unknown", the bibliographic "N.N.", "auteur inconnu", "narratore
// sconosciuto") all import, folded onto their canonical record. A placeholder
// names nobody at all, not even a party, and it is the only one of the four
// kinds of nameless credit that is still refused.
//
// So this list is now exactly ONE family, and it is the one that clears the
// "cannot be a person, a group, or an unnamed party under any reading" bar: the
// TBD forms, which are an administrative state rather than a credit at all. The
// plural-cast forms this list used to carry ("various narrators", "narratori
// vari", "diverse sprecher", "elenco" and friends) were refusing rows for
// stating something true, and they moved to the normalization table.
//
// Deliberately NOT included, and each is a judgment call worth restating:
//
//   - every collective and unknown-identity form. See collective.go, which owns
//     that question now; nothing about a credit's language belongs here.
//   - "Test Narrator", "Test", "RocQET QA" and friends, for the reason
//     junkCreditNames already records: they read as placeholders but are not
//     provably accounts, and their measured neighbours are real people. (The
//     "Test" rows are fixture BOOKS, so the book is what is fake, not the
//     credit - a different rule's job if one is ever wanted.)
//
// Counts are books over the full dump.
var placeholderCreditNames = map[string]bool{
	// The TBD family: an administrative state, not a credit.
	"to be announced": true, // 251
	"to be confirmed": true, // 55
	"tbd":             true, // 20
	"tba":             true, // 10
	"tbc":             true, // 4
	"n/a":             true, // 1
}

// firstPlaceholderCredit reports whether ANY credit in the row's author or
// narrator list is a placeholder rather than a person, naming the first one it
// finds. Like every other credit-side refusal it is whole-row and reads both
// lists, and it compares the whole folded name - never a substring, so the real
// authors surnamed Cast are untouched.
func firstPlaceholderCredit(authors, narrators []string) (name string, placeholder bool) {
	for _, list := range [][]string{authors, narrators} {
		for _, n := range list {
			if placeholderCreditNames[foldCredit(n)] {
				return n, true
			}
		}
	}
	return "", false
}

// ---------------------------------------------------------------------------
// Junk-credit exclusion
//
// A credit can name neither a person nor a synthetic voice but a PLATFORM
// ACCOUNT - the publishing tool's own test harness, leaking a row into the
// public catalogue. The dump carries exactly one: "acx-dev+gamma4", the
// plus-tagged local part of an ACX developer mailbox, credited as the narrator
// of one book (B0CTW69SVQ, "Psion Gamma (Psion series # 2)", publisher "test
// company", 43 minutes for a full-length novel). It slugs cleanly, so
// firstUnnamedCredit does not catch it, and it is not synthetic, so the AI rule
// does not either - it simply is not a person, and importing it minted
// "acx-dev-gamma4" as a narrator of record.
//
// The vocabulary is EXACT NAMES, held to the same evidence bar as
// aiNarratorNames (which itself carries four count-1 entries): a name goes in
// only when it cannot be a person under any reading. A pattern rule was measured
// and rejected - "acx" matches this one narrator credit and zero author credits
// in 1.13M rows, so there is no acx-dev FAMILY to generalize over, and the
// email-plus-tag shape `^[\w.-]+\+[\w.-]+$` also matches "I+Everything", a real
// publisher name. Deliberately NOT included, though the same suspicion applies:
// "Test Narrator" (12 credits), "RocQET QA" (7), "Test" (3), "Test Test" (2),
// "Tom Test" (2), "Test Author" (1), "Scott Russell Test" (1). Those read as
// placeholders but are not provably accounts, and their neighbours in the same
// measurement are unmistakably real people - "Tim Sample" (10), "Dev J. Haldar"
// (19), "Kate Sample" (5), "Dev Joshi" (4) - so a token rule over test/dev/QA/
// sample would eat narrators who exist. If one of those names is ever confirmed
// junk it is a one-line addition here.
//
// Like the AI rule, the refusal is WHOLE-ROW and covers BOTH credit lists (a
// test account can be credited as the author just as easily), and it lives at
// the parse layer so every libex mode inherits it.
var junkCreditNames = map[string]bool{
	"acx-dev+gamma4": true, // 1 credit, 1 book - an ACX developer mailbox
}

// firstJunkCredit reports whether ANY credit in the row's author or narrator
// list is a known platform account, naming the first one it finds.
func firstJunkCredit(authors, narrators []string) (name string, junk bool) {
	for _, list := range [][]string{authors, narrators} {
		for _, n := range list {
			if junkCreditNames[foldCredit(n)] {
				return n, true
			}
		}
	}
	return "", false
}

// foldCredit is the canonical comparison form for a credit STRING - a name, a
// trailing marker, or a vocabulary key: lowercased, diacritics folded away, and collapsed to single
// spaces. Folding the diacritics is what lets one entry cover every spelling of
// "Voz sintética" / "Réplica de voz autorizada" a source may emit - precomposed
// (NFC), decomposed (NFD, which a Mac-side exporter really does produce), or
// typed without the accent - the same problem stripRoleQualifier solves for its
// role list. Note the SQL half of this rule (scripts/libex-export-rows.sql)
// compares the literal accented spellings instead, which is why the Go rule is
// the backstop rather than a duplicate. It is shared with the studio-tail rule
// (studiotail.go), which compares its own vocabulary in exactly this form.
func foldCredit(s string) string {
	decomposed := norm.NFD.String(strings.ToLower(strings.TrimSpace(s)))
	var b strings.Builder
	b.Grow(len(decomposed))
	for _, r := range decomposed {
		if model.IsCombiningMark(r) {
			continue
		}
		b.WriteRune(r)
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
