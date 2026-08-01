package issueform

import (
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// slugify wraps the project's canonical slugifier so form composition and bulk
// import derive identical slugs from the same text. It goes straight to
// pkg/model, where the rule lives, rather than through the importer's
// same-named delegate.
func slugify(s string) string { return model.Slugify(s) }

// normalizeSequence wraps the importer's series-position validator.
func normalizeSequence(raw string) (string, bool) { return importer.NormalizeSequence(raw) }

// splitNames splits a "one per line or comma-separated" name list, trimming
// each, cleaning the credit (role qualifiers, prefix credits, doubled names -
// via the importer), and dropping empties.
func splitNames(joined string) []string {
	var out []string
	for _, line := range strings.FieldsFunc(joined, func(r rune) bool { return r == '\n' || r == ',' }) {
		if name := strings.TrimSpace(line); name != "" {
			out = append(out, importer.CleanCreditName(name))
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

// normalizeISBN wraps the importer's ISBN validator, so a typed form field and a
// bulk import accept (and store) exactly the same values.
func normalizeISBN(raw string) (string, bool) { return importer.NormalizeISBN(raw) }

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
//
// Every ASIN it accepts is also recorded on the composer
// (noteSubmissionASIN), because provenance.go's libex marker is only honored
// for an ASIN the submission itself carries. Recording it HERE rather than at
// the call sites means a future form that parses ASINs cannot forget to.
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
				c.noteSubmissionASIN(asin)
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
		c.noteSubmissionASIN(asin)
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

// recordRef is a resolved "record" reference: the kind of entity a submitter
// named and the slug (or slug pair) that names it.
type recordRef struct {
	kind model.Kind
	// slug is the entity's own slug - the RECORDING slug for a recording.
	slug string
	// workSlug is the parent work of a recording, empty otherwise.
	workSlug string
}

// resolveRecordRef resolves a "record" reference to the entity it names. It
// accepts a meta.audiosilo.app work?id=/series?id=/person?id= URL, a GitHub blob
// URL, or a data-tree path (with or without a leading data/).
//
// The path forms it understands are the per-entity ones a submitter has always
// typed ("works/th/the-thing/work.json"). They are a REFERENCE SYNTAX, not a
// storage location - the tree has not looked like that since the pack migration,
// and refPath below is this package's own parsing of it, kept because it is what
// people write and what older issues, docs and links still say. compose_correct
// maps the result onto the pack entry the record actually lives in.
//
// ok is false when nothing recognizable can be extracted.
func resolveRecordRef(ref string) (rr recordRef, ok bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return recordRef{}, false
	}
	if strings.Contains(ref, "://") {
		u, err := url.Parse(ref)
		if err != nil {
			return recordRef{}, false
		}
		if id := u.Query().Get("id"); id != "" {
			slug, sok := sanitizeSlug(id)
			if !sok {
				return recordRef{}, false
			}
			switch {
			case strings.Contains(u.Path, "series"):
				return recordRef{kind: model.KindSeries, slug: slug}, true
			case strings.Contains(u.Path, "person"):
				return recordRef{kind: model.KindPerson, slug: slug}, true
			default: // work?id=
				return recordRef{kind: model.KindWork, slug: slug}, true
			}
		}
		ref = u.Path // fall through to path handling for blob URLs
	}
	// Trim a leading data/ (and any repo/blob/main prefix) to land on the
	// data-tree-relative portion.
	ref = strings.TrimPrefix(path.Clean(ref), "/")
	if i := strings.Index(ref, "data/"); i >= 0 {
		ref = ref[i+len("data/"):]
	}
	return refPath(ref)
}

// refPath parses the per-entity path forms a submitter may type. The shard
// directory is accepted but ignored: it addressed a file that no longer exists,
// and the slug is what resolves the record.
func refPath(rel string) (recordRef, bool) {
	parts := strings.Split(path.Clean(rel), "/")
	jsonName := func(s string) (string, bool) {
		if !strings.HasSuffix(s, ".json") || len(s) <= len(".json") {
			return "", false
		}
		return strings.TrimSuffix(s, ".json"), true
	}
	switch {
	case len(parts) == 4 && parts[0] == "works" && parts[3] == "work.json":
		return recordRef{kind: model.KindWork, slug: parts[2]}, true
	case len(parts) == 4 && parts[0] == "works" && parts[3] == "characters.json":
		return recordRef{kind: model.KindCharacters, slug: parts[2], workSlug: parts[2]}, true
	case len(parts) == 4 && parts[0] == "works" && parts[3] == "recaps.json":
		return recordRef{kind: model.KindRecaps, slug: parts[2], workSlug: parts[2]}, true
	case len(parts) == 5 && parts[0] == "works" && parts[3] == "recordings":
		if slug, ok := jsonName(parts[4]); ok {
			return recordRef{kind: model.KindRecording, slug: slug, workSlug: parts[2]}, true
		}
	case len(parts) == 3 && parts[0] == "people":
		if slug, ok := jsonName(parts[2]); ok {
			return recordRef{kind: model.KindPerson, slug: slug}, true
		}
	case len(parts) == 3 && parts[0] == "series":
		if slug, ok := jsonName(parts[2]); ok {
			return recordRef{kind: model.KindSeries, slug: slug}, true
		}
	}
	return recordRef{}, false
}
