package issueform

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// Field labels for add-work.yml. Kept in one place so a template label edit is a
// single change here. The labels mirror .github/ISSUE_TEMPLATE/add-work.yml.
const (
	fWorkTitle          = "Title"
	fWorkSubtitle       = "Subtitle"
	fWorkAuthors        = "Author(s)"
	fWorkLanguage       = "Language"
	fWorkFirstPublished = "First published (year)"
	fWorkGenres         = "Genres"
	fWorkSeriesName     = "Series name"
	fWorkSeriesPosition = "Series position"
	fWorkISBN           = "ISBN(s)"
	fWorkWikidata       = "Wikidata ID"
	fWorkOpenLibrary    = "Open Library ID"

	fRecNarrators  = "Narrator(s)"
	fRecAbridged   = "Abridged?"
	fRecRuntime    = "Runtime (minutes)"
	fRecRelease    = "Release date"
	fRecPublisher  = "Publisher"
	fRecPublishers = "Regional publisher(s)"
	fRecASINs      = "ASIN(s) with region"
	fRecISBNs      = "Audiobook ISBN(s)"
	fRecCoverURL   = "Cover image URL"

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
	if c.refuseAIAuthors(authorNames) {
		return
	}
	narratorNames := splitNarratorNames(s.get(fRecNarrators))
	if len(narratorNames) == 0 {
		c.fail(StatusInvalid, "at least one Narrator is required")
		return
	}
	sourceRef := s.get(fSources)
	if sourceRef == "" {
		c.fail(StatusInvalid, "Sources is required for provenance")
		return
	}
	genres, genresOK := c.parseGenres(s.get(fWorkGenres))
	if !genresOK {
		return
	}

	// Dedup before writing anything: an existing ASIN/ISBN or work slug means
	// this book (or edition) is already in the catalog.
	asins := c.parseASINs(s.get(fRecASINs))
	recISBNs := c.parseISBNs(s.get(fRecISBNs))
	// An ASIN naming a mirror-seed recording is a planned takeover (takeover.go),
	// which also stops the compose.
	t, dup := c.dedupIdentifiers(asins, recISBNs, "; use the Add a recording form for another narration")
	if t != nil {
		c.applyTakeover(s, t, genres, "")
	}
	if dup {
		return
	}
	// Parsed here rather than inside emitRecording so a refusal lands before any
	// record is composed - the verdict is about the form, not about half a work.
	publishers := c.parsePublishers(s.get(fRecPublishers), s.get(fRecPublisher))
	if c.failed() {
		return
	}

	// The two title-side gates, before the slug is derived from the title
	// (dupidentity.go). Order is most-specific-first:
	//
	//  1. a title stating a KNOWN series and a volume number the catalogue already
	//     fills names that member, and saying so is more use to a submitter than any
	//     later gate's message;
	//  2. otherwise the decoration is stripped, which is what lets the existing
	//     work-SLUG gate below see the collision at all - a decorated title slugs to
	//     an address no work occupies, which is exactly how a second record of a
	//     catalogued book used to get composed.
	// One series row for every lookup the submission makes, so the gates and the
	// placement judge the same authors.
	seriesRow := c.formSeriesRow(s)
	ctx := c.titleContextFor(title, s.get(fWorkSeriesName), seriesRow)
	if c.checkSeriesVolume(ctx) {
		return
	}
	ctx, titleOK := c.checkDecoratedTitle(ctx)
	if !titleOK {
		return
	}
	title = ctx.title

	workSlug := slugify(title)
	if workSlug == "" {
		c.fail(StatusInvalid, "Title %q produced an empty slug", title)
		return
	}
	if model.IsReservedSlug(workSlug) {
		// A work stored at an API route literal is unreachable through
		// /api/v1/works/{id}, so it steps onto the author-suffixed slug the bulk
		// importer would mint for it - the same formula, so the two composers
		// cannot put one book in two places.
		stepped := c.unreservedWorkSlug(workSlug, authorNames[0])
		c.note("work slug %q is reserved for an API route - using %q", workSlug, stepped)
		workSlug = stepped
	}
	workSlug, slugOK := c.gateWorkSlug(workSlug, authorNames)
	if !slugOK {
		return
	}

	// People.
	authorSlugs := c.slugsFor(authorNames, sourceRef)
	narratorSlugs := c.slugsFor(narratorNames, sourceRef)
	if c.failed() {
		return
	}

	// The identity gate needs the author SLUGS (the identity the catalogue records
	// people by), so it sits just after they are resolved - and before the work
	// record is queued. The person records it may have created are harmless: a
	// person the submission names is real whether or not this work is composed, and
	// the terminal verdict means nothing is written at all (Process discards a failed
	// submission's queued entries).
	if c.checkNormalizedIdentity(ctx, lang, authorSlugs) {
		return
	}

	// Work record. added_at is stamped here because this path CREATES the work;
	// no other issue-form path writes the field.
	work := outWork{
		ID: workSlug, Title: title, Subtitle: s.get(fWorkSubtitle),
		Authors: authorSlugs, Language: lang, Genres: genres,
		AddedAt: c.date, License: licenseCC0,
		Sources: c.sources(sourceRef),
	}
	if yr := s.get(fWorkFirstPublished); yr != "" {
		if dateYearRE.MatchString(yr) {
			work.FirstPublished = yr
		} else {
			c.note("First published %q is not a valid year - dropped", yr)
		}
	}
	work.Xref = c.buildWorkXref(s)
	// putNewEntry, not putEntry: c.works comes from a best-effort catalogue load,
	// so a work the loader could not decode passes the duplicate check above, and
	// a plain upsert would replace its whole composite entry - recordings and all.
	if !c.putNewEntry(pack.FamilyWorks, workSlug, work) {
		return
	}

	// First recording.
	c.emitRecording(workSlug, lang, narratorSlugs, asins, recISBNs, publishers, s, sourceRef)

	// Optional series placement.
	c.placeInSeries(s, seriesRow, workSlug, sourceRef)
}

// unreservedWorkSlug steps a reserved title slug off the route literal, exactly
// as the bulk importer's candidate chain does: the title plus its first author's
// slug (importer.AuthorSuffixedWorkSlug, the one bounded formula), falling back
// to the numeric candidate when that author has no addressable slug of their own
// - which is the same last resort the chain ends in. A retired author slug is
// read as its survivor, as the importer's chain reads the author it resolved.
func (c *composer) unreservedWorkSlug(base, firstAuthor string) string {
	if slug, ok := c.personSlugOf(firstAuthor); ok {
		return importer.AuthorSuffixedWorkSlug(base, slug)
	}
	return importer.NumberedSlugAt(base, 1)
}

// gateWorkSlug is the WORK-SLUG gate: it returns the slug the work is composed
// at, or ok=false with the verdict set.
//
// A title's slug held by another work is a duplicate only when that work is the
// SUBMITTING author's (sameAuthorAs - the importer's same-person rule, so "J.
// Doe" meets "Jane Doe"). A CLEARLY different author's book of the same title is
// another book: it steps to the author-suffixed slug, the bulk chain's next
// candidate (unreservedWorkSlug, the formula a reserved title steps by), so the
// form and an import put it in one place. An author NEAR the incumbent's
// (nearAuthorOf - a likely misspelling) and two other authors' books on both
// candidates are a maintainer's call.
//
// A duplicate is tier-aware like the ASIN/ISBN and narrator-set gates: a work
// only the mirror has ever stated routes to a maintainer, not to a closed
// duplicate (failDuplicateWork). A retired slug meets its survivor here
// (internal/importer/tombstone.go).
func (c *composer) gateWorkSlug(slug string, authorNames []string) (string, bool) {
	live := c.liveWorkSlug(slug)
	if w := c.works[live]; w != nil && !c.sameAuthorAs(w, authorNames) {
		// Stepping past is only safe when the authors are CLEARLY different: a
		// misspelled author ("Brandon Sandersen") must not compose a second record
		// of the book at the suffixed slug.
		if c.nearAuthorOf(w, authorNames) {
			c.fail(StatusNeedsHuman, "the work slug %q is held by %q by %s at %s, and your submission names %s - "+
				"the same author spelled differently, or another author? A maintainer decides (if it is a "+
				"misspelling, correcting the author makes this a duplicate; if not, the book needs its own slug)",
				slug, w.Title, c.personNames(w.Authors), c.entryLocation(pack.FamilyWorks, live, ""),
				strings.Join(authorNames, ", "))
			return "", false
		}
		stepped := c.unreservedWorkSlug(slug, authorNames[0])
		if other := c.works[c.liveWorkSlug(stepped)]; other != nil && !c.sameAuthorAs(other, authorNames) {
			c.fail(StatusNeedsHuman, "the work slug %q and its author-suffixed form %q are both held by other "+
				"authors' books; a maintainer chooses this book's slug", slug, stepped)
			return "", false
		}
		c.note("the work slug %q is held by %q by %s, another author's book of the same title - "+
			"this one is composed at %q", slug, w.Title, c.personNames(w.Authors), stepped)
		slug, live = stepped, c.liveWorkSlug(stepped)
	}
	if live == "" {
		return slug, true
	}
	lead := "a work already exists"
	if live != slug {
		lead = fmt.Sprintf("the work slug %q was retired by a merge onto the work", slug)
	}
	c.failDuplicateWork(live, "its title", "%s at %s; use the Add a recording form to add another narration",
		lead, c.entryLocation(pack.FamilyWorks, live, ""))
	return "", false
}

// sameAuthorAs reports whether any of the submitted author names may be one of
// w's authors, by the importer's same-person rule (importer.SamePerson). A work
// or a submission with no author to compare reads as the same author - the
// conservative answer, which keeps the duplicate verdict.
func (c *composer) sameAuthorAs(w *model.Work, names []string) bool {
	if len(w.Authors) == 0 || len(names) == 0 {
		return true
	}
	for _, id := range w.Authors {
		for _, n := range names {
			if slug, _ := c.personSlugOf(n); importer.SamePerson(id, c.nameOf(id), slug, n) {
				return true
			}
		}
	}
	return false
}

// nearAuthorOf reports whether any submitted author is NEAR one of w's authors
// without being the same person (importer.NearPerson: the same surname, or one
// edit apart) - a name the gate cannot tell a misspelling from another author by.
func (c *composer) nearAuthorOf(w *model.Work, names []string) bool {
	for _, id := range w.Authors {
		for _, n := range names {
			if importer.NearPerson(c.nameOf(id), n) {
				return true
			}
		}
	}
	return false
}

// nameOf is a catalogued person's recorded name, or the id itself when the
// catalogue holds no such record. The index is built on first use: only the
// work-slug gate and a few messages ask.
func (c *composer) nameOf(id string) string {
	if c.personName == nil {
		c.personName = map[string]string{}
		if c.catalog != nil {
			for _, p := range c.catalog.People {
				c.personName[p.ID] = p.Name
			}
		}
	}
	if name, ok := c.personName[id]; ok {
		return name
	}
	return id
}

// personNames renders person ids as their recorded names, joined for a message.
func (c *composer) personNames(ids []string) string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, c.nameOf(id))
	}
	return strings.Join(out, ", ")
}

// slugsFor resolves a list of person names to slugs, creating person records,
// and deduplicates BY SLUG in first-seen order (mirroring
// importer.creditSlugs). The slug is the identity, so two spellings of one
// person on the same form ("Stan Lee" and "Created by Stan Lee", which credit
// cleaning collapses onto one name) are one credit - listing the slug twice
// would compose a record whose authors/narrators array repeats itself, which
// the schema's uniqueItems now rejects outright.
func (c *composer) slugsFor(names []string, sourceRef string) []string {
	out := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		slug, ok := c.getOrCreatePerson(name, sourceRef)
		if !ok {
			return out
		}
		if seen[slug] {
			continue
		}
		seen[slug] = true
		out = append(out, slug)
	}
	return out
}

// refuseAIAuthors refuses a submission whose AUTHOR list names an AI - a
// synthetic voice or a generative system - and reports whether it did.
//
// It is the intake door's half of the rule the bulk importer applies at
// refuseAIBooks, asked through the same vocabulary (importer.AICreditReason).
// The NARRATION decision does not reach here: a synthetic narration is admitted
// and folded (splitNarratorNames), and an author credit is a different claim -
// "this system wrote the book" - which the catalogue does not record against a
// person record, whichever door it arrives at.
//
// It is also what RESERVES the canonical synthetic slug on this path: the
// vocabulary that folds a narrator credit spelled "Virtual Voice" is the one
// that refuses an author credit spelled the same way, so no form can write that
// address for anybody real.
//
// StatusInvalid rather than needs-human: the verdict is settled policy, not an
// undecidable pair, and the message says what the catalogue will take.
func (c *composer) refuseAIAuthors(names []string) bool {
	for _, name := range names {
		if why, isAI := importer.AICreditReason(name); isAI {
			c.fail(StatusInvalid, "author %q is %s, and the catalogue credits works to people - "+
				"name the human author, or a maintainer will decide how to record this book", name, why)
			return true
		}
	}
	return false
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
func (c *composer) emitRecording(workSlug, lang string, narratorSlugs []string, asins []outASIN, isbns []model.ISBNRef, publishers []model.RegionPublisher, s sections, sourceRef string) {
	recSlug := c.uniqueRecordingSlug(workSlug, narratorSlugs, s.get(fRecRelease))
	rec := outRecording{
		ID: recSlug, Work: workSlug, Narrators: narratorSlugs, Language: lang,
		Abridged: abridgedFromForm(s.get(fRecAbridged)),
		License:  licenseCC0, Sources: c.sources(sourceRef),
	}
	rec.RuntimeMin, rec.ReleaseDate, rec.CoverURL = c.recordingFacts(s)
	rec.Publisher = s.get(fRecPublisher)
	if len(publishers) > 0 {
		rec.Publishers = publishers
	}
	if len(asins) > 0 {
		rec.ASIN = asins
	}
	if len(isbns) > 0 {
		rec.ISBN = isbns
	}
	// Both issue-form recording paths (a work's first recording and an added
	// narration) create a recording, so both stamp added_at.
	rec.AddedAt = c.date
	c.putRecording(workSlug, recSlug, rec)
}

// recordingFacts reads the three recording facts a form states as free text -
// runtime, release date and cover URL - dropping (with a note) a value that is
// not well-formed. It is shared by the compose path and the takeover
// (takeover.go), so a stated value means the same whichever of the two uses it.
func (c *composer) recordingFacts(s sections) (runtimeMin int, releaseDate, coverURL string) {
	if rt := s.get(fRecRuntime); rt != "" {
		if n, err := strconv.Atoi(rt); err == nil && n > 0 {
			runtimeMin = n
		} else {
			c.note("Runtime %q is not a positive whole number of minutes - dropped", rt)
		}
	}
	if rd := s.get(fRecRelease); rd != "" {
		if dateFlexRE.MatchString(rd) {
			releaseDate = rd
		} else {
			c.note("Release date %q is not YYYY, YYYY-MM, or YYYY-MM-DD - dropped", rd)
		}
	}
	if cover := s.get(fRecCoverURL); cover != "" {
		if strings.HasPrefix(cover, "https://") {
			coverURL = cover
		} else {
			c.note("Cover image URL %q must start with https:// - dropped", cover)
		}
	}
	return runtimeMin, releaseDate, coverURL
}

// putRecording splices a recording into its work's composite entry. The entry is
// read queued-write-first, so the recording composed right after a brand-new
// work lands in the entry that work is still queued as.
func (c *composer) putRecording(workSlug, recSlug string, rec any) {
	entry, found, ok := c.entryRaw(pack.FamilyWorks, workSlug)
	if !ok {
		return
	}
	if !found {
		c.fail(StatusInvalid, "work %q has no entry to attach a recording to", workSlug)
		return
	}
	if err := pack.SetRecording(entry, recSlug, rec); err != nil {
		c.fail(StatusInvalid, "works entry %q: %v", workSlug, err)
		return
	}
	c.putEntry(pack.FamilyWorks, workSlug, entry)
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
// release year, and disambiguates against a work's existing recordings. It
// composes the same bounded chain the bulk importer's addRecording walks
// (importer.BoundedSlugTail / importer.NumberedSlugAt), so a long full-cast or
// corporate narrator credit yields a valid id here too rather than an over-long
// one the composer's own validation would then reject.
func (c *composer) uniqueRecordingSlug(workSlug string, narratorSlugs []string, releaseDate string) string {
	base := "unknown-narrator"
	if len(narratorSlugs) > 0 && narratorSlugs[0] != "" {
		base = narratorSlugs[0]
	}
	if yr := importer.YearOf(releaseDate); yr != "" {
		base = importer.BoundedSlugTail(base, "-"+yr)
	}
	existing := map[string]bool{}
	if w := c.works[workSlug]; w != nil {
		for _, r := range w.Recordings {
			existing[r.ID] = true
		}
	}
	for i := 0; ; i++ {
		if slug := importer.NumberedSlugAt(base, i); !existing[slug] {
			return slug
		}
	}
}

// seriesSlugOf is the slug a form's series name addresses: the importer's first
// series candidate (SeriesSlugAt, which steps "Latest" onto latest-2), so both
// writers resolve a name to one slug. base is the unstepped slug, for the note.
func seriesSlugOf(name string) (slug, base string) {
	base = slugify(name)
	return importer.SeriesSlugAt(base, 0), base
}

// formSeries is what a form's series name addresses (seriesForForm).
type formSeries struct {
	// slug is the name's first candidate (seriesSlugOf) and base the unstepped
	// slug, for the reserved-literal note. A new series is minted at slug.
	slug, base string
	// rec is the series the name resolves to and id where it is stored; via is
	// the retired base slug it was reached through, or "".
	rec     *model.Series
	id, via string
	// taken is set when rec is nil, no same-named series was stepped past, and
	// slug is held by a differently-named series - a maintainer's call: the form
	// mints a numbered series only for the author case below, where the
	// importer's own rule has decided the name belongs to other authors.
	taken *model.Series
	// otherAuthors are the same-named series the submission cannot join - other
	// authors' (importer seriesauthors.go) or in another language (importer
	// seriesresolve.go) - set only when none on the chain fits, and why is the
	// importer's own account of it (importer.SeriesMatch.Why); slug is then the
	// first free chain slug, where the importer would mint the submitting
	// author's own series and the form composes it too.
	otherAuthors []string
	why          string
}

// seriesForForm resolves a form's series name for the submission row - the one
// lookup placeInSeries places by and titleContextFor's gates read, so both see
// the same record. The name is FOUND by the importer's own resolution
// (importer.SeriesAuthorIndex.Resolve: a retired base reaches its survivor, a
// differently-named holder is stepped past and a later candidate carrying the
// name answers) under the importer's own AUTHOR rule (a same-named series of
// other authors is not joined), so the form and the importer agree which series
// a name is for a given book. When the chain holds no series of that name the
// form stays conservative: a slug held by another series is taken, not stepped.
func (c *composer) seriesForForm(name string, row *importer.SeriesRow) formSeries {
	if fs, ok := c.formSeries[name]; ok {
		return fs
	}
	fs := formSeries{}
	fs.slug, fs.base = seriesSlugOf(name)
	if fs.slug != "" {
		stored := func(slug string) (string, bool) {
			s := c.series[slug]
			if s == nil {
				return "", false
			}
			return s.Name, true
		}
		switch m := c.seriesIndex().Resolve(name, c.redirects, stored, row); {
		case m.Found:
			fs.rec, fs.id, fs.via = c.series[m.Slug], m.Slug, m.Via
		case len(m.Stepped) > 0:
			fs.otherAuthors, fs.why, fs.slug = m.Stepped, m.Why(), m.Slug
		default:
			fs.taken = c.series[fs.slug]
		}
	}
	if c.formSeries == nil {
		c.formSeries = map[string]formSeries{}
	}
	c.formSeries[name] = fs
	return fs
}

// formSeriesRow is the submission as the series resolution reads it, through
// the importer's one row builder: the form's authors (each at the person slug it
// resolves to - a retired one at its survivor when the catalogue holds it, the
// rule every other lookup here applies), its title spellings, its publisher of
// record and its language.
func (c *composer) formSeriesRow(s sections) *importer.SeriesRow {
	slugOf := func(name string) string {
		slug, _ := c.personSlugOf(name)
		return slug
	}
	lang, _ := normalizeLanguage(s.get(fWorkLanguage)) // an invalid one is refused before any lookup
	return importer.SeriesRowFor(splitNames(s.get(fWorkAuthors)), []string{s.get(fWorkTitle), s.get(fWorkSubtitle)}, s.get(fRecPublisher), lang, slugOf)
}

// placeInSeries adds the work to the named series (creating it or extending an
// existing one). It is a no-op when no series name is given.
func (c *composer) placeInSeries(s sections, row *importer.SeriesRow, workSlug, sourceRef string) {
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
	fs := c.seriesForForm(name, row)
	if fs.slug == "" {
		c.note("series name %q produced an empty slug - work not placed in the series", name)
		return
	}
	if len(fs.otherAuthors) > 0 {
		// Every same-named series on the chain belongs to other authors or is in
		// another language, so this work does not squat their slots: it starts
		// its own series at the next free candidate, exactly where the bulk
		// importer would.
		c.note("series %q at %s - composed a new series at %q", name, fs.why, fs.slug)
	}
	if model.IsReservedSlug(fs.base) {
		c.note("series slug %q is reserved for an API route - using %q", fs.base, fs.slug)
	}
	if fs.taken != nil {
		c.fail(StatusNeedsHuman, "series slug %q already belongs to %q - a maintainer must resolve the series for %q", fs.slug, fs.taken.Name, name)
		return
	}
	if fs.rec != nil {
		switch {
		case fs.via != "":
			c.noteRetired(model.RedirectSeries, fs.via, fs.id)
		case fs.rec.Name != name:
			// The importer's run-level note, for the one submission: a join the
			// name alone does not show (a respelled qualifier, a renamed edition
			// series, a plain name reaching its language's edition).
			c.note("series %q joined %s %q", name, fs.id, fs.rec.Name)
		}
		c.extendSeries(fs.rec, fs.id, workSlug, pos)
		return
	}

	// New series entry with this one work.
	c.putNewEntry(pack.FamilySeries, fs.slug, outSeries{
		ID: fs.slug, Name: name, License: licenseCC0,
		Works:   []outSeriesWork{{Work: workSlug, Position: pos}},
		Sources: c.sources(sourceRef),
	})
}

// extendSeries appends the work to an existing series entry, preserving every
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
	obj, found, ok := c.entryRaw(pack.FamilySeries, seriesSlug)
	if !ok {
		return
	}
	if !found {
		c.fail(StatusInvalid, "series %q is in the catalogue but has no entry to extend", seriesSlug)
		return
	}
	works, _ := obj["works"].([]any)
	obj["works"] = append(works, map[string]any{"work": workSlug, "position": pos})
	c.putEntry(pack.FamilySeries, seriesSlug, obj)
}
