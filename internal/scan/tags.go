package scan

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/dhowden/tag"
)

// tagInfo is the metadata read from a single audio file's embedded tags. Empty
// fields mean "the tags did not carry it" and are omitted from the output.
//
// title is the merged book title (album preferred, track title fallback);
// album and trackTitle also keep the two raw values apart because the
// collection split decision (splitVerdict) needs them as separate evidence.
type tagInfo struct {
	title      string
	album      string
	trackTitle string
	authors    []string
	narrators  []string
	series     string
	position   string
	asin       string
	isbn       string
	year       int
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
	// Audiobook title is conventionally the album; fall back to the track title.
	t.title = firstNonEmpty(t.album, t.trackTitle)
	if v := firstNonEmpty(md.AlbumArtist(), md.Artist()); v != "" {
		t.authors = splitPeople(v)
	}
	// Narrator is conventionally the composer in audiobook tagging.
	if v := strings.TrimSpace(md.Composer()); v != "" {
		t.narrators = splitPeople(v)
	}
	t.year = md.Year()

	raw := md.Raw()
	if series := rawString(raw, "series", "©grp", "show", "TXXX:SERIES", "TXXX:series"); series != "" {
		t.series = strings.TrimSpace(series)
	}
	if narr := rawString(raw, "narrator", "©nrt", "----:com.apple.iTunes:NARRATOR", "TXXX:NARRATOR"); narr != "" {
		t.narrators = splitPeople(narr)
	}
	if part := rawString(raw, "series-part", "TXXX:SERIES-PART", "movement", "movementname"); part != "" {
		if pos := normalizePosition(strings.TrimSpace(part), true); pos != "" {
			t.position = pos
		}
	}
	t.asin = asinFromRaw(raw)
	t.isbn = isbnFromRaw(raw)
	return t
}

// asinKeyRE matches raw tag keys that plausibly carry an ASIN: anything
// containing "asin", the Audible "cdek" content key, or an Audible freeform atom
// ("----:com.audible:..."). Matching on the key (not the value) avoids false
// positives from unrelated B0-prefixed text.
var asinKeyRE = regexp.MustCompile(`(?i)asin|cdek|audible`)

// asinFromRaw hunts for an Audible ASIN across the raw tag atoms/frames. Libation
// and OpenAudible m4b files embed it under keys like ASIN, CDEK, AUDIBLE_ASIN, or
// a "----:com.audible..." freeform atom; ID3 stores it as a TXXX:ASIN frame.
func asinFromRaw(raw map[string]interface{}) string {
	for k, v := range raw {
		if !asinKeyRE.MatchString(k) {
			continue
		}
		if asin := findASIN(strings.ToUpper(rawValueString(v))); asin != "" {
			return asin
		}
	}
	return ""
}

var isbnKeyRE = regexp.MustCompile(`(?i)isbn`)

// isbnFromRaw hunts for an ISBN-13 in the raw tags (keys containing "isbn").
func isbnFromRaw(raw map[string]interface{}) string {
	for k, v := range raw {
		if !isbnKeyRE.MatchString(k) {
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

// splitPeople splits a multi-author/narrator string on common separators and
// trims each name. A name with no separator is returned as a single element.
func splitPeople(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ';' || r == '&' || r == '/'
	})
	var out []string
	for _, f := range fields {
		// Also split a spelled-out " and " conjunction.
		for _, part := range strings.Split(f, " and ") {
			if name := strings.TrimSpace(part); name != "" {
				out = append(out, name)
			}
		}
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
