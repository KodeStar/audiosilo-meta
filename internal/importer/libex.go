package importer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
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
// aggregate when enriching (see libexParse.warningLines).
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
	sum.SkippedRows = parsed.skipped
	sum.Warnings = append(parsed.warningLines(opts.Enrich), sum.Warnings...)
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

// maxWarnExamples caps how many rows an aggregated class names. A handful is
// enough to go and look at the data; a full list would be the per-row output the
// aggregation exists to avoid.
const maxWarnExamples = 5

// warningLines renders the parse-layer warnings for the caller to print. In
// create mode every row's own line is kept (a curated tranche is small, and the
// detail is what a contributor acts on). In enrichment mode the input is an
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
		if w.label != "" && len(examples[w.class]) < maxWarnExamples {
			examples[w.class] = append(examples[w.class], w.label)
		}
	}
	lines := make([]string, 0, len(order))
	for _, class := range order {
		line := "libex: " + class.aggregateForm(counts[class])
		if ex := examples[class]; len(ex) > 0 {
			line += " (for example: " + strings.Join(ex, ", ") + ")"
		}
		lines = append(lines, line)
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
		lp.books = append(lp.books, libexToBook(e, asin, region, &lp))
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
// touched). asin and region are already normalized by parseLibex. The credits,
// runtime, abridged flag, series claims, genre claims, ISBNs and chapters are
// parse-time facts carried as typed fields on the sourceBook, never smuggled
// through raw in another source's key shape. Its warnings go to the parse
// collector class-tagged, so a large enrichment run can report them in aggregate.
func libexToBook(e rawBook, asin, region string, lp *libexParse) sourceBook {
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

	sb.authors = libexNames(e["authors"])
	sb.narrators = libexNames(e["narrators"])
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
func libexNames(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return SplitNames(coerceStr(v))
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
