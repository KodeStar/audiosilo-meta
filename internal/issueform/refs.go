package issueform

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
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
	// entityPageRE matches the entity PAGE paths metaserve serves the three
	// families at - /works/{slug}, /people/{slug}, /series/{slug} - the URL a
	// submitter copies out of their address bar, which the correct-data form
	// advertises as the easiest way to name a record. It is deliberately STRICT
	// where worksPathRE is loose: the whole path, and exactly ONE segment after
	// the family. That is what keeps it disjoint from the retired data-tree path
	// (works/<shard>/<slug>/work.json, which has more) and off /api/v1/works/{id},
	// which is an API endpoint rather than a page and resolves exactly as it did
	// before.
	entityPageRE = regexp.MustCompile(`^/?(works|people|series)/([^/]+)/?$`)
)

// pageKinds maps a page path's family segment onto the record kind it names. A
// RECORDING is deliberately absent: it is addressed inside its work and has no
// page of its own, so a recording still needs one of the reference forms that
// can name it (the data-tree path, or the recording forms' own fields).
var pageKinds = map[string]model.Kind{
	"works":  model.KindWork,
	"people": model.KindPerson,
	"series": model.KindSeries,
}

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
// one region vocabulary, common.schema.json #/$defs/region (which uses "uk",
// not "gb"). The KEYS are deliberately wider than the enum - that is the point
// of an alias table - but every VALUE must be an enum member, or a submission
// would compose a region metacheck rejects: TestRegionAliasesTargetTheSchemaEnum.
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
	for _, line := range splitLines(block) {
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

// parseISBNs parses the ISBN lines of a recording form field. A line is either a
// bare ISBN or "region: ISBN", and both spellings land in the same list - they
// identify one production in different marketplaces, which is exactly what
// recording.schema.json's isbn[] models.
//
// A bare ISBN is recorded with the region UNSTATED, and that asymmetry with
// parseASINs above is deliberate. parseASINs defaults a bare ASIN to "us"
// because an ASIN only exists inside a marketplace, so a region-less one is a
// missing prefix rather than a fact about the world - and dropping it would cost
// the recording its identity/dedup key. An ISBN is not marketplace-scoped:
// "region unknown" is an ordinary, truthful state the schema spells as the bare
// string, and inventing "us" for it would be a fabricated fact of exactly the
// kind the facts-only rule forbids.
//
// An unusable line is noted and skipped, matching parseASINs' tone: one bad line
// never fails a submission that is otherwise fine.
//
// The list is deduplicated BY VALUE (isbnKey), because the two spellings make it
// easy to state one identifier twice - "9781473647633" and "GB: 9781473647633"
// are the same ISBN - and composing both would write a record that is a
// duplicate of ITSELF, reported afterwards as a raw metacheck line rather than
// as anything the submitter could act on. When the two spellings differ, the
// SCOPED one wins: bare states no region, so keeping the region is the same
// "adds information, contradicts nothing" rule correctISBN's upgrade applies.
func (c *composer) parseISBNs(block string) []model.ISBNRef {
	var out []model.ISBNRef
	at := map[string]int{} // isbnKey -> index in out
	for _, line := range splitList(block) {
		entry, reason, ok := parseISBNLine(line)
		if !ok {
			c.note("%s - skipped", reason)
			continue
		}
		key := isbnKey(entry.ISBN)
		i, dup := at[key]
		if !dup {
			at[key] = len(out)
			out = append(out, entry)
			continue
		}
		if out[i].Region == "" && entry.Region != "" {
			c.note("ISBN %s is listed twice; keeping the one scoped to region %q", entry.ISBN, entry.Region)
			out[i] = entry
			continue
		}
		// Two DIFFERENT stated regions for one value is the contradiction
		// correctISBN escalates to a maintainer. A list field has to resolve it
		// somehow, so first wins - but the note names what was dropped, because
		// a region silently discarded is the thing the submitter would never
		// otherwise learn.
		if out[i].Region != "" && entry.Region != "" && out[i].Region != entry.Region {
			c.note("ISBN %s is listed under both %q and %q; keeping %q - one ISBN identifies one edition",
				entry.ISBN, out[i].Region, entry.Region, out[i].Region)
			continue
		}
		c.note("ISBN %s is listed more than once - the later line was dropped", entry.ISBN)
	}
	return out
}

// parseISBNLine reads one ISBN line in either spelling. reason is a rendered
// phrase naming what is wrong when ok is false; the caller decides whether that
// is a note (a list field skips the line) or a terminal verdict (a correction
// states exactly one value, so an unusable one is the whole submission).
//
// A value carrying several lines cannot get through here: NormalizeISBN strips
// whitespace and then anchors its pattern, so a second line makes the whole
// value fail to be an ISBN. parsePublisherLine has no such backstop - a
// publisher name is free text - and rejects the shape explicitly; the asymmetry
// is worth knowing about before either function is edited.
func parseISBNLine(line string) (entry model.ISBNRef, reason string, ok bool) {
	line = strings.TrimSpace(line)
	i := strings.IndexAny(line, ":\t")
	if i < 0 {
		isbn, valid := normalizeISBN(line)
		if !valid {
			return model.ISBNRef{}, fmt.Sprintf("value %q is not a valid ISBN", line), false
		}
		// Region deliberately left unstated - see parseISBNs.
		return model.ISBNRef{ISBN: isbn}, "", true
	}
	region, regionOK := normalizeRegion(line[:i])
	if !regionOK {
		return model.ISBNRef{}, fmt.Sprintf("region %q is not a known marketplace", strings.TrimSpace(line[:i])), false
	}
	isbn, valid := normalizeISBN(line[i+1:])
	if !valid {
		return model.ISBNRef{}, fmt.Sprintf("value %q is not a valid ISBN", strings.TrimSpace(line[i+1:])), false
	}
	return model.ISBNRef{Region: region, ISBN: isbn}, "", true
}

// parsePublisherLine reads one "region: Publisher Name" line. It is
// parseISBNLine's twin: one grammar, one place, and a reason string the caller
// renders as a note (a list field skips the line) or as a terminal verdict (a
// correction states exactly one value).
func parsePublisherLine(line string) (entry model.RegionPublisher, reason string, ok bool) {
	line = strings.TrimSpace(line)
	// A publisher name is free text, so a value holding several lines would weld
	// them into one corrupt name ("Hodder\nCA: Doubleday") that every later check
	// accepts. The list callers have already split on newlines, so anything left
	// here arrives from a correction - and the issue BODY is untrusted: the form
	// widget is single-line, but intake also runs on edited bodies and on issues
	// opened through the API.
	if strings.ContainsAny(line, "\n\r") {
		return model.RegionPublisher{}, "a correction states one \"region: Publisher Name\" - list several on the add-recording form", false
	}
	i := strings.IndexAny(line, ":\t")
	if i < 0 {
		return model.RegionPublisher{}, fmt.Sprintf("%q is not of the form \"region: Publisher Name\"", line), false
	}
	region, regionOK := normalizeRegion(line[:i])
	if !regionOK {
		return model.RegionPublisher{}, fmt.Sprintf("region %q is not a known marketplace", strings.TrimSpace(line[:i])), false
	}
	name := strings.TrimSpace(line[i+1:])
	if name == "" {
		return model.RegionPublisher{}, fmt.Sprintf("region %q is named with no publisher", region), false
	}
	return model.RegionPublisher{Region: region, Publisher: name}, "", true
}

// parsePublishers parses the regional-publisher lines of a recording form field
// into the recording's publishers[]. publisherOfRecord is the form's Publisher
// field.
//
// The three refusals are the schema's and pkg/check's own rules asked at COMPOSE
// time, so a submitter gets a verdict naming what is wrong instead of a raw
// metacheck line against a record the bot already wrote. They set a terminal
// status, which the caller sees through the package's usual c.failed(); a merely
// unusable line is noted and skipped, as everywhere else.
//
// The result is sorted by region - see sortRegionPublishers.
func (c *composer) parsePublishers(block, publisherOfRecord string) []model.RegionPublisher {
	var out []model.RegionPublisher
	seen := map[string]bool{}
	for _, line := range splitLines(block) {
		entry, reason, ok := parsePublisherLine(line)
		if !ok {
			c.note("regional publisher: %s - skipped", reason)
			continue
		}
		if seen[entry.Region] {
			c.fail(StatusInvalid, "region %q is listed twice under %s; one region names one imprint", entry.Region, fRecPublishers)
			return nil
		}
		if entry.Publisher == publisherOfRecord {
			c.fail(StatusInvalid, "%s lists %q, which is already the %s field; that list holds the OTHER regions' imprints of this same recording", fRecPublishers, entry.Publisher, fRecPublisher)
			return nil
		}
		seen[entry.Region] = true
		out = append(out, entry)
	}
	if len(out) > 0 && publisherOfRecord == "" {
		c.fail(StatusInvalid, "%s needs a %s: the schema requires a publisher of record before the other regions' imprints can be listed (\"the other regions\" has to be other than something)", fRecPublishers, fRecPublisher)
		return nil
	}
	sortRegionPublishers(out)
	return out
}

// sortRegionPublishers orders publishers[] by region. Nothing REQUIRES it -
// publishers[] is a JSON array, canonical formatting preserves array order
// rather than imposing one, and a hand-authored pull request may write any
// order - but every writer in this package sorts, so one set of imprints
// composed here always has one byte-form and a re-submission is a no-op diff.
// Regions are unique within one recording (pkg/check's
// checkRegionalPublishers), so the order is total.
func sortRegionPublishers(ps []model.RegionPublisher) {
	sort.Slice(ps, func(i, j int) bool { return ps[i].Region < ps[j].Region })
}

// splitLines splits a one-per-line block, trimming and dropping empties. Unlike
// splitList it does NOT split on commas: a publisher name legitimately contains
// one ("Houghton Mifflin Harcourt, Inc.").
func splitLines(block string) []string {
	var out []string
	for _, line := range strings.Split(block, "\n") {
		if v := strings.TrimSpace(line); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// resolveWorkRef resolves a "work" reference to a work slug. The shapes it
// accepts, in the order it tries them:
//
//   - a meta.audiosilo.app / GitHub URL carrying ?id=<slug> (the legacy page URL,
//     which the site still composes);
//   - the entity PAGE path /works/<slug>, absolute or bare, with or without a
//     query string or fragment - what metaserve serves a work at now, and what a
//     submitter copies out of the address bar;
//   - the retired data-tree path works/<shard>/<slug>/work.json, which older
//     issues, docs and GitHub blob links still say;
//   - a bare slug.
//
// ok is false when nothing slug-shaped can be extracted.
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
			// The ESCAPED path, so a %2F inside a segment cannot read as a
			// separator; workPageSlug does the one decode.
			if slug, ok := workPageSlug(u.EscapedPath()); ok {
				return slug, true
			}
			if m := worksPathRE.FindStringSubmatch(u.Path); m != nil {
				return sanitizeSlug(m[1])
			}
		}
		return "", false
	}
	// Only the PAGE reading is given the stripped form: the shapes below either
	// cannot carry a suffix or are the best-effort slugify, and narrowing the
	// strip to the one rule that needs it leaves them byte-identical.
	if slug, ok := workPageSlug(trimURLSuffix(ref)); ok {
		return slug, true
	}
	if m := worksPathRE.FindStringSubmatch(ref); m != nil {
		return sanitizeSlug(m[1])
	}
	return sanitizeSlug(ref)
}

// entityPageRef reads a path as one of the entity PAGE URLs. It is strict on
// purpose: exactly one segment after a known family, and that segment must
// already BE a slug once decoded. Anything else declines, so the caller falls
// through to the shapes it understood before - a loose reading here would claim
// /api/v1/works/{id} and the data-tree path, both of which mean something else.
func entityPageRef(p string) (recordRef, bool) {
	m := entityPageRE.FindStringSubmatch(p)
	if m == nil {
		return recordRef{}, false
	}
	seg, err := url.PathUnescape(m[2])
	if err != nil || !model.ValidSlug(seg) {
		return recordRef{}, false
	}
	return recordRef{kind: pageKinds[m[1]], slug: seg}, true
}

// workPageSlug reads a path as the WORK page URL /works/<slug> - the one family
// resolveWorkRef may return. It is the same rule as entityPageRef, asked of one
// family, rather than a second reading of the same URLs.
func workPageSlug(path string) (string, bool) {
	rr, ok := entityPageRef(path)
	if !ok || rr.kind != model.KindWork {
		return "", false
	}
	return rr.slug, true
}

// trimURLSuffix cuts a query string or fragment off a BARE (scheme-less)
// reference. url.Parse already does this for an absolute URL, and a submitter
// who pastes the path alone - "/works/the-thing?tab=chapters" - has to get the
// same reading: no path form below carries a "?" or a "#", and an unstripped one
// fails the strict page match and is then slugified into garbage
// (works-the-thing-tab-chapters) rather than declining.
func trimURLSuffix(ref string) string {
	if i := strings.IndexAny(ref, "?#"); i >= 0 {
		return ref[:i]
	}
	return ref
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
// accepts a meta.audiosilo.app work?id=/series?id=/person?id= URL, an entity
// PAGE URL (/works/<slug>, /people/<slug>, /series/<slug> - absolute or bare,
// which is what the correct-data form calls the easiest reference), a GitHub
// blob URL, or a data-tree path (with or without a leading data/).
//
// The path forms it understands are the per-entity ones a submitter has always
// typed ("works/th/the-thing/work.json"). They are a REFERENCE SYNTAX, not a
// storage location - the tree has not looked like that since the pack migration,
// and refPath below is this package's own parsing of it, kept because it is what
// people write and what older issues, docs and links still say. compose_correct
// maps the result onto the pack entry the record actually lives in.
//
// A RECORDING has no page URL to copy - it is addressed inside its work - so it
// still needs one of the older forms, unchanged.
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
		// The ESCAPED path, so a %2F inside a segment cannot read as a separator;
		// entityPageRef does the one decode.
		if rr, ok := entityPageRef(u.EscapedPath()); ok {
			return rr, true
		}
		ref = u.Path // fall through to path handling for blob URLs
	}
	// Trim a leading data/ (and any repo/blob/main prefix) to land on the
	// data-tree-relative portion.
	ref = strings.TrimPrefix(path.Clean(ref), "/")
	if i := strings.Index(ref, "data/"); i >= 0 {
		ref = ref[i+len("data/"):]
	}
	// The bare page path, e.g. "people/andy-weir" - the same rule the URL branch
	// asked, for a submitter who pasted the path alone. It is tried before
	// refPath because refPath's shapes all carry more segments, so the two cannot
	// both match.
	if rr, ok := entityPageRef(trimURLSuffix(ref)); ok {
		return rr, true
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
