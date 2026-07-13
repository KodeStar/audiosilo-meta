package importer

import (
	"strings"
)

// libation.go maps a Libation "Export Library" JSON export into the same
// internal sourceBook the OpenAudible path produces, so both sources share
// every mapping, dedup, and series rule (see openaudible.go / mapping.go /
// importer.go). Only factual fields are carried across: personal state
// (Account, DateAdded, MyRating*, BookStatus, AbsentFromLastScan) and
// non-factual copy (Description, CommunityRating*, CategoriesNames, ContentType,
// HasPdf) are dropped here, before they ever reach a record (see LICENSING.md).
//
// A Libation export is a top-level JSON array of objects, each keyed by its
// AudibleProductId (the ASIN). Field notes verified against a real export:
//   - AuthorNames / NarratorNames: comma-joined ("A, B, C"); role qualifiers
//     ("- translator") ride on names and are stripped by splitNames.
//   - SeriesNames / SeriesOrder: SeriesOrder is authoritative and carries both
//     the position and the name for every series ("{order} : {name}", multiple
//     joined by ", "), so a book can be in several series at once.
//   - Locale: a marketplace code ("uk"); accepted only when the recording schema
//     knows it (mapRegion).
//   - Language: a word ("English"); mapped to an ISO code (mapLanguage).
//   - LengthInMinutes: whole minutes. DatePublished: an ISO timestamp.
//   - PictureId: an Amazon image id; the cover CDN URL is derived from it.
//   - IsAbridged: Libation states this explicitly, so a present bool is a fact.

// parseLibation decodes a Libation export and converts every entry into the
// OpenAudible-shaped sourceBook the shared pipeline consumes.
func parseLibation(data []byte) ([]sourceBook, error) {
	entries, err := decodeEntries(data, "libation export")
	if err != nil {
		return nil, err
	}
	books := make([]sourceBook, 0, len(entries))
	for _, e := range entries {
		books = append(books, libationToBook(e))
	}
	return books, nil
}

// libationToBook normalizes one Libation entry into a sourceBook, translating
// field names and shapes to the OpenAudible keys addBook understands and
// dropping every non-factual field. The runtime and series claims are
// parse-time facts carried on the sourceBook itself.
func libationToBook(e rawBook) sourceBook {
	b := rawBook{}
	sb := sourceBook{raw: b}

	if asin := e.str("AudibleProductId"); asin != "" {
		b["asin"] = asin
	}

	// title_short is the work title; title is the fuller "Title: Subtitle" used
	// only for slug disambiguation (mirrors OpenAudible's short/full split).
	title := e.str("Title")
	b["title_short"] = title
	if sub := e.str("Subtitle"); sub != "" && !strings.Contains(title, sub) {
		b["title"] = title + ": " + sub
	} else {
		b["title"] = title
	}

	if a := e.str("AuthorNames"); a != "" {
		b["author"] = a
	}
	if n := e.str("NarratorNames"); n != "" {
		b["narrated_by"] = n
	}
	if lang := e.str("Language"); lang != "" {
		b["language"] = lang
	}
	if region := e.str("Locale"); region != "" {
		b["region"] = region
	}
	if pub := e.str("Publisher"); pub != "" {
		b["publisher"] = pub
	}
	if rd := libationDate(e.str("DatePublished")); rd != "" {
		b["release_date"] = rd
	}
	if mins, ok := coerceInt(e["LengthInMinutes"]); ok && mins > 0 {
		sb.runtimeMin = int(mins)
	}
	if v := coerceBoolPtr(e["IsAbridged"]); v != nil {
		b["abridged"] = *v
	}
	if cover := libationCover(e.str("PictureId")); cover != "" {
		b["image_url"] = cover
	}
	sb.series = parseLibationSeries(e.str("SeriesOrder"), e.str("SeriesNames"))
	return sb
}

// libationDate reduces an ISO timestamp ("2018-10-18T23:00:00") to its
// YYYY-MM-DD date part; addRecording validates it before use.
func libationDate(ts string) string {
	ts = strings.TrimSpace(ts)
	if i := strings.IndexByte(ts, 'T'); i >= 0 {
		ts = ts[:i]
	}
	return ts
}

// libationCover builds the Amazon cover CDN URL from a PictureId. The id can
// contain '+' (a valid image-id char) which is percent-encoded so the URL is
// unambiguous; every other id char ([A-Za-z0-9-]) is already URL-safe. Returns
// "" for an empty id.
func libationCover(pictureID string) string {
	pictureID = strings.TrimSpace(pictureID)
	if pictureID == "" {
		return ""
	}
	enc := strings.ReplaceAll(pictureID, "+", "%2B")
	return "https://m.media-amazon.com/images/I/" + enc + "._SL500_.jpg"
}

// libationUnsorted is Libation's sentinel SeriesOrder position for content it
// cannot order (periodical episodes); it means "no position", not book 999999999.
const libationUnsorted = "999999999"

// parseLibationSeries turns a Libation SeriesOrder ("{order} : {name}", multiple
// joined by ", ") into series refs. SeriesOrder is authoritative (it carries both
// halves); SeriesNames is only a fallback when SeriesOrder is absent. Entries are
// split on ", " (a name never contains it in practice) and each entry on its
// FIRST colon (the order is always digits/dots/hyphens, so this is exact even
// when a name itself contains a colon, e.g. "Discworld: Rincewind"). An empty or
// sentinel order yields a name with no position (the book imports but is not
// placed). Every returned ref carries a non-empty name (the sourceBook
// invariant): empty names are skipped in both branches.
func parseLibationSeries(order, names string) []seriesRef {
	if strings.TrimSpace(order) != "" {
		var refs []seriesRef
		for _, part := range strings.Split(order, ", ") {
			ci := strings.IndexByte(part, ':')
			if ci < 0 {
				continue // not "order : name" shaped
			}
			name := strings.TrimSpace(part[ci+1:])
			if name == "" {
				continue
			}
			refs = append(refs, makeLibationSeriesRef(strings.TrimSpace(part[:ci]), name))
		}
		if len(refs) > 0 {
			return refs
		}
	}

	// Fallback: SeriesNames present but no usable SeriesOrder -> names, no positions.
	var refs []seriesRef
	for _, name := range strings.Split(strings.TrimSpace(names), ", ") {
		if name = strings.TrimSpace(name); name != "" {
			refs = append(refs, seriesRef{name: name})
		}
	}
	return refs
}

// makeLibationSeriesRef validates a single order token against the position
// rules; the unsorted sentinel and an empty order both become "no position".
func makeLibationSeriesRef(order, name string) seriesRef {
	if order == libationUnsorted {
		order = ""
	}
	pos, ok := normalizeSequence(order)
	return seriesRef{name: name, seq: pos, seqOK: ok, rawSeq: order}
}
