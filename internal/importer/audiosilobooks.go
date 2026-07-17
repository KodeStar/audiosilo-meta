package importer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// audiosilobooks.go ingests the site's "audiosilo-books" envelope - the bulk
// new-books download the /import page builds from an Audiobookshelf export (see
// site/src/lib/github-prefill.ts newBooksPayload and import-parse.ts). It is a
// self-identifying wrapper:
//
//	{"format":"audiosilo-books","version":1,"books":[ ...curated projection... ]}
//
// whose entries are already the flat, factual ParsedBook projection (title,
// subtitle, authors[], narrators[], series, series_position, asin, isbn,
// language, release_date, publisher, runtime_min, chapters (a COUNT), abridged,
// cover_url (https only, optional)).
// Each entry is normalized into the same internal sourceBook the OpenAudible and
// Libation paths produce, so it shares every mapping/dedup/series/person rule via
// runBooks. Consuming this envelope is a cross-repo contract: the site produces
// it (its books are ParsedBook-shaped) and this importer is the automated intake
// for an Audiobookshelf import issue.

// audiosiloBooksFormat is the envelope's format discriminator (mirrors the site's
// newBooksPayload for the Audiobookshelf source).
const audiosiloBooksFormat = "audiosilo-books"

// sourceAudiosiloBooks is the provenance stamped on every record from this source.
const sourceAudiosiloBooks = "audiosilo-books-import"

// audiosiloBooksEnvelope is the self-identifying wrapper the site emits. Books
// stay loosely typed (decoded with UseNumber) and are lifted through the same
// coercion helpers as every other source.
type audiosiloBooksEnvelope struct {
	Format  string           `json:"format"`
	Version int              `json:"version"`
	Books   []map[string]any `json:"books"`
}

// IsAudiosiloBooksEnvelope reports whether raw is a JSON object whose "format"
// field is the self-identifying audiosilo-books discriminator. It is cheap and
// safe on any input: a JSON array, a foreign object, or non-JSON garbage all
// return false. The intake bot uses it to trust the file over the submitter's
// dropdown selection (the envelope names its own format).
func IsAudiosiloBooksEnvelope(raw []byte) bool {
	var probe struct {
		Format string `json:"format"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	return probe.Format == audiosiloBooksFormat
}

// RunAudiosiloBooks imports an "audiosilo-books" envelope (exportPath) into
// opts.DataDir, reusing the shared pipeline. Behaviour is otherwise identical to
// Run / RunLibation.
func RunAudiosiloBooks(exportPath string, opts Options) (Summary, error) {
	raw, err := os.ReadFile(exportPath)
	if err != nil {
		return Summary{}, fmt.Errorf("read %s: %w", exportPath, err)
	}
	books, err := parseAudiosiloBooks(raw)
	if err != nil {
		return Summary{}, err
	}
	return runBooks(books, sourceAudiosiloBooks, opts)
}

// parseAudiosiloBooks decodes the envelope, validating its format/version marker
// (so a foreign file fails loud instead of misparsing), and converts each curated
// book projection into a sourceBook.
func parseAudiosiloBooks(data []byte) ([]sourceBook, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var env audiosiloBooksEnvelope
	if err := dec.Decode(&env); err != nil {
		return nil, fmt.Errorf("parse audiosilo-books export: %w", err)
	}
	if env.Format != audiosiloBooksFormat {
		return nil, fmt.Errorf("parse audiosilo-books export: not an %q envelope (format=%q)", audiosiloBooksFormat, env.Format)
	}
	if env.Version != 1 {
		return nil, fmt.Errorf("parse audiosilo-books export: unsupported version %d (expected 1)", env.Version)
	}
	books := make([]sourceBook, 0, len(env.Books))
	for _, e := range env.Books {
		books = append(books, audiosiloBookToBook(rawBook(e)))
	}
	return books, nil
}

// audiosiloBookToBook normalizes one curated projection entry into a sourceBook,
// translating its field names/shapes to the OpenAudible keys addBook understands.
// The projection carries no marketplace region (Audiobookshelf tracks none), so a
// present ASIN defaults to region us - mirroring the issue form's bare-ASIN
// handling - rather than dropping the recording's primary identity/dedup key. The
// projection's chapters field is a COUNT (not the OpenAudible chapters array), so
// it is intentionally not carried (buildChapters ignores a non-array anyway). A
// projection cover_url is routed into image_url so the shared pipeline records it.
func audiosiloBookToBook(e rawBook) sourceBook {
	raw := rawBook{}
	sb := sourceBook{raw: raw}

	if asin := e.str("asin"); asin != "" {
		raw["asin"] = asin
		raw["region"] = "us"
	}

	// title_short is the work title; title is the fuller "Title: Subtitle" used
	// only for slug disambiguation (mirrors the OpenAudible/Libation short/full
	// split). When the ABS export already concatenated the subtitle into the title
	// ("Fugitive Telemetry: Murderbot Diaries, Book 6" + subtitle "Murderbot
	// Diaries, Book 6"), title_short is the prefix so the work title is just
	// "Fugitive Telemetry"; the full title is kept as-is for disambiguation.
	//
	// Strip any trailing (Unabridged)/(Abridged) edition marker off BOTH strings
	// FIRST: a marker on the concatenated title ("...Book 6 (Unabridged)") would
	// otherwise defeat the ": "+subtitle CutSuffix and leak into the work slug.
	// We only clean here; runBooks is the SINGLE mechanism that derives the
	// abridged flag from a marker (before it mutates titles), so this path does
	// not touch sb.abridged (ABS entries carry their own explicit abridged field).
	title := cleanWorkTitle(e.str("title"))
	sub := cleanWorkTitle(e.str("subtitle"))
	titleShort := title
	if sub != "" {
		if prefix, cut := strings.CutSuffix(title, ": "+sub); cut && strings.TrimSpace(prefix) != "" {
			titleShort = strings.TrimSpace(prefix)
		}
	}
	raw["title_short"] = titleShort
	if title != "" && sub != "" && !strings.Contains(title, sub) {
		raw["title"] = title + ": " + sub
	} else {
		raw["title"] = title
	}

	if authors := joinNames(e["authors"]); authors != "" {
		raw["author"] = authors
	}
	if narrators := joinNames(e["narrators"]); narrators != "" {
		raw["narrated_by"] = narrators
	}
	if lang := e.str("language"); lang != "" {
		raw["language"] = lang // already an ISO code; mapLanguage accepts codes
	}
	if pub := e.str("publisher"); pub != "" {
		raw["publisher"] = pub
	}
	if rd := e.str("release_date"); rd != "" {
		raw["release_date"] = rd
	}
	// Route the projection's cover into image_url so the shared pipeline writes
	// rec.CoverURL (importer.go). Guarded https-only here too (the site already
	// guards it) - drop a non-https value rather than emit an invalid cover.
	if cover := e.str("cover_url"); strings.HasPrefix(cover, "https://") {
		raw["image_url"] = cover
	}
	if mins, ok := coerceInt(e["runtime_min"]); ok && mins > 0 {
		sb.runtimeMin = int(mins)
	}
	sb.abridged = coerceBoolPtr(e["abridged"])

	if name := e.str("series"); name != "" {
		rawSeq := e.str("series_position")
		pos, ok := NormalizeSequence(rawSeq)
		sb.series = []seriesRef{{name: name, seq: pos, seqOK: ok, rawSeq: rawSeq}}
	}
	return sb
}

// joinNames renders the projection's authors/narrators string array as the
// comma-joined string addBook's SplitNames consumes. A non-array value falls back
// to its string form. The site already split and role-stripped these names, so
// joining then re-splitting round-trips them.
func joinNames(v any) string {
	arr, ok := v.([]any)
	if !ok {
		return coerceStr(v)
	}
	parts := make([]string, 0, len(arr))
	for _, el := range arr {
		if s := coerceStr(el); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, ", ")
}
