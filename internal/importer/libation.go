package importer

import (
	"regexp"
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
//   - Locale: AudibleApi's locale name - "us"/"uk" are 2-letter but the other
//     marketplaces are full names ("germany", "france", ...); mapped to schema
//     codes by mapLibationLocale.
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
	if sub := e.str("Subtitle"); title != "" && sub != "" && !strings.Contains(title, sub) {
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
	if loc := e.str("Locale"); loc != "" {
		if region, ok := mapLibationLocale(loc); ok {
			b["region"] = region
		} else {
			b["region"] = loc // unmapped: addRecording's warning surfaces the raw value
		}
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
	sb.abridged = coerceBoolPtr(e["IsAbridged"])
	if cover := libationCover(e.str("PictureId")); cover != "" {
		b["image_url"] = cover
	}
	sb.series = parseLibationSeries(e.str("SeriesOrder"), e.str("SeriesNames"))
	return sb
}

// libationLocaleNames maps AudibleApi's full locale names (what Libation's
// Locale column carries for every marketplace except us/uk, which are already
// 2-letter; verified against AudibleApi/Localization.cs) onto the recording
// schema's marketplace codes.
var libationLocaleNames = map[string]string{
	"australia": "au",
	"brazil":    "br",
	"canada":    "ca",
	"france":    "fr",
	"germany":   "de",
	"india":     "in",
	"italy":     "it",
	"japan":     "jp",
	"spain":     "es",
}

// mapLibationLocale resolves a Libation Locale value - a 2-letter marketplace
// code ("us", "uk") or an AudibleApi locale name ("germany") - to a schema
// marketplace code. AudibleApi's legacy "pre-amazon - <name>" locales map like
// their plain counterparts. ok is false for an unknown or empty value.
func mapLibationLocale(locale string) (string, bool) {
	l := strings.ToLower(strings.TrimSpace(locale))
	l = strings.TrimPrefix(l, "pre-amazon - ")
	if region, ok := mapRegion(l); ok {
		return region, true
	}
	code, ok := libationLocaleNames[l]
	return code, ok
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

// libClaimStartRE marks where a series claim starts inside SeriesOrder: the
// string start or a ", " separator, followed by an order token (digits/dots/
// hyphens, possibly empty - the Cosmere no-position case) and its colon.
// Splitting on claim STARTS rather than on every ", " keeps a comma inside a
// series name ("1 : Ready, Set, Go: The Story") intact.
var libClaimStartRE = regexp.MustCompile(`(^|, )([0-9.\-]*) ?: `)

// splitLibationClaims cuts a SeriesOrder into its "order : name" claims. Each
// claim runs from its order token to the separator that starts the next claim.
func splitLibationClaims(order string) []string {
	locs := libClaimStartRE.FindAllStringSubmatchIndex(order, -1)
	claims := make([]string, 0, len(locs))
	for i, loc := range locs {
		start := loc[3] // end of the (^|, ) group: the claim's first char
		end := len(order)
		if i+1 < len(locs) {
			end = locs[i+1][0] // the next claim's ", " separator
		}
		claims = append(claims, order[start:end])
	}
	return claims
}

// parseLibationSeries turns a Libation SeriesOrder ("{order} : {name}", multiple
// joined by ", ") into series refs. SeriesOrder is authoritative (it carries both
// halves); SeriesNames is only a fallback when SeriesOrder is absent/unusable.
// Claims are split where a new "order :" actually starts (splitLibationClaims),
// then each claim on its FIRST colon - the order is always digits/dots/hyphens,
// so this is exact even when a name itself contains a comma ("Ready, Set, Go:
// The Story") or a colon ("Discworld: Rincewind"). An empty or sentinel order
// yields a name with no position (the book imports but is not placed). Every
// returned ref carries a non-empty name (the sourceBook invariant): empty names
// are skipped in both branches.
func parseLibationSeries(order, names string) []seriesRef {
	var refs []seriesRef
	for _, claim := range splitLibationClaims(order) {
		ci := strings.IndexByte(claim, ':')
		name := strings.TrimSpace(claim[ci+1:])
		if name == "" {
			continue
		}
		refs = append(refs, makeLibationSeriesRef(strings.TrimSpace(claim[:ci]), name))
	}
	if len(refs) > 0 {
		return refs
	}

	// Fallback: SeriesNames present but no usable SeriesOrder -> names, no positions.
	refs = nil
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
