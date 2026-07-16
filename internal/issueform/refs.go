package issueform

import (
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// slugify wraps the importer's canonical slugifier so form composition and bulk
// import derive identical slugs from the same text.
func slugify(s string) string { return importer.Slugify(s) }

// normalizeSequence wraps the importer's series-position validator.
func normalizeSequence(raw string) (string, bool) { return importer.NormalizeSequence(raw) }

// splitNames splits a "one per line or comma-separated" name list, trimming
// each, stripping trailing Audible credit qualifiers (via the importer), and
// dropping empties.
func splitNames(joined string) []string {
	var out []string
	for _, line := range strings.FieldsFunc(joined, func(r rune) bool { return r == '\n' || r == ',' }) {
		if name := strings.TrimSpace(line); name != "" {
			out = append(out, importer.StripRoleQualifier(name))
		}
	}
	return out
}

// splitList splits a "one per line or comma-separated" plain list (ISBNs, etc.)
// without name-qualifier stripping.
func splitList(joined string) []string {
	var out []string
	for _, line := range strings.FieldsFunc(joined, func(r rune) bool { return r == '\n' || r == ',' }) {
		if v := strings.TrimSpace(line); v != "" {
			out = append(out, v)
		}
	}
	return out
}

var (
	languageRE  = regexp.MustCompile(`^[a-z]{2,3}(-[a-z0-9]{2,8})*$`)
	dateYearRE  = regexp.MustCompile(`^\d{4}(-\d{2}-\d{2})?$`)
	dateFlexRE  = regexp.MustCompile(`^\d{4}(-\d{2}(-\d{2})?)?$`)
	wikidataRE  = regexp.MustCompile(`^Q\d+$`)
	olWorkRE    = regexp.MustCompile(`^OL\d+W$`)
	isbnRE      = regexp.MustCompile(`^(\d{9}[0-9Xx]|\d{13})$`)
	isbnStripRE = regexp.MustCompile(`[-\s]`)
	worksPathRE = regexp.MustCompile(`works/[^/]+/([^/]+)`)
)

// normalizeLanguage lowercases a BCP-47 tag (the schema restricts the region
// subtag to lowercase) and reports whether it matches the schema pattern.
func normalizeLanguage(raw string) (string, bool) {
	lang := strings.ToLower(strings.TrimSpace(raw))
	if lang == "" || !languageRE.MatchString(lang) {
		return "", false
	}
	return lang, true
}

// normalizeISBN strips hyphens/spaces and reports whether the remainder is a
// valid ISBN-10/13.
func normalizeISBN(raw string) (string, bool) {
	v := isbnStripRE.ReplaceAllString(strings.TrimSpace(raw), "")
	if !isbnRE.MatchString(v) {
		return "", false
	}
	return v, true
}

// regionAliases maps the region words a submitter is likely to type onto the
// recording schema's marketplace enum (which uses "uk", not "gb").
var regionAliases = map[string]string{
	"us": "us", "usa": "us", "gb": "uk", "uk": "uk", "ca": "ca", "can": "ca",
	"au": "au", "aus": "au", "de": "de", "ger": "de", "fr": "fr", "es": "es",
	"it": "it", "jp": "jp", "jpn": "jp", "in": "in", "ind": "in", "br": "br", "bra": "br",
}

// normalizeRegion maps a region word onto the marketplace enum.
func normalizeRegion(raw string) (string, bool) {
	r, ok := regionAliases[strings.ToLower(strings.TrimSpace(raw))]
	return r, ok
}

// parseASINs parses the "region: ASIN" lines from a form field into schema
// ASIN entries, noting (but not failing on) unrecognized regions or ASINs. A
// bare line that is a valid ASIN with no "region:" prefix defaults to region us:
// the site's issue prefill emits a region-less ASIN whenever the source book has
// no marketplace (always the case for Audiobookshelf and folder-scan books), and
// dropping it would lose the recording's primary identity/dedup key.
func (c *composer) parseASINs(block string) []outASIN {
	var out []outASIN
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		i := strings.IndexAny(line, ":\t")
		if i < 0 {
			if asin := importer.NormalizeASIN(line); asin != "" {
				c.note("ASIN %s had no region prefix - defaulted to us", asin)
				out = append(out, outASIN{Region: "us", ASIN: asin})
			} else {
				c.note("ASIN line %q is neither \"region: ASIN\" nor a bare ASIN - skipped", line)
			}
			continue
		}
		region, regionOK := normalizeRegion(line[:i])
		asin := importer.NormalizeASIN(line[i+1:])
		if !regionOK {
			c.note("region %q is not a known marketplace - ASIN not recorded", strings.TrimSpace(line[:i]))
			continue
		}
		if asin == "" {
			c.note("value %q is not a valid ASIN - skipped", strings.TrimSpace(line[i+1:]))
			continue
		}
		out = append(out, outASIN{Region: region, ASIN: asin})
	}
	return out
}

// parseISBNs parses and validates an ISBN list field.
func (c *composer) parseISBNs(block string) []string {
	var out []string
	for _, raw := range splitList(block) {
		if isbn, ok := normalizeISBN(raw); ok {
			out = append(out, isbn)
		} else {
			c.note("value %q is not a valid ISBN - skipped", raw)
		}
	}
	return out
}

// recordingPath returns the data-relative slash path of a recording file.
func recordingPath(workSlug, recSlug string) string {
	return path.Join("works", model.Shard(workSlug), workSlug, "recordings", recSlug+".json")
}

// resolveWorkRef resolves a "work" reference (a slug, a data-tree path, or a
// meta.audiosilo.app / GitHub URL) to a work slug. ok is false when nothing
// slug-shaped can be extracted.
func resolveWorkRef(ref string) (slug string, ok bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", false
	}
	if strings.Contains(ref, "://") {
		if u, err := url.Parse(ref); err == nil {
			if id := u.Query().Get("id"); id != "" {
				return sanitizeSlug(id)
			}
			if m := worksPathRE.FindStringSubmatch(u.Path); m != nil {
				return sanitizeSlug(m[1])
			}
		}
		return "", false
	}
	if m := worksPathRE.FindStringSubmatch(ref); m != nil {
		return sanitizeSlug(m[1])
	}
	return sanitizeSlug(ref)
}

// sanitizeSlug accepts an already-valid slug verbatim, otherwise slugifies the
// input as a best effort. ok is false when nothing survives.
func sanitizeSlug(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if model.ValidSlug(s) {
		return s, true
	}
	if slug := slugify(s); slug != "" {
		return slug, true
	}
	return "", false
}

// resolveRecordPath resolves a "record" reference to a data-relative file path
// and its parsed location. It accepts a data-tree path (with or without a
// leading data/), a GitHub blob URL, or a meta.audiosilo.app
// work?id=/series?id=/person?id= URL. ok is false when it cannot be mapped to a
// recognized data-tree location.
func resolveRecordPath(ref string) (rel string, loc model.Location, ok bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", model.Location{}, false
	}
	if strings.Contains(ref, "://") {
		u, err := url.Parse(ref)
		if err != nil {
			return "", model.Location{}, false
		}
		if id := u.Query().Get("id"); id != "" {
			switch {
			case strings.Contains(u.Path, "series"):
				rel = path.Join("series", model.Shard(id), id+".json")
			case strings.Contains(u.Path, "person"):
				rel = path.Join("people", model.Shard(id), id+".json")
			default: // work?id=
				rel = path.Join("works", model.Shard(id), id, "work.json")
			}
			loc, lok := model.ParseLocation(rel)
			return rel, loc, lok
		}
		ref = u.Path // fall through to path handling for blob URLs
	}
	// Trim a leading data/ (and any repo/blob/main prefix) to land on the
	// data-tree-relative portion.
	ref = strings.TrimPrefix(path.Clean(ref), "/")
	if i := strings.Index(ref, "data/"); i >= 0 {
		ref = ref[i+len("data/"):]
	}
	loc, lok := model.ParseLocation(ref)
	if !lok {
		return "", model.Location{}, false
	}
	return ref, loc, true
}
