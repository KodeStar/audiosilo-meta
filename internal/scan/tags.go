package scan

import (
	"cmp"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/dhowden/tag"

	"github.com/kodestar/audiosilo-meta/internal/importer"
)

// tagInfo is the metadata read from a single audio file's tags - either the
// canonical dhowden read (readTags) or the ffprobe container tags
// (probeTagInfo), merged by mergeTags so ONE precedence policy exists. Empty
// fields mean "the tags did not carry it" and are omitted from the output.
// All string fields are stored trimmed.
//
// album and trackTitle are kept apart (rather than pre-merged into one title)
// because the collection split decision (splitVerdict) needs them as separate
// evidence; bookTitle() derives the book title.
type tagInfo struct {
	album       string
	trackTitle  string
	authors     []string
	narrators   []string
	series      string
	position    string
	asin        string
	isbn        string
	subtitle    string // container tags only (dhowden does not expose these)
	publisher   string
	language    string
	releaseDate string // a bare year from dhowden, possibly a full date from ffprobe
}

// bookTitle is the book-level title: audiobook titles conventionally live in
// the album tag, with the track title as fallback.
func (t tagInfo) bookTitle() string {
	return cmp.Or(t.album, t.trackTitle)
}

// mergeTags overlays probe-derived container tags (p) under the canonical
// dhowden-read tags (d): dhowden wins field-wise, probe fills the gaps. One
// exception: the LONGER release date wins regardless of reader, so a full ISO
// date from the container beats dhowden's bare year.
func mergeTags(d, p tagInfo) tagInfo {
	out := d
	out.album = cmp.Or(d.album, p.album)
	out.trackTitle = cmp.Or(d.trackTitle, p.trackTitle)
	if len(out.authors) == 0 {
		out.authors = p.authors
	}
	if len(out.narrators) == 0 {
		out.narrators = p.narrators
	}
	out.series = cmp.Or(d.series, p.series)
	out.position = cmp.Or(d.position, p.position)
	out.asin = cmp.Or(d.asin, p.asin)
	out.isbn = cmp.Or(d.isbn, p.isbn)
	out.subtitle = cmp.Or(d.subtitle, p.subtitle)
	out.publisher = cmp.Or(d.publisher, p.publisher)
	out.language = cmp.Or(d.language, p.language)
	if len(p.releaseDate) > len(d.releaseDate) {
		out.releaseDate = p.releaseDate
	}
	return out
}

// readTags reads embedded tags from one audio file (best-effort - a file that
// dhowden cannot parse yields a zero tagInfo and ok=false so the caller can
// count read failures). It never returns an error: a bad tag block is not a
// scan failure, just missing metadata.
func readTags(path string) (tagInfo, bool) {
	f, err := os.Open(path)
	if err != nil {
		return tagInfo{}, false
	}
	defer func() { _ = f.Close() }()

	md, err := tag.ReadFrom(f)
	if err != nil {
		return tagInfo{}, false
	}
	return tagsFromMetadata(md), true
}

// tagsFromMetadata extracts a tagInfo from a parsed tag.Metadata. Split out
// from readTags so the field-mapping logic is unit-testable without a real file.
func tagsFromMetadata(md tag.Metadata) tagInfo {
	var t tagInfo

	t.album = strings.TrimSpace(md.Album())
	t.trackTitle = strings.TrimSpace(md.Title())
	if v := firstNonEmpty(md.AlbumArtist(), md.Artist()); v != "" {
		t.authors = splitPeople(v)
	}
	// Narrator is conventionally the composer in audiobook tagging.
	if v := strings.TrimSpace(md.Composer()); v != "" {
		t.narrators = splitPeople(v)
	}
	if y := md.Year(); y > 0 {
		t.releaseDate = strconv.Itoa(y)
	}

	raw := md.Raw()
	if series := rawString(raw, "series", "©grp", "show", "TXXX:SERIES", "TXXX:series"); series != "" {
		t.series = series
	}
	if narr := rawString(raw, "narrator", "©nrt", "----:com.apple.iTunes:NARRATOR", "TXXX:NARRATOR"); narr != "" {
		t.narrators = splitPeople(narr)
	}
	if part := rawString(raw, "series-part", "TXXX:SERIES-PART", "movement", "movementname"); part != "" {
		t.position = normalizePosition(part, true)
	}
	t.asin = asinFromRaw(raw)
	t.isbn = isbnFromRaw(raw)
	return t
}

// probeTagInfo maps ffprobe container tags (lower-cased keys) into a tagInfo so
// the dhowden and ffprobe reads share ONE merge policy (mergeTags) and one key
// vocabulary per identifier (asinFromRaw/isbnFromRaw).
func probeTagInfo(t map[string]string) tagInfo {
	if len(t) == 0 {
		return tagInfo{}
	}
	var out tagInfo
	out.album = strings.TrimSpace(t["album"])
	out.trackTitle = strings.TrimSpace(t["title"])
	if v := firstNonEmpty(t["album_artist"], t["artist"], t["author"]); v != "" {
		out.authors = splitPeople(v)
	}
	if v := firstNonEmpty(t["narrator"], t["composer"]); v != "" {
		out.narrators = splitPeople(v)
	}
	out.series = firstNonEmpty(t["series"], t["show"], t["grouping"])
	if v := t["series-part"]; v != "" {
		out.position = normalizePosition(v, true)
	}
	out.subtitle = firstNonEmpty(t["subtitle"], t["subtitle-0"])
	out.publisher = firstNonEmpty(t["publisher"], t["label"], t["©pub"])
	out.language = strings.TrimSpace(t["language"])
	out.releaseDate = firstNonEmpty(t["date"], t["releasedate"], t["year"])
	raw := toRawMap(t)
	out.asin = asinFromRaw(raw)
	out.isbn = isbnFromRaw(raw)
	return out
}

// toRawMap adapts a string-valued tag map to the dhowden Raw() shape so the
// ASIN/ISBN hunters have exactly one implementation.
func toRawMap(t map[string]string) map[string]interface{} {
	out := make(map[string]interface{}, len(t))
	for k, v := range t {
		out[k] = v
	}
	return out
}

// isASINKey reports whether a raw tag key plausibly carries an ASIN: anything
// containing "asin", the Audible "cdek" content key, or an Audible freeform
// atom ("----:com.audible:..."). Matching on the key (not the value) avoids
// false positives from unrelated B0-prefixed text.
func isASINKey(k string) bool {
	k = strings.ToLower(k)
	return strings.Contains(k, "asin") || strings.Contains(k, "cdek") || strings.Contains(k, "audible")
}

// asinFromRaw hunts for an Audible ASIN across the raw tag atoms/frames.
// Libation and OpenAudible m4b files embed it under keys like ASIN, CDEK,
// AUDIBLE_ASIN, or a "----:com.audible..." freeform atom; ID3 stores it as a
// TXXX:ASIN frame. An explicitly ASIN-keyed value is accepted whole via
// importer.NormalizeASIN first (so valid non-B0 ASINs are kept); the free-text
// B0 pattern is the fallback for values with surrounding noise.
func asinFromRaw(raw map[string]interface{}) string {
	for k, v := range raw {
		if !isASINKey(k) {
			continue
		}
		s := rawValueString(v)
		if asin := importer.NormalizeASIN(s); asin != "" {
			return asin
		}
		if asin := findASIN(strings.ToUpper(s)); asin != "" {
			return asin
		}
	}
	return ""
}

// isbnFromRaw hunts for an ISBN-13 in the raw tags (keys containing "isbn").
func isbnFromRaw(raw map[string]interface{}) string {
	for k, v := range raw {
		if !strings.Contains(strings.ToLower(k), "isbn") {
			continue
		}
		if isbn := findISBN(rawValueString(v)); isbn != "" {
			return isbn
		}
	}
	return ""
}

// rawValueString stringifies a raw tag value, which dhowden may hand back as a
// string, []byte, a *tag.Comm (ID3 COMM/TXXX), or a number.
func rawValueString(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []byte:
		return string(t)
	case fmt.Stringer: // dhowden's *tag.Comm (ID3 COMM/TXXX) implements String()
		return t.String()
	case int:
		return strconv.Itoa(t)
	default:
		return ""
	}
}

func rawString(raw map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := raw[k]; ok {
			if s := strings.TrimSpace(rawValueString(v)); s != "" {
				return s
			}
		}
	}
	return ""
}

// splitPeople splits a multi-author/narrator string into individual names. The
// extra audiobook-tag separators (";", "&", "/", " and ") are a scan-side
// pre-split; each fragment then goes through importer.SplitNames, the shared
// comma splitter that also strips trailing Audible role qualifiers
// ("J. Kharkova - translator" -> "J. Kharkova").
func splitPeople(s string) []string {
	var out []string
	for _, frag := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ';' || r == '&' || r == '/'
	}) {
		for _, part := range strings.Split(frag, " and ") {
			out = append(out, importer.SplitNames(part)...)
		}
	}
	return out
}

// firstNonEmpty returns the first value that is non-empty after trimming.
// (Tag values are not pre-trimmed, so plain cmp.Or is not enough here.)
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
