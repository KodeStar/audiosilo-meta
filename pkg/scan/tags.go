package scan

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
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
// album and trackTitle are kept apart (never pre-merged into one title): the
// collection split decision (splitVerdict) needs them as separate evidence,
// and the title policy differs by book shape (assemble: a multi-file book
// never takes a per-file track title).
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
	releaseDate string // validated: "YYYY" or "YYYY-MM-DD" (normalizeReleaseDate)
}

// mergeTags overlays probe-derived container tags (p) under the canonical
// dhowden-read tags (d): dhowden wins field-wise, probe fills the gaps. Two
// deliberate exceptions:
//
//   - releaseDate: the LONGER validated value wins (a full ISO date beats a
//     bare year, whichever reader produced it); on a tie dhowden wins.
//   - asin is NOT merged: the wire precedence is explicit dhowden tag >
//     filename > path > probe container, so the probe-container ASIN rides
//     separately (fileData.probeASIN) and is applied LAST in assemble - a
//     user who renames a folder to override a stale embedded ASIN must win
//     over the container copy.
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
	out.isbn = cmp.Or(d.isbn, p.isbn)
	out.subtitle = cmp.Or(d.subtitle, p.subtitle)
	out.publisher = cmp.Or(d.publisher, p.publisher)
	out.language = cmp.Or(d.language, p.language)
	if len(p.releaseDate) > len(d.releaseDate) {
		out.releaseDate = p.releaseDate
	}
	return out
}

// readTags reads embedded tags from one audio file. hasTags is true when a tag
// block was parsed (even an empty one); failed is true only for a REAL read
// problem (open error, corrupt tag data) - a plainly untagged file
// (tag.ErrNoTagsFound, which dhowden also returns for formats it has no parser
// for, like .wma/.aac) is absent metadata, not a failure.
func readTags(path string) (t tagInfo, hasTags, failed bool) {
	f, err := os.Open(path)
	if err != nil {
		return tagInfo{}, false, true
	}
	defer func() { _ = f.Close() }()

	md, err := tag.ReadFrom(f)
	if err != nil {
		return tagInfo{}, false, !errors.Is(err, tag.ErrNoTagsFound)
	}
	return tagsFromMetadata(md), true, false
}

// Raw-key vocabularies, shared by both readers. dhowden's Raw() map is reshaped
// by flatRaw first (ID3v2 TXXX frames resolve to their Description, MP4 known
// atoms keep their byte-encoded codes like "\xa9grp"); ffprobe's flattened
// container tags use plain names. The union lives here once - keys one reader
// never emits are simply dead for that reader.
var (
	seriesKeys     = []string{"series", "\xa9grp", "grouping", "show"}
	narratorKeys   = []string{"narrator"}
	seriesPartKeys = []string{"series-part", "series_part"}
)

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
		t.releaseDate = normalizeReleaseDate(strconv.Itoa(y))
	}

	pairs := flatRaw(md.Raw())
	if v := lookupRaw(pairs, seriesKeys...); v != "" {
		t.series = v
	}
	if v := lookupRaw(pairs, narratorKeys...); v != "" {
		t.narrators = splitPeople(v)
	}
	if v := lookupRaw(pairs, seriesPartKeys...); v != "" {
		t.position = normalizePosition(v, true)
	}
	t.asin = asinFromPairs(pairs)
	t.isbn = isbnFromPairs(pairs)
	return t
}

// probeTagInfo maps ffprobe container tags (lower-cased keys) into a tagInfo so
// the dhowden and ffprobe reads share ONE merge policy (mergeTags) and one key
// vocabulary per identifier (asinFromPairs/isbnFromPairs).
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
	pairs := flatStringMap(t)
	out.series = lookupRaw(pairs, seriesKeys...)
	if v := lookupRaw(pairs, seriesPartKeys...); v != "" {
		out.position = normalizePosition(v, true)
	}
	out.subtitle = firstNonEmpty(t["subtitle"], t["subtitle-0"])
	out.publisher = firstNonEmpty(t["publisher"], t["label"])
	out.language = strings.TrimSpace(t["language"])
	out.releaseDate = normalizeReleaseDate(firstNonEmpty(t["date"], t["releasedate"], t["year"]))
	out.asin = asinFromPairs(pairs)
	out.isbn = isbnFromPairs(pairs)
	return out
}

// rawPair is one flattened (key, value) tag entry. Keys are lower-cased and
// values trimmed; slices are sorted by key so every hunt is deterministic.
type rawPair struct{ key, val string }

// flatRaw flattens dhowden's Raw() map into deterministic rawPairs. The crucial
// reshaping (verified against dhowden's source): ID3v2 stores user frames under
// raw keys "TXXX"/"TXXX_0"/... as *tag.Comm with the user-defined field name in
// Description - so the Description becomes the pair key. MP4 known atoms are
// keyed by their byte-encoded code ("\xa9grp"); freeform "----" atoms survive
// only for dhowden's allow-listed means (com.apple.iTunes and two DJ tools),
// keyed by the bare name sub-atom (e.g. "NARRATOR") - com.audible freeforms
// are dropped by dhowden and reachable only through ffprobe.
func flatRaw(raw map[string]interface{}) []rawPair {
	pairs := make([]rawPair, 0, len(raw))
	for k, v := range raw {
		key, val := lowerKey(k), ""
		if c, ok := v.(*tag.Comm); ok {
			if d := trimTagValue(c.Description); d != "" {
				key = lowerKey(d)
			}
			val = trimTagValue(c.Text)
		} else {
			val = trimTagValue(rawValueString(v))
		}
		if val == "" {
			continue
		}
		pairs = append(pairs, rawPair{key: key, val: val})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].key < pairs[j].key })
	return pairs
}

// lowerKey lower-cases a tag key BYTE-safely: MP4 atom codes like "\xa9grp"
// are not valid UTF-8, and strings.ToLower would mangle the 0xA9 byte into
// U+FFFD, breaking the byte-keyed lookups.
func lowerKey(k string) string {
	b := []byte(k)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// trimTagValue trims whitespace AND NUL bytes: real ID3v2 TXXX values (ffmpeg,
// several taggers) arrive NUL-terminated, and a stray NUL would defeat both
// exact-value validation (NormalizeASIN) and case-folded comparisons.
func trimTagValue(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		return r == 0 || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
}

// flatStringMap is flatRaw for ffprobe's already-flat string map.
func flatStringMap(t map[string]string) []rawPair {
	pairs := make([]rawPair, 0, len(t))
	for k, v := range t {
		if val := trimTagValue(v); val != "" {
			pairs = append(pairs, rawPair{key: lowerKey(k), val: val})
		}
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].key < pairs[j].key })
	return pairs
}

// lookupRaw returns the first value under any of keys - the key list order is
// the priority, and within one key the sorted pair order makes ties
// deterministic.
func lookupRaw(pairs []rawPair, keys ...string) string {
	for _, k := range keys {
		for _, p := range pairs {
			if p.key == k {
				return p.val
			}
		}
	}
	return ""
}

// asinFromPairs hunts for an ASIN across the flattened tags, deterministically:
//
//  1. Values under an EXACTLY "asin"-named key first, accepted whole via
//     importer.NormalizeASIN - an explicitly labeled value is trusted, so
//     valid non-B0 ASINs ("1774248182") are kept.
//  2. Then values under fuzzy ASIN-ish keys (audible_asin, cdek, ...), which
//     must contain the strict upper-case B0 shape - a fuzzy key is not enough
//     to trust an arbitrary 10-char value (an epoch-seconds number under
//     "audible_date_asin" must not become the ASIN).
func asinFromPairs(pairs []rawPair) string {
	for _, p := range pairs {
		if p.key == "asin" {
			if a := importer.NormalizeASIN(p.val); a != "" {
				return a
			}
			if a := findASIN(p.val); a != "" {
				return a // explicit key, value with surrounding noise
			}
		}
	}
	for _, p := range pairs {
		if p.key != "asin" && isASINKey(p.key) {
			if a := findASIN(p.val); a != "" {
				return a
			}
		}
	}
	return ""
}

// isASINKey reports whether a flattened tag key plausibly carries an ASIN:
// anything containing "asin", the Audible "cdek" content key, or "audible".
// Matching on the key (not the value) avoids false positives from unrelated
// B0-prefixed text.
func isASINKey(k string) bool {
	return strings.Contains(k, "asin") || strings.Contains(k, "cdek") || strings.Contains(k, "audible")
}

// isbnFromPairs hunts for a valid ISBN-13 under keys containing "isbn".
func isbnFromPairs(pairs []rawPair) string {
	for _, p := range pairs {
		if !strings.Contains(p.key, "isbn") {
			continue
		}
		if isbn := findISBN(p.val); isbn != "" {
			return isbn
		}
	}
	return ""
}

// releaseDateRE accepts "YYYY" or "YYYY-MM-DD".
var releaseDateRE = regexp.MustCompile(`^(\d{4})(-\d{2}-\d{2})?$`)

// normalizeReleaseDate validates a raw date-ish tag value into the two shapes
// the output emits: "YYYY" or "YYYY-MM-DD" (an ISO timestamp is truncated to
// its date part), with a sane year range so junk like "0000-00-00" or a rip
// timestamp can never win. Returns "" for anything else (omit, never guess).
func normalizeReleaseDate(v string) string {
	v = strings.TrimSpace(v)
	if len(v) > 10 {
		v = v[:10] // "2017-11-02T09:00:00Z" -> "2017-11-02"
	}
	m := releaseDateRE.FindStringSubmatch(v)
	if m == nil {
		return ""
	}
	if y, _ := strconv.Atoi(m[1]); y < 1400 || y > 2100 {
		return ""
	}
	return v
}

// rawValueString stringifies a raw tag value, which dhowden may hand back as a
// string, []byte, a *tag.Comm (handled by flatRaw before this), or a number.
func rawValueString(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []byte:
		return string(t)
	case fmt.Stringer:
		return t.String()
	case int:
		return strconv.Itoa(t)
	default:
		return ""
	}
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
