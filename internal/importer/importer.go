// Package importer converts external audiobook-library exports into
// audiosilo-meta records on disk. It maps one OpenAudible books.json entry to a
// work + recording (+ people, + series), deduplicating against the existing
// catalog so a contributor's upload lands as a reviewable diff. Only factual
// fields are imported (see LICENSING.md); publisher copy and covers-as-files are
// never touched.
package importer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/canonical"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

var (
	asinPattern = regexp.MustCompile(`^[A-Z0-9]{10}$`)
	datePattern = regexp.MustCompile(`^\d{4}(-\d{2}(-\d{2})?)?$`)
	// editionMarkerRE matches one or more stacked trailing (Unabridged)/(Abridged)
	// edition markers (parens or brackets), with their surrounding whitespace, so a
	// work title carries no edition decoration - unabridged-ness lives on the
	// recording's tri-state abridged flag, not in the work's identity.
	editionMarkerRE = regexp.MustCompile(`(?i)(?:\s*[([](?:un)?abridged[)\]])+\s*$`)
	// unabridgedMarkerRE / abridgedMarkerRE detect which edition a title's marker
	// states. unabridged is checked first because "(Unabridged)" contains the
	// substring "abridged" but never immediately after a bracket.
	unabridgedMarkerRE = regexp.MustCompile(`(?i)[([]unabridged[)\]]`)
	abridgedMarkerRE   = regexp.MustCompile(`(?i)[([]abridged[)\]]`)
)

// abridgedFromMarker derives the abridged tri-state from a title's edition
// marker: an "(Unabridged)"/"[Unabridged]" marker means false, an
// "(Abridged)"/"[Abridged]" marker means true, and no marker means nil. The
// title stating the edition is a factual statement printed on the release (it is
// on the cover), so reading the flag from it respects the facts-only rule - we
// are reading a fact the source published, not guessing. When both markers
// somehow appear the more common "unabridged" wins.
func abridgedFromMarker(title string) *bool {
	if unabridgedMarkerRE.MatchString(title) {
		f := false
		return &f
	}
	if abridgedMarkerRE.MatchString(title) {
		t := true
		return &t
	}
	return nil
}

// cleanWorkTitle strips trailing (Unabridged)/(Abridged)/[Unabridged]/[Abridged]
// edition markers from a work title (all stacked markers in one pass), so
// "Mageling" and "Mageling (Unabridged)" resolve to one work. It never returns an
// empty string: a title that is ONLY a marker (or trims to nothing) is returned
// unchanged.
func cleanWorkTitle(title string) string {
	cleaned := strings.TrimSpace(title)
	stripped := strings.TrimSpace(editionMarkerRE.ReplaceAllString(cleaned, ""))
	if stripped == "" {
		return cleaned
	}
	return stripped
}

// recInfo remembers enough about a recording under a work to detect a
// same-identity re-import (idempotency) versus a genuine slug collision, and to
// merge a re-release ASIN into an existing recording rather than minting a
// sibling work (see addRecording). Its file location is derived on demand from
// the work + recording slugs (recordingPath), never stored.
type recInfo struct {
	narrators  map[string]bool
	asins      map[string]bool
	runtimeMin int
	// abridged is the recording's tri-state abridged flag as far as this run
	// knows it. For a recording created THIS run it carries the entry's tri-state
	// (nil = the source did not state it); for a recording loaded from disk it is
	// left nil (unknown) because model.Recording.Abridged is a plain bool that
	// cannot distinguish stated-false from absent - reading the raw JSON to tell
	// them apart is not worth it, so a disk incumbent never blocks a merge on
	// abridged grounds. See abridgedConflict.
	abridged *bool
}

// recordingPath returns a recording file's data-relative, slash-separated
// location (works/<shard>/<work>/recordings/<rec>.json) from its work and
// recording slugs.
func recordingPath(workSlug, recSlug string) string {
	return filepath.ToSlash(filepath.Join("works", model.Shard(workSlug), workSlug, "recordings", recSlug+".json"))
}

// workState tracks a work's identity (slug + author set) and its recordings.
type workState struct {
	slug    string
	authors map[string]bool
	recs    map[string]*recInfo
}

// seriesState tracks a series' membership so works dedupe and positions never
// collide. Existing series carry their full raw JSON so extending one preserves
// every field the importer does not manage.
type seriesState struct {
	slug      string
	name      string
	path      string
	isNew     bool
	dirty     bool
	out       *OutSeries        // populated for a newly created series
	raw       map[string]any    // populated lazily for an existing series
	members   map[string]string // work slug -> position
	positions map[string]string // position -> work slug
}

// planner accumulates the writes and warnings for a run.
type planner struct {
	dataDir string
	// people is the set of known person slugs. The slug IS the normalized
	// identity: two names that slug the same are the same person.
	people    map[string]bool
	works     map[string]*workState
	series    map[string]*seriesState
	asins     map[string]bool
	writes    map[string][]byte
	curSource OutSource
	fatal     error
	summary   Summary
}

// Run imports booksPath (an OpenAudible export) into opts.DataDir. On a dry run
// it only computes the plan. On a real run it writes the new/changed files and
// then validates the whole tree, returning an error if the post-write check
// fails. The Summary is always returned so the caller can print the plan.
func Run(booksPath string, opts Options) (Summary, error) {
	raw, err := os.ReadFile(booksPath)
	if err != nil {
		return Summary{}, fmt.Errorf("read %s: %w", booksPath, err)
	}
	books, err := parseOpenAudible(raw)
	if err != nil {
		return Summary{}, err
	}
	return runBooks(books, sourceOpenAud, opts)
}

// sourceBook is the parsed, source-independent view of one export entry. raw
// carries only the shared-key passthrough fields the planner reads directly
// (asin, title, title_short, author, narrated_by, language, region,
// release_date, publisher, image_url, and OpenAudible's chapters array); any
// fact a source derives differently at parse time is promoted to a typed field
// here, never smuggled through raw. Invariant: every seriesRef carries a
// non-empty name (the parsers skip empties and never emit one).
type sourceBook struct {
	raw        rawBook
	series     []seriesRef // the book's series claims (>1 only for Libation)
	runtimeMin int         // whole minutes; 0 = unknown
	abridged   *bool       // tri-state: nil = the source did not state it
}

// str is a convenience passthrough to the underlying raw entry.
func (s sourceBook) str(key string) string { return s.raw.str(key) }

// primarySeriesClaim returns the book's first fully-valid series claim (a name
// with a valid position), for the work-title disambiguation pre-pass.
func (s sourceBook) primarySeriesClaim() (name, pos string, ok bool) {
	for _, r := range s.series {
		if r.seqOK {
			return r.name, r.seq, true
		}
	}
	return "", "", false
}

// RunLibation imports exportPath (a Libation "Export Library" JSON export) into
// opts.DataDir. Each Libation entry is normalized into the same internal
// sourceBook the OpenAudible path produces (factual fields only; see
// libation.go), so the two sources share every mapping/dedup rule. Behaviour is
// otherwise identical to Run.
func RunLibation(exportPath string, opts Options) (Summary, error) {
	raw, err := os.ReadFile(exportPath)
	if err != nil {
		return Summary{}, fmt.Errorf("read %s: %w", exportPath, err)
	}
	books, err := parseLibation(raw)
	if err != nil {
		return Summary{}, err
	}
	return runBooks(books, sourceLibation, opts)
}

// runBooks is the shared import core: it plans every book into records against
// the existing catalog, then (on a real run) writes and re-validates the tree.
// sourceType is the provenance stamped on every created record.
func runBooks(books []sourceBook, sourceType string, opts Options) (Summary, error) {
	p := &planner{
		dataDir: opts.DataDir,
		people:  map[string]bool{},
		works:   map[string]*workState{},
		series:  map[string]*seriesState{},
		asins:   map[string]bool{},
		writes:  map[string][]byte{},
	}
	p.loadExisting()

	// At the batch boundary, for every book: (1) derive the abridged tri-state
	// from the title's edition marker when the source did not state it, then
	// (2) clean the trailing (Unabridged)/(Abridged) markers off the raw
	// title/title_short. This is the SINGLE marker-derivation mechanism for ALL
	// sources (the ABS path already cleans its titles locally to fix its subtitle
	// split, but never derives abridged), so it must run BEFORE the titles are
	// mutated. Cleaning once here means downstream work-title resolution and
	// full-title re-derivation read undecorated titles without re-cleaning.
	for i := range books {
		if books[i].abridged == nil {
			for _, key := range []string{"title_short", "title"} {
				if a := abridgedFromMarker(books[i].str(key)); a != nil {
					books[i].abridged = a
					break
				}
			}
		}
		for _, key := range []string{"title", "title_short"} {
			raw := books[i].str(key)
			if raw == "" {
				continue
			}
			if cleaned := cleanWorkTitle(raw); cleaned != "" && cleaned != raw {
				books[i].raw[key] = cleaned
			}
		}
	}

	titles := resolveWorkTitles(books)
	for i, b := range books {
		p.curSource = OutSource{Type: sourceType, Ref: NormalizeASIN(b.str("asin")), ImportedAt: opts.ImportDate}
		p.addBook(b, titles[i])
		if p.fatal != nil {
			return p.summary, p.fatal
		}
	}
	p.finalizeSeries()
	if p.fatal != nil {
		return p.summary, p.fatal
	}

	if opts.DryRun {
		return p.summary, nil
	}

	if err := p.flush(); err != nil {
		return p.summary, err
	}
	if res := check.Load(opts.DataDir); !res.OK() {
		return p.summary, fmt.Errorf("post-import validation failed:\n%s", problemLines(res.Problems))
	}
	return p.summary, nil
}

// loadExisting seeds the planner's identity maps from the current data tree so
// new records dedupe against what is already committed.
func (p *planner) loadExisting() {
	cat := check.Load(p.dataDir).Catalog
	if cat == nil {
		return
	}
	for _, person := range cat.People {
		p.people[person.ID] = true
	}
	for _, w := range cat.Works {
		ws := &workState{slug: w.ID, authors: ToSet(w.Authors), recs: map[string]*recInfo{}}
		for _, r := range w.Recordings {
			ri := &recInfo{
				narrators:  ToSet(r.Narrators),
				asins:      map[string]bool{},
				runtimeMin: r.RuntimeMin,
				// abridged stays nil (unknown) for a disk incumbent: the model's
				// plain bool can't distinguish stated-false from absent, so we do
				// not let it block a merge. See recInfo.abridged.
				abridged: nil,
			}
			for _, a := range r.ASIN {
				ri.asins[a.ASIN] = true
				p.asins[a.ASIN] = true
			}
			ws.recs[r.ID] = ri
		}
		p.works[w.ID] = ws
	}
	for _, s := range cat.Series {
		ss := &seriesState{
			slug:      s.ID,
			name:      s.Name,
			path:      filepath.Join("series", model.Shard(s.ID), s.ID+".json"),
			members:   map[string]string{},
			positions: map[string]string{},
		}
		for _, sw := range s.Works {
			ss.members[sw.Work] = sw.Position
			ss.positions[sw.Position] = sw.Work
		}
		p.series[s.ID] = ss
	}
}

// resolveWorkTitles is the deterministic pre-pass over the parsed batch that
// picks each book's work title. The default is title_short (falling back to
// title). But series where every volume shares title_short ("Dragon Heart" for
// volumes whose full titles are "Dragon Heart - Book 10: Land of War", ...)
// would collapse into one work - so books are grouped by title slug ONLY (not
// by author set: Audible's author field varies per volume, listing extra
// translator/introduction credits on some, which would let a volume escape the
// group and squat the bare slug), and when a group carries more than one
// distinct (series, position) claim, EVERY book in the group derives its work
// title from the full title field verbatim, so the incumbent volume does not
// squat the ambiguous slug either. Renaming to full titles is harmless even
// when the group spans genuinely different books - full titles are still
// correct titles - and single-claim groups are never touched.
func resolveWorkTitles(books []sourceBook) []string {
	titles := make([]string, len(books))
	groups := map[string][]int{}
	for i, b := range books {
		titles[i] = firstNonEmpty(b.str("title_short"), b.str("title"))
		key := Slugify(titles[i])
		groups[key] = append(groups[key], i)
	}
	for _, idxs := range groups {
		claims := map[string]bool{}
		for _, i := range idxs {
			name, pos, ok := books[i].primarySeriesClaim()
			if !ok {
				continue
			}
			claims[strings.ToLower(name)+"\x00"+pos] = true
		}
		if len(claims) < 2 {
			continue
		}
		for _, i := range idxs {
			if full := books[i].str("title"); full != "" {
				titles[i] = full
			}
		}
	}
	return titles
}

// addBook maps one export entry to records. workTitle is the pre-pass-resolved
// title for the book's work. It returns quietly (recording a warning or a skip)
// whenever the entry cannot be imported cleanly.
func (p *planner) addBook(b sourceBook, workTitle string) {
	label := bookLabel(b)
	warn := func(format string, args ...any) {
		p.summary.Warnings = append(p.summary.Warnings, label+": "+fmt.Sprintf(format, args...))
	}

	asin := NormalizeASIN(b.str("asin"))

	// Dedup first: an already-present ASIN is a skip, not a warning.
	if asin != "" && p.asins[asin] {
		p.summary.Skipped++
		return
	}

	lang, ok := mapLanguage(b.str("language"))
	if !ok {
		warn("unknown language %q; skipped", b.str("language"))
		return
	}
	narratorNames := SplitNames(b.str("narrated_by"))
	if len(narratorNames) == 0 {
		warn("no narrator; a recording requires narrators; skipped")
		return
	}
	authorNames := SplitNames(b.str("author"))
	if len(authorNames) == 0 {
		warn("no author; a work requires an author; skipped")
		return
	}

	if workTitle == "" {
		warn("no title; skipped")
		return
	}

	authorSlugs := make([]string, 0, len(authorNames))
	for _, name := range authorNames {
		authorSlugs = append(authorSlugs, p.getOrCreatePerson(name, warn))
	}
	narratorSlugs := make([]string, 0, len(narratorNames))
	for _, name := range narratorNames {
		narratorSlugs = append(narratorSlugs, p.getOrCreatePerson(name, warn))
	}

	// The book's series claims (one for OpenAudible, possibly several for
	// Libation). The first that resolves to an already-known series (on disk or
	// created earlier this run) is used to refuse merging into a same-titled work
	// that sits in that series at a different position.
	var claim *seriesClaim
	for _, r := range b.series {
		if !r.seqOK {
			continue
		}
		if ss := p.findSeries(r.name); ss != nil {
			claim = &seriesClaim{ss: ss, pos: r.seq}
			break
		}
	}

	ws := p.getOrCreateWork(workTitle, b.str("title"), authorSlugs, lang, claim, warn)
	p.addRecording(ws, b, asin, lang, narratorSlugs, warn)

	// Single owner of the global ASIN registry: whether addRecording created a
	// new recording or merged the ASIN into an existing one, this tail records it.
	if asin != "" {
		p.asins[asin] = true
	}

	for _, r := range b.series {
		if !r.seqOK {
			warn("series %q: missing or invalid position %q; not placed in series", r.name, r.rawSeq)
		} else {
			p.addToSeries(r.name, ws.slug, r.seq, warn)
		}
	}
}

// seriesRef is a book's claim to a position in a named series. name is always
// non-empty (the sourceBook invariant). seqOK reports whether seq passed
// position validation; rawSeq is the original text (for the "invalid position"
// warning). A book may carry several (Libation multi-series).
type seriesRef struct {
	name   string
	seq    string
	seqOK  bool
	rawSeq string
}

// getOrCreatePerson returns the slug for name, creating the person record when
// it is new. The slug is the normalized identity: "B.V. Larson", "B. V. Larson"
// and "Ramón De Ocampo"/"Ramon de Ocampo" all slug the same, so they are the
// same person - the first record (existing catalog first, then batch order)
// wins and keeps its name; spelling variants never fork a numbered duplicate.
func (p *planner) getOrCreatePerson(name string, warn func(string, ...any)) string {
	slug := Slugify(name)
	if slug == "" {
		slug = "person"
		warn("name %q produced an empty slug; using %q", name, slug)
	}
	if p.people[slug] {
		return slug
	}
	p.people[slug] = true
	p.emit(filepath.Join("people", model.Shard(slug), slug+".json"), OutPerson{
		ID: slug, Name: name, License: licenseCC0, Sources: []OutSource{p.curSource},
	})
	p.summary.NewPeople++
	return slug
}

// seriesClaim is a book's claim to a position in an already-known series.
type seriesClaim struct {
	ss  *seriesState
	pos string
}

// compatible reports whether merging the book into work ws is consistent with
// its series claim. No claim, a work not yet in the series, or the same
// position all merge; the same series at a DIFFERENT position means ws is a
// different volume that merely shares the title.
func (c *seriesClaim) compatible(ws *workState) bool {
	if c == nil {
		return true
	}
	existing, in := c.ss.members[ws.slug]
	return !in || existing == c.pos
}

// getOrCreateWork returns the work identified by (title-slug, author set),
// creating it when new. A same-author work that the book's series claim rules
// out (same series, different position) is not a merge target: the slug is
// re-derived from the full title, with the candidate chain (author suffix,
// then numeric) only as the last-resort collision fallback. A collision with a
// different author set appends the first author's slug, then numeric suffixes,
// and warns.
func (p *planner) getOrCreateWork(title, fullTitle string, authorSlugs []string, lang string, claim *seriesClaim, warn func(string, ...any)) *workState {
	base := Slugify(title)
	if base == "" {
		base = "untitled"
		warn("title %q produced an empty slug; using %q", title, base)
	}
	want := ToSet(authorSlugs)
	for _, slug := range workCandidates(base, authorSlugs[0]) {
		ws, exists := p.works[slug]
		if !exists {
			if slug != base {
				warn("work slug %q taken by a different book; using %q for %q", base, slug, title)
			}
			ws = &workState{slug: slug, authors: want, recs: map[string]*recInfo{}}
			p.works[slug] = ws
			p.emit(filepath.Join("works", model.Shard(slug), slug, "work.json"), outWork{
				ID: slug, Title: title, Authors: authorSlugs, Language: lang,
				License: licenseCC0, Sources: []OutSource{p.curSource},
			})
			p.summary.NewWorks++
			return ws
		}
		if SameSet(ws.authors, want) {
			if claim.compatible(ws) {
				return ws
			}
			// Same authors, but this slug's work sits in the book's series at a
			// different position: a different volume sharing the short title.
			// Re-derive from the full title (once); the candidate chain below is
			// the last resort when that is unusable. Titles are already cleaned of
			// trailing edition markers at the batch boundary.
			if full := Slugify(fullTitle); fullTitle != title && full != "" && full != base {
				return p.getOrCreateWork(fullTitle, "", authorSlugs, lang, claim, warn)
			}
		}
	}
	// Unreachable: workCandidates yields an unbounded numeric tail.
	return nil
}

// findSeries returns the already-known series (existing on disk or created this
// run) that name resolves to, or nil - it never creates. It walks the same
// candidate chain as getOrCreateSeries so both resolve a name identically.
func (p *planner) findSeries(name string) *seriesState {
	base := Slugify(name)
	if base == "" {
		base = "series"
	}
	for i := 0; ; i++ {
		slug := base
		if i > 0 {
			slug = fmt.Sprintf("%s-%d", base, i+1)
		}
		ss, exists := p.series[slug]
		if !exists {
			return nil
		}
		if strings.EqualFold(ss.name, name) {
			return ss
		}
	}
}

// addRecording builds and emits the recording for a book under work ws. When an
// identical recording (same narrator set) already exists, a re-release ASIN on
// this entry is merged into it (runtime-guarded) rather than dropped or minted
// as a sibling work; a genuinely different production (both runtimes known and
// diverging beyond 10 percent) becomes a distinct recording under the same work.
func (p *planner) addRecording(ws *workState, b sourceBook, asin, lang string, narratorSlugs []string, warn func(string, ...any)) {
	base := narratorSlugs[0]
	if year := YearOf(b.str("release_date")); year != "" {
		base += "-" + year
	}
	if base == "" {
		base = "unknown-narrator"
	}
	narrSet := ToSet(narratorSlugs)

	// Collect EVERY same-narrator recording along the base candidate chain (not
	// just the first), and the first free slug for a genuinely new recording. A
	// re-release ASIN can belong to any same-narrator sibling, so we consider all
	// of them before deciding to merge or to mint a distinct recording.
	matches, freeSlug := sameNarratorRecs(ws, base, narrSet)
	slug := freeSlug
	if len(matches) > 0 {
		if asin == "" {
			return // nothing new to add (same production, no new ASIN)
		}
		for _, m := range matches {
			if m.info.asins[asin] {
				return // idempotent: this ASIN is already recorded
			}
		}
		// A new ASIN on this entry is a re-release of an existing production when
		// a sibling is merge-compatible (same narrators - already true here -
		// compatible runtimes, and no abridged conflict). Merge into the FIRST
		// compatible sibling. If none is compatible it is a genuinely different
		// production (a distinct runtime, or a known-abridged edition), so fall
		// through to a distinct slug under the same work.
		for _, m := range matches {
			if runtimesCompatible(m.info.runtimeMin, b.runtimeMin) && !abridgedConflict(m.info.abridged, b.abridged) {
				region, ok := p.resolveASINRegion(b, warn)
				if !ok {
					return
				}
				p.mergeRecordingASIN(m.info, ws.slug, m.slug, region, asin)
				return
			}
		}
	}

	rec := outRecording{
		ID: slug, Work: ws.slug, Narrators: narratorSlugs, Language: lang,
		License: licenseCC0, Sources: []OutSource{p.curSource},
	}
	rec.Abridged = b.abridged
	if b.runtimeMin > 0 {
		rec.RuntimeMin = b.runtimeMin
	}
	if rd := b.str("release_date"); datePattern.MatchString(rd) {
		rec.ReleaseDate = rd
	}
	if pub := b.str("publisher"); pub != "" {
		rec.Publisher = pub
	}
	if img := b.str("image_url"); strings.HasPrefix(img, "https://") {
		rec.CoverURL = img
	}
	if asin != "" {
		if region, ok := p.resolveASINRegion(b, warn); ok {
			rec.ASIN = []OutASIN{{Region: region, ASIN: asin}}
		}
	}
	if chs := buildChapters(b.raw, warn); chs != nil {
		rec.Chapters = chs
	}

	relPath := recordingPath(ws.slug, slug)
	ri := &recInfo{narrators: narrSet, asins: map[string]bool{}, runtimeMin: b.runtimeMin, abridged: b.abridged}
	for _, a := range rec.ASIN {
		ri.asins[a.ASIN] = true
	}
	ws.recs[slug] = ri
	p.emit(relPath, rec)
	p.summary.NewRecordings++
}

// resolveASINRegion maps the book's marketplace region to a canonical region
// code, warning (and returning ok=false) when it is not a known marketplace so
// the caller drops the ASIN rather than record a bogus region. Shared by
// addRecording's merge and new-recording branches.
func (p *planner) resolveASINRegion(b sourceBook, warn func(string, ...any)) (string, bool) {
	region, ok := mapRegion(b.str("region"))
	if !ok {
		warn("region %q is not a known marketplace; ASIN not recorded", b.str("region"))
	}
	return region, ok
}

// abridgedConflict reports whether two recording abridged tri-states are
// incompatible enough to block a merge. An absent flag is read as "unabridged"
// (the audiobook default, and what an unmarked title implies), so an entry KNOWN
// to be abridged never silently merges into a recording that is unabridged or
// unstated - an abridged edition is a distinct production and earns its own
// recording. Two unknown/unabridged sides merge freely.
func abridgedConflict(a, b *bool) bool {
	return boolOrFalse(a) != boolOrFalse(b)
}

func boolOrFalse(p *bool) bool { return p != nil && *p }

// mergeRecordingASIN appends {region, asin} to an existing recording's asin
// array and re-emits it, preserving every other field byte-for-byte. The record
// (located from its work + recording slugs) is loaded from this run's queued
// write when present (a recording emitted earlier in the same run) or from disk
// otherwise. The caller has already checked that asin is not present on ri.
func (p *planner) mergeRecordingASIN(ri *recInfo, workSlug, recSlug, region, asin string) {
	if p.fatal != nil {
		return
	}
	recPath := recordingPath(workSlug, recSlug)
	var raw map[string]any
	if queued, ok := p.writes[recPath]; ok {
		if err := json.Unmarshal(queued, &raw); err != nil {
			p.fatal = fmt.Errorf("parse queued recording %s: %w", recPath, err)
			return
		}
	} else {
		raw = p.loadRawJSON(recPath)
		if p.fatal != nil {
			return
		}
	}
	arr, _ := raw["asin"].([]any)
	raw["asin"] = append(arr, map[string]any{"region": region, "asin": asin})
	// Stamp provenance for the merged fact: the source ref is the incoming ASIN,
	// so the merge stays auditable and retractable per the sources[] contract.
	srcArr, _ := raw["sources"].([]any)
	raw["sources"] = append(srcArr, sourceMap(p.curSource))
	p.emitRaw(recPath, raw)
	ri.asins[asin] = true
	// p.asins is registered by addBook's tail for every path (merge and new
	// recording alike), so it is intentionally NOT set here - one owner.
	p.summary.MergedASINs++
}

// sourceMap renders an OutSource as a JSON-object map for splicing into an
// existing record's raw sources[] array, honoring the same omitempty rules as
// the OutSource struct (canonical.Format sorts the keys, so order is irrelevant).
func sourceMap(s OutSource) map[string]any {
	m := map[string]any{"type": s.Type}
	if s.Ref != "" {
		m["ref"] = s.Ref
	}
	if s.ImportedAt != "" {
		m["imported_at"] = s.ImportedAt
	}
	return m
}

// loadRawJSON reads a data-relative JSON file into a fresh map, setting p.fatal
// on any error (and returning nil). rel is slash-separated. Shared by the
// recording-merge disk branch and loadSeriesRaw so the read -> unmarshal ->
// fatal shape lives in one place.
func (p *planner) loadRawJSON(rel string) map[string]any {
	if p.fatal != nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(p.dataDir, filepath.FromSlash(rel)))
	if err != nil {
		p.fatal = fmt.Errorf("read %s: %w", rel, err)
		return nil
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		p.fatal = fmt.Errorf("parse %s: %w", rel, err)
		return nil
	}
	return raw
}

// addToSeries places work at position pos in the named series, creating the
// series when new. Duplicate memberships and position clashes warn and leave the
// existing entry.
func (p *planner) addToSeries(name, work, pos string, warn func(string, ...any)) {
	// Defense in depth: the parsers uphold the non-empty-name invariant, but a
	// future source (or a direct caller) must never mint a nameless series.
	if name == "" {
		warn("empty series name; not placed in series")
		return
	}
	ss := p.getOrCreateSeries(name, warn)
	if existing, ok := ss.members[work]; ok {
		if existing != pos {
			warn("series %q already lists work %q at position %q; not re-adding at %q", name, work, existing, pos)
		}
		return
	}
	if other, ok := ss.positions[pos]; ok && other != work {
		warn("series %q position %q already taken by %q; %q not added", name, pos, other, work)
		return
	}
	ss.members[work] = pos
	ss.positions[pos] = work
	ss.dirty = true
	if ss.isNew {
		ss.out.Works = append(ss.out.Works, OutSeriesWork{Work: work, Position: pos})
	} else {
		p.loadSeriesRaw(ss)
		works, _ := ss.raw["works"].([]any)
		ss.raw["works"] = append(works, map[string]any{"work": work, "position": pos})
	}
}

// getOrCreateSeries returns the series for name, creating an in-memory record
// when new. Numeric suffixes resolve a collision with a differently-named series.
func (p *planner) getOrCreateSeries(name string, warn func(string, ...any)) *seriesState {
	base := Slugify(name)
	if base == "" {
		base = "series"
		warn("series name %q produced an empty slug; using %q", name, base)
	}
	for i := 0; ; i++ {
		slug := base
		if i > 0 {
			slug = fmt.Sprintf("%s-%d", base, i+1)
		}
		ss, exists := p.series[slug]
		if !exists {
			if slug != base {
				warn("series slug %q taken by a different series; using %q for %q", base, slug, name)
			}
			ss = &seriesState{
				slug:      slug,
				name:      name,
				path:      filepath.Join("series", model.Shard(slug), slug+".json"),
				isNew:     true,
				out:       &OutSeries{ID: slug, Name: name, License: licenseCC0, Sources: []OutSource{p.curSource}},
				members:   map[string]string{},
				positions: map[string]string{},
			}
			p.series[slug] = ss
			p.summary.NewSeries++
			return ss
		}
		if strings.EqualFold(ss.name, name) {
			return ss
		}
	}
}

// loadSeriesRaw reads an existing series file into ss.raw the first time it is
// extended, so its non-managed fields (authors, xref, existing sources) survive.
func (p *planner) loadSeriesRaw(ss *seriesState) {
	if ss.raw != nil || p.fatal != nil {
		return
	}
	ss.raw = p.loadRawJSON(ss.path)
}

// finalizeSeries queues the JSON for every new or extended series.
func (p *planner) finalizeSeries() {
	for _, ss := range p.series {
		if !ss.dirty {
			continue
		}
		if ss.isNew {
			p.emit(ss.path, ss.out)
		} else {
			p.emitRaw(ss.path, ss.raw)
		}
	}
}

// emit canonicalizes v and queues it for writing at rel (a data-relative path).
func (p *planner) emit(rel string, v any) {
	if p.fatal != nil {
		return
	}
	data, err := json.Marshal(v)
	if err != nil {
		p.fatal = fmt.Errorf("marshal %s: %w", rel, err)
		return
	}
	p.emitRaw(rel, json.RawMessage(data))
}

func (p *planner) emitRaw(rel string, v any) {
	if p.fatal != nil {
		return
	}
	data, err := json.Marshal(v)
	if err != nil {
		p.fatal = fmt.Errorf("marshal %s: %w", rel, err)
		return
	}
	formatted, err := canonical.Format(data)
	if err != nil {
		p.fatal = fmt.Errorf("canonicalize %s: %w", rel, err)
		return
	}
	p.writes[filepath.ToSlash(rel)] = formatted
}

// flush writes every queued file to disk under the data dir, creating parent
// directories. Paths are written in sorted order for a deterministic run.
func (p *planner) flush() error {
	rels := make([]string, 0, len(p.writes))
	for rel := range p.writes {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		full := filepath.Join(p.dataDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", rel, err)
		}
		if err := os.WriteFile(full, p.writes[rel], 0o644); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
	}
	return nil
}

// buildChapters maps a book's chapters, trimming titles and enforcing the same
// monotonic-from-zero rule metacheck applies. On any structural violation it
// warns and returns nil (the recording is emitted without chapters).
func buildChapters(b rawBook, warn func(string, ...any)) []outChapter {
	raw := b.chapters()
	if len(raw) == 0 {
		return nil
	}
	out := make([]outChapter, 0, len(raw))
	for i, rc := range raw {
		start, sOK := rc.intVal("start_offset_ms")
		length, lOK := rc.intVal("length_ms")
		if !sOK || !lOK || length <= 0 || start < 0 {
			warn("chapter %d has invalid offsets; chapters omitted", i+1)
			return nil
		}
		title := strings.TrimSpace(rc.str("title"))
		if title == "" {
			title = fmt.Sprintf("Chapter %d", i+1)
		}
		out = append(out, outChapter{Title: title, StartMS: start, LengthMS: length})
	}
	if out[0].StartMS != 0 {
		warn("chapters do not start at 0; chapters omitted")
		return nil
	}
	for i := 1; i < len(out); i++ {
		if out[i].StartMS <= out[i-1].StartMS {
			warn("chapter offsets are not strictly increasing; chapters omitted")
			return nil
		}
	}
	return out
}

// recCandidate pairs a recording slug with its recInfo for the same-narrator
// scan.
type recCandidate struct {
	slug string
	info *recInfo
}

// sameNarratorRecs walks the whole base candidate chain under ws, returning
// EVERY recording whose narrator set matches (a re-release ASIN can land on any
// same-narrator sibling, not just the first) together with the first free slug
// for a new recording. The walk terminates at the first free slug, so the chain
// is finite.
func sameNarratorRecs(ws *workState, base string, narrators map[string]bool) (matches []recCandidate, freeSlug string) {
	for i := 0; ; i++ {
		slug := recSlugAt(base, i)
		existing, ok := ws.recs[slug]
		if !ok {
			return matches, slug
		}
		if SameSet(existing.narrators, narrators) {
			matches = append(matches, recCandidate{slug: slug, info: existing})
		}
	}
}

// recSlugAt is the recording slug-candidate formula: base for the first
// candidate, then base-2, base-3, ... for subsequent ones.
func recSlugAt(base string, i int) string {
	if i == 0 {
		return base
	}
	return fmt.Sprintf("%s-%d", base, i+1)
}

// runtimesCompatible reports whether two recording runtimes (whole minutes; 0 or
// negative = unknown) are close enough to be the same production. An unknown on
// either side is compatible; two known runtimes must be within 10 percent of the
// larger.
func runtimesCompatible(a, b int) bool {
	if a <= 0 || b <= 0 {
		return true
	}
	hi, lo := a, b
	if lo > hi {
		hi, lo = lo, hi
	}
	return float64(hi-lo) <= 0.10*float64(hi)
}

// workCandidates yields the ordered slug candidates for a work: the bare title
// slug, then the title plus first-author slug, then numeric suffixes on that.
func workCandidates(base, firstAuthor string) []string {
	withAuthor := base + "-" + firstAuthor
	out := []string{base, withAuthor}
	for i := 2; i <= 50; i++ {
		out = append(out, fmt.Sprintf("%s-%d", withAuthor, i))
	}
	return out
}

func NormalizeASIN(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	if asinPattern.MatchString(s) {
		return s
	}
	return ""
}

func bookLabel(b sourceBook) string {
	if a := strings.TrimSpace(b.str("asin")); a != "" {
		return a
	}
	if t := firstNonEmpty(b.str("title_short"), b.str("title")); t != "" {
		return t
	}
	return "(unknown book)"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// ToSet builds a set from a string slice. Shared with internal/issueform so a
// form submission dedupes narrator/author sets exactly like a bulk import.
func ToSet(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, it := range items {
		m[it] = true
	}
	return m
}

// SameSet reports whether two string sets have identical membership.
func SameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func problemLines(ps []check.Problem) string {
	var b strings.Builder
	for _, p := range ps {
		b.WriteString("  " + p.String() + "\n")
	}
	return b.String()
}
