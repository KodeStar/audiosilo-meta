package issueform

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/internal/model"
)

// Field labels for add-work.yml. Kept in one place so a template label edit is a
// single change here. The labels mirror .github/ISSUE_TEMPLATE/add-work.yml.
const (
	fWorkTitle          = "Title"
	fWorkSubtitle       = "Subtitle"
	fWorkAuthors        = "Author(s)"
	fWorkLanguage       = "Language"
	fWorkFirstPublished = "First published (year)"
	fWorkSeriesName     = "Series name"
	fWorkSeriesPosition = "Series position"
	fWorkISBN           = "ISBN(s)"
	fWorkWikidata       = "Wikidata ID"
	fWorkOpenLibrary    = "Open Library ID"

	fRecNarrators = "Narrator(s)"
	fRecAbridged  = "Abridged?"
	fRecRuntime   = "Runtime (minutes)"
	fRecRelease   = "Release date"
	fRecPublisher = "Publisher"
	fRecASINs     = "ASIN(s) with region"
	fRecISBNs     = "Audiobook ISBN(s)"
	fRecCoverURL  = "Cover image URL"

	fSources = "Sources"
	fCC0     = "Public domain dedication"
)

// addWork composes a work, its first recording, any new people, and optional
// series placement from an add-work submission.
func (c *composer) addWork(s sections) {
	if !s.checked(fCC0) {
		c.fail(StatusInvalid, "the CC0 public-domain dedication checkbox is not ticked")
		return
	}

	title := s.get(fWorkTitle)
	if title == "" {
		c.fail(StatusInvalid, "Title is required")
		return
	}
	lang, langOK := normalizeLanguage(s.get(fWorkLanguage))
	if !langOK {
		c.fail(StatusInvalid, "Language %q is not a valid BCP-47 code (e.g. en, en-gb)", s.get(fWorkLanguage))
		return
	}
	authorNames := splitNames(s.get(fWorkAuthors))
	if len(authorNames) == 0 {
		c.fail(StatusInvalid, "at least one Author is required")
		return
	}
	narratorNames := splitNames(s.get(fRecNarrators))
	if len(narratorNames) == 0 {
		c.fail(StatusInvalid, "at least one Narrator is required")
		return
	}
	sourceRef := s.get(fSources)
	if sourceRef == "" {
		c.fail(StatusInvalid, "Sources is required for provenance")
		return
	}

	// Dedup before writing anything: an existing ASIN/ISBN or work slug means
	// this book (or edition) is already in the catalog.
	asins := c.parseASINs(s.get(fRecASINs))
	recISBNs := c.parseISBNs(s.get(fRecISBNs))
	if c.dedupIdentifiers(asins, recISBNs, "; use the Add a recording form for another narration") {
		return
	}

	workSlug := slugify(title)
	if workSlug == "" {
		c.fail(StatusInvalid, "Title %q produced an empty slug", title)
		return
	}
	if _, exists := c.works[workSlug]; exists {
		c.fail(StatusDuplicate, "a work already exists at %s; use the Add a recording form to add another narration", "data/"+path.Join("works", model.Shard(workSlug), workSlug, "work.json"))
		return
	}

	// People.
	authorSlugs := c.slugsFor(authorNames, sourceRef)
	narratorSlugs := c.slugsFor(narratorNames, sourceRef)
	if c.status == StatusInvalid {
		return
	}

	// Work record.
	work := outWork{
		ID: workSlug, Title: title, Subtitle: s.get(fWorkSubtitle),
		Authors: authorSlugs, Language: lang, License: licenseCC0,
		Sources: []outSource{c.source(sourceRef)},
	}
	if yr := s.get(fWorkFirstPublished); yr != "" {
		if dateYearRE.MatchString(yr) {
			work.FirstPublished = yr
		} else {
			c.note("First published %q is not a valid year - dropped", yr)
		}
	}
	work.Xref = c.buildWorkXref(s)
	c.emit(path.Join("works", model.Shard(workSlug), workSlug, "work.json"), work)

	// First recording.
	c.emitRecording(workSlug, lang, narratorSlugs, asins, recISBNs, s, sourceRef)

	// Optional series placement.
	c.placeInSeries(s, workSlug, sourceRef)
}

// slugsFor resolves a list of person names to slugs, creating person records.
func (c *composer) slugsFor(names []string, sourceRef string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		slug, ok := c.getOrCreatePerson(name, sourceRef)
		if !ok {
			return out
		}
		out = append(out, slug)
	}
	return out
}

// buildWorkXref assembles the work xref from the optional identifier fields,
// dropping (with a note) any that fail their schema pattern.
func (c *composer) buildWorkXref(s sections) *outWorkXref {
	xref := &outWorkXref{}
	if q := s.get(fWorkWikidata); q != "" {
		if wikidataRE.MatchString(q) {
			xref.Wikidata = q
		} else {
			c.note("Wikidata ID %q is not of the form Q123 - dropped", q)
		}
	}
	if ol := s.get(fWorkOpenLibrary); ol != "" {
		if olWorkRE.MatchString(ol) {
			xref.Openlibrary = ol
		} else {
			c.note("Open Library ID %q is not of the form OL123W - dropped", ol)
		}
	}
	for _, raw := range splitList(s.get(fWorkISBN)) {
		if isbn, ok := normalizeISBN(raw); ok {
			xref.ISBN = append(xref.ISBN, isbn)
		} else {
			c.note("work ISBN %q is not valid - dropped", raw)
		}
	}
	if xref.Wikidata == "" && xref.Openlibrary == "" && len(xref.ISBN) == 0 {
		return nil
	}
	return xref
}

// emitRecording composes and queues the recording record for a work.
func (c *composer) emitRecording(workSlug, lang string, narratorSlugs []string, asins []outASIN, isbns []string, s sections, sourceRef string) {
	recSlug := c.uniqueRecordingSlug(workSlug, narratorSlugs, s.get(fRecRelease))
	rec := outRecording{
		ID: recSlug, Work: workSlug, Narrators: narratorSlugs, Language: lang,
		Abridged: abridgedFromForm(s.get(fRecAbridged)),
		License:  licenseCC0, Sources: []outSource{c.source(sourceRef)},
	}
	if rt := s.get(fRecRuntime); rt != "" {
		if n, err := strconv.Atoi(rt); err == nil && n > 0 {
			rec.RuntimeMin = n
		} else {
			c.note("Runtime %q is not a positive whole number of minutes - dropped", rt)
		}
	}
	if rd := s.get(fRecRelease); rd != "" {
		if dateFlexRE.MatchString(rd) {
			rec.ReleaseDate = rd
		} else {
			c.note("Release date %q is not YYYY, YYYY-MM, or YYYY-MM-DD - dropped", rd)
		}
	}
	rec.Publisher = s.get(fRecPublisher)
	if len(asins) > 0 {
		rec.ASIN = asins
	}
	if len(isbns) > 0 {
		rec.ISBN = isbns
	}
	if cover := s.get(fRecCoverURL); cover != "" {
		if strings.HasPrefix(cover, "https://") {
			rec.CoverURL = cover
		} else {
			c.note("Cover image URL %q must start with https:// - dropped", cover)
		}
	}
	c.emit(recordingPath(workSlug, recSlug), rec)
}

// abridgedFromForm maps the rec_abridged dropdown to the recording's tri-state
// abridged field: "Abridged" -> true, "Unabridged" -> false, and "Unknown" (the
// default) / empty / anything else -> nil, so the field is omitted rather than
// fabricated. This honors the schema's omit-never-guess rule for abridged.
func abridgedFromForm(v string) *bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "abridged":
		t := true
		return &t
	case "unabridged":
		f := false
		return &f
	default:
		return nil
	}
}

// uniqueRecordingSlug derives a recording slug from the first narrator plus the
// release year, and disambiguates against a work's existing recordings.
func (c *composer) uniqueRecordingSlug(workSlug string, narratorSlugs []string, releaseDate string) string {
	base := "unknown-narrator"
	if len(narratorSlugs) > 0 && narratorSlugs[0] != "" {
		base = narratorSlugs[0]
	}
	if yr := importer.YearOf(releaseDate); yr != "" {
		base += "-" + yr
	}
	existing := map[string]bool{}
	if w := c.works[workSlug]; w != nil {
		for _, r := range w.Recordings {
			existing[r.ID] = true
		}
	}
	slug := base
	for i := 2; existing[slug]; i++ {
		slug = base + "-" + strconv.Itoa(i)
	}
	return slug
}

// placeInSeries adds the work to the named series (creating it or extending an
// existing one). It is a no-op when no series name is given.
func (c *composer) placeInSeries(s sections, workSlug, sourceRef string) {
	name := s.get(fWorkSeriesName)
	if name == "" {
		return
	}
	posRaw := s.get(fWorkSeriesPosition)
	if posRaw == "" {
		c.note("series %q given without a position - work not placed in the series", name)
		return
	}
	pos, ok := normalizeSequence(posRaw)
	if !ok {
		c.note("series position %q is not a number or omnibus range - work not placed in the series", posRaw)
		return
	}
	seriesSlug := slugify(name)
	if seriesSlug == "" {
		c.note("series name %q produced an empty slug - work not placed in the series", name)
		return
	}

	if existing, ok := c.series[seriesSlug]; ok {
		if !strings.EqualFold(existing.Name, name) {
			c.fail(StatusNeedsHuman, "series slug %q already belongs to %q - a maintainer must resolve the series for %q", seriesSlug, existing.Name, name)
			return
		}
		c.extendSeries(existing, seriesSlug, workSlug, pos)
		return
	}

	// New series file with this one work.
	c.emit(path.Join("series", model.Shard(seriesSlug), seriesSlug+".json"), outSeries{
		ID: seriesSlug, Name: name, License: licenseCC0,
		Works:   []outSeriesWork{{Work: workSlug, Position: pos}},
		Sources: []outSource{c.source(sourceRef)},
	})
}

// extendSeries appends the work to an existing series file, preserving every
// field the form does not manage.
func (c *composer) extendSeries(existing *model.Series, seriesSlug, workSlug, pos string) {
	for _, sw := range existing.Works {
		if sw.Work == workSlug {
			c.note("series %q already lists %q - not re-added", existing.Name, workSlug)
			return
		}
		if sw.Position == pos {
			c.fail(StatusNeedsHuman, "series %q position %q is already taken by %q - a maintainer must resolve it", existing.Name, pos, sw.Work)
			return
		}
	}
	rel := path.Join("series", model.Shard(seriesSlug), seriesSlug+".json")
	raw, err := os.ReadFile(filepath.Join(c.dataDir, filepath.FromSlash(rel)))
	if err != nil {
		c.fail(StatusInvalid, "read existing series %s: %v", rel, err)
		return
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		c.fail(StatusInvalid, "parse existing series %s: %v", rel, err)
		return
	}
	works, _ := obj["works"].([]any)
	obj["works"] = append(works, map[string]any{"work": workSlug, "position": pos})
	c.writeRaw(rel, obj)
}
