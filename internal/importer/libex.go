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
// here, and the bounded subset selection LICENSING.md's import posture requires,
// are separate tools - this file must stay usable by them (its parse layer is a
// plain []byte -> []sourceBook function with no I/O).
//
// The parse layer is deliberately all-in-memory: the intended input is the
// BOUNDED row set the libex-select tool emits (a series completion, a
// contributor's shelf), not libex's raw 1.13M-row dump. Feeding it the whole
// dump is out of contract for the import posture and for this parser's memory
// profile alike.
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
// marketplace, an invalid ISBN) are prepended to the run's warnings so the
// caller prints them together.
func RunLibex(exportPath string, opts Options) (Summary, error) {
	raw, err := os.ReadFile(exportPath)
	if err != nil {
		return Summary{}, fmt.Errorf("read %s: %w", exportPath, err)
	}
	books, warnings, err := parseLibex(raw)
	if err != nil {
		return Summary{}, err
	}
	sum, runErr := runBooks(books, sourceLibex, opts)
	sum.Warnings = append(warnings, sum.Warnings...)
	return sum, runErr
}

// parseLibex decodes a libex export and converts every usable row into the
// OpenAudible-shaped sourceBook the shared pipeline consumes. A row is skipped
// here (with a warning) when it lacks either of the two things the parse layer
// can decide on its own: a well-formed ASIN (this source's identity and dedup
// key) or a marketplace the recording schema knows. Rows missing a title,
// author, narrator or language are left to addBook, which owns those rules for
// every source.
func parseLibex(data []byte) ([]sourceBook, []string, error) {
	entries, err := decodeLibexEntries(data)
	if err != nil {
		return nil, nil, err
	}
	books := make([]sourceBook, 0, len(entries))
	var warnings []string
	warn := func(format string, args ...any) {
		warnings = append(warnings, "libex: "+fmt.Sprintf(format, args...))
	}
	for _, e := range entries {
		asin := NormalizeASIN(e.str("asin"))
		if asin == "" {
			label := firstNonEmpty(e.str("title"), e.str("asin"), "(unknown row)")
			warn("row %q has no well-formed ASIN (%q); skipped", label, e.str("asin"))
			continue
		}
		// A row whose marketplace does not map would import as a work and a
		// recording carrying NO asin[] at all: unreachable by lookup and
		// invisible to the ASIN dedup, so a later row for the same book would
		// collide with it. Refusing the row here keeps the ASIN available for a
		// sibling row that does state a marketplace.
		region, rawRegion, ok := libexRegion(e)
		if !ok {
			warn("%s: region %q is not a known marketplace; row skipped (an ASIN must be marketplace-scoped)", asin, rawRegion)
			continue
		}
		books = append(books, libexToBook(e, asin, region, warn))
	}
	return books, warnings, nil
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
// through raw in another source's key shape.
func libexToBook(e rawBook, asin, region string, warn func(string, ...any)) sourceBook {
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
			warn("%s: cover URL %q is not https; dropped", asin, img)
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
	sb.isbns = libexISBNs(e["isbn"], asin, warn)
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
