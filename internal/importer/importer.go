// Package importer converts external audiobook-library exports into
// audiosilo-meta records on disk. It maps one OpenAudible books.json entry to a
// work + recording (+ people, + series), deduplicating against the existing
// catalog so a contributor's upload lands as a reviewable diff. Only factual
// fields are imported (see LICENSING.md); publisher copy and covers-as-files are
// never touched.
package importer

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
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
// sibling work (see addRecording). Its storage location is the work's composite
// pack entry, reached on demand from the work + recording slugs, never stored.
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
	people map[string]bool
	works  map[string]*workState
	series map[string]*seriesState
	asins  map[string]bool
	// isbns is the set of ISBNs already recorded on some recording (seeded from
	// disk in loadExisting, then extended as recordings are emitted), so an
	// emitted tree can never violate checkUniqueness's global ISBN rule. Keys are
	// uppercased to match the rule's normISBN comparison.
	isbns map[string]bool
	// store is the run's write layer: every record lands as a pack entry through
	// it, and its queued-write-first reads are what let several rows compose one
	// record within a run (see write.go).
	store *pack.Store
	// asinLoc locates the recording each already-catalogued ASIN sits on. It is
	// allocated only for the runs that need to REACH a record an ASIN already
	// sits on: an enrichment pass, and any user-tier run (whose ASIN-matched
	// rows attest the record they matched - see attest.go). A libex create or
	// recordings-only run needs the p.asins membership test alone and pays
	// nothing, so a nil map is still "this run never looks a record up".
	asinLoc map[string]RecRef
	// userTier reports whether THIS run's source type carries user-library trust
	// (model.TierUserLibrary): a person's own library export, as opposed to the
	// libex bulk mirror. It is the run-wide half of the overwrite decision; the
	// per-record half is whether the record the row matched is still
	// bulk-mirror-only. See attest.go and LICENSING.md's trust tiers.
	userTier bool
	// genres is the source-genre-string -> vocabulary mapping table (one
	// embedded table, looked up once per run rather than once per book).
	genres genreTable
	// unmappedGenres collects every distinct source genre string that has no
	// vocabulary mapping, reported once per run rather than once per book.
	unmappedGenres map[string]bool
	// noWorkExamples labels a FEW of the rows a RECORDINGS-ONLY run could not
	// place because their work is not in the catalogue, for the aggregate warning
	// at the end of the pass (see reportUnmatchedWorks). It is capped at
	// maxWarnExamples as it fills: the mode's natural input is the unfiltered
	// dump, where nearly every row lands here, and only a handful are ever
	// printed - the COUNT comes from Summary.SkippedNoWork. Empty in every other
	// mode.
	noWorkExamples []string
	// sourceType / importDate are the run-wide halves of every provenance stamp
	// (the per-row half is the book's ASIN); setSource composes the three.
	sourceType string
	importDate string
	curSource  OutSource
	fatal      error
	summary    Summary
}

// setSource points the planner's provenance stamp at the row being planned. Every
// record a row creates or changes carries it (see stampSource).
func (p *planner) setSource(asin string) {
	p.curSource = OutSource{Type: p.sourceType, Ref: asin, ImportedAt: p.importDate}
}

// stampSource appends this row's provenance to an existing record's raw sources[]
// array. It goes through appendSourceUnique, so a second pass over the same row
// never double-stamps.
func (p *planner) stampSource(raw map[string]any) {
	srcArr, _ := raw["sources"].([]any)
	raw["sources"] = appendSourceUnique(srcArr, p.curSource)
}

// bookWarn returns the warning sink for one book: every line it records is
// prefixed with the book's label (its ASIN, else its title), so a warning always
// names the row it came from.
func (p *planner) bookWarn(b sourceBook) func(string, ...any) {
	label := bookLabel(b)
	return func(format string, args ...any) {
		p.summary.Warnings = append(p.summary.Warnings, label+": "+fmt.Sprintf(format, args...))
	}
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
// release_date, publisher, image_url, and a chapters array); any fact a source
// derives differently at parse time is promoted to a typed field here, never
// smuggled through raw in another source's key shape. Invariant: every
// seriesRef carries a non-empty name (the parsers skip empties and never emit
// one).
type sourceBook struct {
	raw        rawBook
	series     []seriesRef  // the book's series claims (>1 only for Libation)
	runtimeMin int          // whole minutes; 0 = unknown
	abridged   *bool        // tri-state: nil = the source did not state it
	genres     []genreClaim // raw genre claims, mapped onto our vocabulary on work creation
	isbns      []string     // well-formed ISBNs the source stated (validated at parse)
	// authors / narrators are the source's STRUCTURED credit lists, set when it
	// provides one name per element. They are used verbatim (each still passed
	// through CleanCreditName) instead of splitting raw's comma-joined
	// string, so a name that contains a comma ("Alexandre Dumas, pere") stays
	// one person. Empty means the source only has the joined string.
	authors   []string
	narrators []string
	// chapters is the source's own chapter rows, read when non-nil instead of
	// raw's chapters array. buildChapters accepts either documented offset
	// spelling (see rawChapter.startMS), so a parser hands its rows over
	// as-is.
	chapters []rawChapter
}

// str is a convenience passthrough to the underlying raw entry.
func (s sourceBook) str(key string) string { return s.raw.str(key) }

// chapterRows returns the book's chapter rows: the typed field when the parser
// set one, else the raw entry's own chapters array.
func (s sourceBook) chapterRows() []rawChapter {
	if s.chapters != nil {
		return s.chapters
	}
	return s.raw.chapters()
}

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
// sourceType is the provenance stamped on every created (or enriched) record.
//
// The three planning modes are disjoint by design and selected by opts.Mode
// (see the Mode constants), so there is no combination to police here.
// Loading, emitting, flushing and post-run validation are shared.
func runBooks(books []sourceBook, sourceType string, opts Options) (Summary, error) {
	// Opened before anything is planned: a tree still in the file-per-entity
	// layout is refused here, having written nothing and read nothing it could
	// misinterpret.
	store, err := openStore(opts.DataDir)
	if err != nil {
		return Summary{}, err
	}
	p := &planner{
		dataDir:        opts.DataDir,
		people:         map[string]bool{},
		works:          map[string]*workState{},
		series:         map[string]*seriesState{},
		asins:          map[string]bool{},
		isbns:          map[string]bool{},
		store:          store,
		genres:         audibleGenreTable().withRunMemo(),
		unmappedGenres: map[string]bool{},
		sourceType:     sourceType,
		importDate:     opts.ImportDate,
		userTier:       model.TierOfSource(sourceType) == model.TierUserLibrary,
	}
	if opts.Mode == ModeEnrich || p.userTier {
		p.asinLoc = map[string]RecRef{}
	}
	p.loadExisting()

	switch opts.Mode {
	case ModeEnrich:
		p.planEnrich(books)
	case ModeRecordingsOnly:
		p.planRecordings(books)
	case ModeCreate:
		p.planCreate(books)
	}
	if p.fatal != nil {
		return p.summary, p.fatal
	}
	p.finalizeSeries()
	p.reportUnmappedGenres()
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

// planCreate is the default (create) planning pass: every book that the
// catalogue does not already hold by ASIN becomes work/recording/person/series
// records, each stamped with the planner's run provenance.
func (p *planner) planCreate(books []sourceBook) {
	normalizeEditionMarkers(books)
	titles := resolveWorkTitles(books)
	for i, b := range books {
		asin := NormalizeASIN(b.str("asin"))
		p.setSource(asin)
		p.addBook(b, asin, titles[i])
		if p.fatal != nil {
			return
		}
	}
}

// normalizeEditionMarkers is the batch-boundary title pre-pass every planning
// mode that reads titles runs first. For every book it: (1) derives the abridged
// tri-state from the title's edition marker when the source did not state it,
// then (2) cleans the trailing (Unabridged)/(Abridged) markers off the raw
// title/title_short. This is the SINGLE marker-derivation mechanism for ALL
// sources (the ABS path already cleans its titles locally to fix its subtitle
// split, but never derives abridged), so step 1 must run BEFORE the titles are
// mutated. Cleaning once here means downstream work-title resolution and
// full-title re-derivation read undecorated titles without re-cleaning.
func normalizeEditionMarkers(books []sourceBook) {
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
				p.locateASIN(a.ASIN, w.ID, r.ID)
			}
			for _, isbn := range r.ISBN {
				p.isbns[strings.ToUpper(isbn)] = true
			}
			ws.recs[r.ID] = ri
		}
		p.works[w.ID] = ws
	}
	for _, s := range cat.Series {
		ss := &seriesState{
			slug:      s.ID,
			name:      s.Name,
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

// locateASIN records which catalogued recording an ASIN sits on, for the
// enrichment mode's identifier match. It is a no-op unless asinLoc was allocated
// (create mode needs the p.asins membership test alone).
//
// ASSUMPTION, deliberately made visible: uniqueness upstream is (region, ASIN),
// so two recordings could legally carry the same ASIN STRING in different
// marketplaces - while an export row states one bare ASIN and nothing that could
// pick between them. No such pair exists in the catalogue today. If one appears,
// the FIRST recording (in the catalogue's stable load order) keeps the match and
// the collision is reported, so the day it happens it is visible rather than
// silently decided by whichever file loaded last.
func (p *planner) locateASIN(asin, workSlug, recSlug string) {
	if p.asinLoc == nil {
		return
	}
	if prev, taken := p.asinLoc[asin]; taken {
		p.summary.Warnings = append(p.summary.Warnings, fmt.Sprintf(
			"catalogue: ASIN %s is recorded on both %s and %s; enrichment matched it to the first",
			asin, recLabel(prev.Work, prev.Rec), recLabel(workSlug, recSlug)))
		return
	}
	p.asinLoc[asin] = RecRef{Work: workSlug, Rec: recSlug}
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

// addBook maps one export entry to records. asin is the row's normalized ASIN
// (computed once by the caller) and workTitle is the pre-pass-resolved title for
// the book's work. It returns quietly (recording a warning or a skip) whenever
// the entry cannot be imported cleanly.
func (p *planner) addBook(b sourceBook, asin, workTitle string) {
	warn := p.bookWarn(b)

	// Dedup first: an already-present ASIN is a skip, not a warning. It is also
	// the one place a USER's own library meets a record the libex mirror seeded,
	// so the skip is where the trust-tier attestation happens (attest.go); for a
	// libex run attestExisting is a no-op and the skip is exactly what it was.
	if p.dedupeByASIN(asin) {
		p.attestExisting(b, asin)
		return
	}

	lang, narratorNames, ok := admitRecordingFacts(b, warn)
	if !ok {
		return
	}
	authorNames := rowAuthorNames(b)
	if len(authorNames) == 0 {
		warn("no author; a work requires an author; skipped")
		return
	}

	if workTitle == "" {
		warn("no title; skipped")
		return
	}

	authorSlugs := p.creditSlugs(authorNames, warn)
	narratorSlugs := p.creditSlugs(narratorNames, warn)

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

	// The book's genre claims are mapped from the source's own strings onto this
	// project's vocabulary (LICENSING.md: never a retailer's taxonomy verbatim)
	// inside getOrCreateWork, and ONLY when it creates the work - a work already
	// in the catalogue is not modified by a normal import, so mapping a row whose
	// genres could never be stored would only add noise to the unmapped report.
	ws := p.getOrCreateWork(workTitle, b.str("title"), authorSlugs, lang, claim, b.genres, warn)
	recorded := p.addRecording(ws, b, asin, lang, narratorSlugs, warn)

	// Single owner of the global ASIN registry: whether addRecording created a
	// new recording or merged the ASIN into an existing one, this tail records
	// it - but ONLY when the ASIN actually landed on a recording. An ASIN the
	// region check rejected is nowhere in the tree, so claiming it would make a
	// later, well-formed row for the same book dedupe against nothing and be
	// skipped, losing the ASIN for the whole run.
	//
	// What a merge carries is deliberately narrow: the ASIN, this run's
	// provenance stamp, and any globally-unclaimed ISBN. The incumbent
	// recording's cover, publisher, chapters and release date are left alone -
	// backfilling absent facts onto records already in the catalogue is the
	// separate enrichment mode's job, not a side effect of a new-books import.
	if asin != "" && recorded {
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

// dedupeByASIN is the first gate of every planner that CREATES a recording: a
// row whose ASIN the catalogue already holds is a skip, not a warning. A row
// with no well-formed ASIN can never dedupe, so it always passes.
func (p *planner) dedupeByASIN(asin string) bool {
	if asin != "" && p.asins[asin] {
		p.summary.Skipped++
		return true
	}
	return false
}

// admitRecordingFacts validates the two things a RECORDING cannot be built
// without - a language the schema knows and at least one narrator - and returns
// them. ok=false means the row was warned about and must be dropped.
//
// It is shared by the create and recordings-only planners so the two can never
// drift on what a usable row is, or on how a rejected one is worded. They differ
// only in WHEN they call it: the create path validates before it resolves a
// work, while recordings-only resolves the work FIRST, so a row for a book the
// catalogue does not hold is never warned about at all.
func admitRecordingFacts(b sourceBook, warn func(string, ...any)) (lang string, narratorNames []string, ok bool) {
	lang, ok = mapLanguage(b.str("language"))
	if !ok {
		warn("unknown language %q; skipped", b.str("language"))
		return "", nil, false
	}
	narratorNames = rowNarratorNames(b)
	if len(narratorNames) == 0 {
		warn("no narrator; a recording requires narrators; skipped")
		return "", nil, false
	}
	return lang, narratorNames, true
}

// rowAuthorNames / rowNarratorNames are a row's cleaned credit lists, read from
// the source's structured list when it has one and from its comma-joined string
// otherwise (creditNames owns that choice).
func rowAuthorNames(b sourceBook) []string { return creditNames(b.authors, b.str("author")) }

func rowNarratorNames(b sourceBook) []string { return creditNames(b.narrators, b.str("narrated_by")) }

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

// makeSeriesRef builds a book's claim to a position in a named series,
// validating the raw position token through the shared rules. Every source
// builds its refs here so one spelling of a position ("1.0") can never become a
// different position from another ("1").
func makeSeriesRef(name, rawSeq string) seriesRef {
	pos, ok := NormalizeSequence(rawSeq)
	return seriesRef{name: name, seq: pos, seqOK: ok, rawSeq: rawSeq}
}

// creditNames resolves one credit list (authors or narrators). A source that
// parsed structured credits passes them in typed, and they are used verbatim
// (trimmed, credit-cleaned, empties dropped) - splitting them on commas
// would tear "Alexandre Dumas, pere" into two people. A source that only has the
// retailer's comma-joined string passes it as joined and it is split.
func creditNames(typed []string, joined string) []string {
	if len(typed) == 0 {
		return SplitNames(joined)
	}
	out := make([]string, 0, len(typed))
	for _, name := range typed {
		if n := strings.TrimSpace(name); n != "" {
			out = append(out, CleanCreditName(n))
		}
	}
	return out
}

// creditSlugs resolves a credit list to person slugs, creating people as needed,
// and deduplicates BY SLUG in first-seen order. The slug is the identity, so two
// spellings of one person on the same book ("Ramon de Ocampo" and "Ramon De
// Ocampo") are one credit - listing the slug twice would emit a record whose
// narrators/authors array repeats itself.
func (p *planner) creditSlugs(names []string, warn func(string, ...any)) []string {
	out := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		slug := p.getOrCreatePerson(name, warn)
		if seen[slug] {
			continue
		}
		seen[slug] = true
		out = append(out, slug)
	}
	return out
}

// getOrCreatePerson returns the slug for name, creating the person record when
// it is new. The slug is the normalized identity: "B.V. Larson", "B. V. Larson"
// and "Ramón De Ocampo"/"Ramon de Ocampo" all slug the same, so they are the
// same person - the first record (existing catalog first, then batch order)
// wins and keeps its name; spelling variants never fork a numbered duplicate.
func (p *planner) getOrCreatePerson(name string, warn func(string, ...any)) string {
	slug, fellBack := personSlug(name)
	if fellBack {
		warn("name %q produced an empty slug; using %q", name, slug)
	}
	if p.people[slug] {
		return slug
	}
	p.people[slug] = true
	p.putNewEntry(pack.FamilyPeople, slug, OutPerson{
		ID: slug, Name: name, License: licenseCC0, Sources: []OutSource{p.curSource},
	})
	p.summary.NewPeople++
	return slug
}

// personSlug derives a credit name's person identity, substituting the shared
// "person" fallback when the name slugs away to nothing (a name in a script that
// folds entirely). fellBack reports that substitution so a caller that CREATES
// the record can warn about it, while a caller that only MATCHES (slugCredits)
// stays silent. Both go through here so a name resolves to one identity
// everywhere.
func personSlug(name string) (slug string, fellBack bool) {
	if slug = Slugify(name); slug == "" {
		return "person", true
	}
	return slug, false
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
// and warns. genreClaims are the book's raw genre claims; they are mapped onto
// the project vocabulary only on the branch that creates a work (the only place
// they can be stored), and ride through the full-title retry unchanged.
func (p *planner) getOrCreateWork(title, fullTitle string, authorSlugs []string, lang string, claim *seriesClaim, genreClaims []genreClaim, warn func(string, ...any)) *workState {
	base := Slugify(title)
	if base == "" {
		base = "untitled"
		warn("title %q produced an empty slug; using %q", title, base)
	}
	want := ToSet(authorSlugs)
	for i, slug := range workCandidates(base, authorSlugs[0]) {
		ws, exists := p.works[slug]
		if !exists {
			if slug != base {
				warn("work slug %q taken by a different book; using %q for %q", base, slug, title)
			}
			ws = &workState{slug: slug, authors: want, recs: map[string]*recInfo{}}
			p.works[slug] = ws
			// added_at is stamped here and only here for a work: this is the
			// branch that CREATES one. A merge onto an existing work, and every
			// enrichment backfill, leave the field as they found it.
			//
			// putNewEntry, not putEntry: p.works comes from a best-effort
			// catalogue load, so a work the loader could not decode looks free
			// here, and a plain upsert would replace its whole composite entry -
			// every recording included.
			p.putNewEntry(pack.FamilyWorks, slug, outWork{
				ID: slug, Title: title, Authors: authorSlugs, Language: lang,
				Genres:  p.genres.mapGenres(genreClaims, p.unmappedGenres),
				AddedAt: p.importDate,
				License: licenseCC0, Sources: []OutSource{p.curSource},
			})
			p.summary.NewWorks++
			return ws
		}
		if SameSet(ws.authors, want) {
			if claim.compatible(ws) {
				// A merge onto a SHORTENED candidate is the one case where the slug
				// no longer carries the whole title: two different long titles by
				// one author agreeing up to the cut land here as a single work. The
				// identity model accepts that collision risk, but it must not be
				// silent - on the unbounded formula it surfaced as an invalid slug.
				if workSlugTruncated(base, authorSlugs[0], i) {
					warn("work slug for %q was shortened to fit; merging into existing work %q - verify these are the same book", title, slug)
				}
				return ws
			}
			// Same authors, but this slug's work sits in the book's series at a
			// different position: a different volume sharing the short title.
			// Re-derive from the full title (once); the candidate chain below is
			// the last resort when that is unusable. Titles are already cleaned of
			// trailing edition markers at the batch boundary.
			if full := Slugify(fullTitle); fullTitle != title && full != "" && full != base {
				return p.getOrCreateWork(fullTitle, "", authorSlugs, lang, claim, genreClaims, warn)
			}
		}
	}
	// Unreachable in practice: 50 numeric candidates never all collide.
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
		ss, exists := p.series[NumberedSlugAt(base, i)]
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
//
// asinRecorded reports whether asin ended up on a recording (newly attached,
// merged, or already there). It is false when the region check rejected it, so
// the caller does not claim an ASIN that is nowhere in the tree.
func (p *planner) addRecording(ws *workState, b sourceBook, asin, lang string, narratorSlugs []string, warn func(string, ...any)) (asinRecorded bool) {
	// Defensive only: personSlug substitutes "person" for an unslugifiable name
	// and admitRecordingFacts guarantees at least one narrator, so the slug is
	// never empty. Checked before the year so the guard cannot produce "-2020".
	base := narratorSlugs[0]
	if base == "" {
		base = "unknown-narrator"
	}
	// The year is bounded onto the narrator slug, which Slugify already capped at
	// MaxSlugLen: a long full-cast or corporate credit would otherwise overrun
	// the cap before the collision chain adds a single suffix.
	if year := YearOf(b.str("release_date")); year != "" {
		base = BoundedSlugTail(base, "-"+year)
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
			return false // nothing new to add (same production, no new ASIN)
		}
		for _, m := range matches {
			if m.info.asins[asin] {
				return true // idempotent: this ASIN is already recorded
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
					return false
				}
				// A user import merging into a BULK-MIRROR-ONLY recording attests
				// it, and must do so BEFORE the merge stamps this run's source on
				// the record: that stamp is what ends the record's
				// bulk-mirror-only status, so reading the tier afterwards would
				// see a user-attested record and apply nothing - the row's facts
				// would be lost in the very act that claims a user attested them.
				// On any other record this is a no-op, so the merge stays as
				// narrow as it has always been (see addBook's note above).
				p.attestOnMerge(b, RecRef{Work: ws.slug, Rec: m.slug}, warn)
				// The entry's ISBNs ride along with the ASIN: they are the same
				// edition's identifiers, and dropping them silently (as an
				// earlier version did) loses a fact no later run would restore.
				p.mergeRecordingASIN(m.info, ws.slug, m.slug, region, asin, p.claimISBNs(b.isbns, warn))
				return true
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
			asinRecorded = true
		}
	}
	rec.ISBN = p.claimISBNs(b.isbns, warn)
	if chs := buildChapters(b.chapterRows(), warn); chs != nil {
		rec.Chapters = chs
	}
	// Stamped only on this branch, which is the one that CREATES a recording;
	// the ASIN merge above returns before reaching it.
	rec.AddedAt = p.importDate

	ri := &recInfo{narrators: narrSet, asins: map[string]bool{}, runtimeMin: b.runtimeMin, abridged: b.abridged}
	for _, a := range rec.ASIN {
		ri.asins[a.ASIN] = true
	}
	ws.recs[slug] = ri
	p.putRecording(ws.slug, slug, rec)
	p.summary.NewRecordings++
	return asinRecorded
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

// claimISBNs registers the book's ISBNs against the run's global ISBN set and
// returns the ones this recording may carry. An ISBN already recorded elsewhere
// (on disk or emitted earlier this run) is dropped with a warning rather than
// emitted: checkUniqueness requires ISBNs to be globally unique, so a duplicate
// would fail the post-import validation of the whole tree. Values are already
// well-formed (the parsers validate against the schema pattern); the set key is
// uppercased to match the rule's case-insensitive comparison.
func (p *planner) claimISBNs(isbns []string, warn func(string, ...any)) []string {
	var out []string
	for _, isbn := range isbns {
		key := strings.ToUpper(isbn)
		if p.isbns[key] {
			warn("ISBN %s is already recorded on another recording; not added", isbn)
			continue
		}
		p.isbns[key] = true
		out = append(out, isbn)
	}
	return out
}

// reportUnmappedGenres appends one run-level warning naming every distinct
// source genre string that had no vocabulary mapping (sorted, so the line is
// deterministic). Unmapped strings are DROPPED by design - LICENSING.md forbids
// storing a retailer's genre strings verbatim - and this warning is how a
// maintainer learns the vocabulary or the mapping table needs extending.
func (p *planner) reportUnmappedGenres() {
	if len(p.unmappedGenres) == 0 {
		return
	}
	names := make([]string, 0, len(p.unmappedGenres))
	for name := range p.unmappedGenres {
		names = append(names, name)
	}
	sort.Strings(names)
	p.summary.Warnings = append(p.summary.Warnings,
		fmt.Sprintf("unmapped genre strings: %s", strings.Join(names, ", ")))
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

// mergeRecordingASIN appends {region, asin} (and any ISBN the caller claimed for
// this entry) to an existing recording and re-queues it, preserving every other
// field byte-for-byte. The recording is read from inside its work's composite
// entry, queued-write-first (so a recording written earlier in the same run is
// the one edited), and the whole entry goes back. It never stamps added_at: the
// recording being merged into entered the database earlier. The caller has
// already checked that asin is not present on ri, and that every isbn is
// globally unclaimed.
func (p *planner) mergeRecordingASIN(ri *recInfo, workSlug, recSlug, region, asin string, isbns []string) {
	if p.fatal != nil {
		return
	}
	entry, raw := p.recordingRaw(workSlug, recSlug)
	if raw == nil {
		return
	}
	arr, _ := raw["asin"].([]any)
	raw["asin"] = append(arr, map[string]any{"region": region, "asin": asin})
	appendISBNs(raw, isbns)
	// Stamp provenance for the merged fact: the source ref is the incoming ASIN,
	// so the merge stays auditable and retractable per the sources[] contract.
	p.stampSource(raw)
	p.putWorkEntry(workSlug, entry)
	ri.asins[asin] = true
	// p.asins is registered by addBook's tail for every path (merge and new
	// recording alike), so it is intentionally NOT set here - one owner.
	p.summary.MergedASINs++
}

// appendSourceUnique appends src to an existing record's raw sources[] array
// unless an entry with the same type+ref is already present. Re-importing an
// ASIN (or a second pass over the same library, like a cover backfill) must not
// double-stamp provenance: sources[] is meant to be a set of distinct,
// auditable/retractable refs, so an identical stamp carries no new information
// and only muddies which record to trust or retract.
func appendSourceUnique(srcArr []any, src OutSource) []any {
	for _, s := range srcArr {
		m, ok := s.(map[string]any)
		if !ok {
			continue
		}
		t, _ := m["type"].(string)
		r, _ := m["ref"].(string)
		if t == src.Type && r == src.Ref {
			return srcArr
		}
	}
	return append(srcArr, sourceMap(src))
}

// appendISBNs appends isbns to an existing record's raw isbn[] array. The caller
// has already checked that every value is globally unclaimed (claimISBNs) and not
// already on this record, so this is a plain append.
func appendISBNs(raw map[string]any, isbns []string) {
	if len(isbns) == 0 {
		return
	}
	existing, _ := raw["isbn"].([]any)
	for _, isbn := range isbns {
		existing = append(existing, isbn)
	}
	raw["isbn"] = existing
}

// fillStr records val at key on an existing record when the row states one,
// reporting whether it changed anything. It is the one place the "existing value
// wins" rule lives for a plain string field - and, with overwrite, the one place
// the trust-tier exception to it lives:
//
//   - overwrite false (the default posture): the value is written only when the
//     record carries none. A recorded value always wins.
//   - overwrite true: the row's stated value REPLACES the recorded one. Only a
//     user-library run against a bulk-mirror-only record gets this (see
//     attest.go and LICENSING.md's trust tiers), and after that first
//     attestation the record is no longer bulk-mirror-only, so the next run is
//     back to the default posture. That is what makes the takeover a one-way
//     step rather than last-writer-wins churn.
//
// A row that states nothing (val == "") never clears a recorded value in either
// posture: overwriting is about a fact the row ASSERTS, and silence is not an
// assertion.
func fillStr(raw map[string]any, key, val string, overwrite bool) bool {
	if val == "" {
		return false
	}
	cur := coerceStr(raw[key])
	if cur == val {
		return false
	}
	if cur != "" && !overwrite {
		return false
	}
	raw[key] = val
	return true
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
		slug := NumberedSlugAt(base, i)
		ss, exists := p.series[slug]
		if !exists {
			if slug != base {
				warn("series slug %q taken by a different series; using %q for %q", base, slug, name)
			}
			ss = &seriesState{
				slug:      slug,
				name:      name,
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

// loadSeriesRaw reads an existing series entry into ss.raw the first time it is
// extended, so its non-managed fields (authors, xref, existing sources) survive.
// The read is queued-write-first, so a series two rows extend composes.
func (p *planner) loadSeriesRaw(ss *seriesState) {
	if ss.raw != nil || p.fatal != nil {
		return
	}
	ss.raw = p.entryRaw(pack.FamilySeries, ss.slug)
}

// finalizeSeries queues the entry for every new or extended series.
func (p *planner) finalizeSeries() {
	for _, ss := range p.series {
		if !ss.dirty {
			continue
		}
		if ss.isNew {
			p.putNewEntry(pack.FamilySeries, ss.slug, ss.out)
		} else {
			p.putEntry(pack.FamilySeries, ss.slug, ss.raw)
		}
	}
}

// buildChapters maps a book's chapter rows (sourceBook.chapterRows), trimming
// titles and enforcing the same monotonic-from-zero rule metacheck applies. On
// any structural violation it warns and returns nil (the recording is emitted
// without chapters).
func buildChapters(raw []rawChapter, warn func(string, ...any)) []outChapter {
	if len(raw) == 0 {
		return nil
	}
	out := make([]outChapter, 0, len(raw))
	for i, rc := range raw {
		start, sOK := rc.startMS()
		length, lOK := rc.lengthMS()
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

// sameNarratorRecs returns EVERY recording under ws whose narrator set matches
// (in slug order, so a merge target is deterministic), together with the first
// free slug along base's candidate chain for a genuinely new recording.
//
// The match scan deliberately covers ALL of the work's recordings rather than
// just base's chain: the slug embeds the release YEAR, so the US (2019-12) and
// UK (2020-01) releases of one production sit on unrelated chains. Walking only
// the incoming chain minted a second recording for the sibling instead of
// merging its ASIN. The runtime and abridged guards in addRecording remain the
// correctness gate for what may merge.
func sameNarratorRecs(ws *workState, base string, narrators map[string]bool) (matches []recCandidate, freeSlug string) {
	slugs := make([]string, 0, len(ws.recs))
	for slug, existing := range ws.recs {
		if SameSet(existing.narrators, narrators) {
			slugs = append(slugs, slug)
		}
	}
	sort.Strings(slugs)
	for _, slug := range slugs {
		matches = append(matches, recCandidate{slug: slug, info: ws.recs[slug]})
	}
	// The recording candidate formula: base (narrator plus release year), then
	// base-2, base-3, ... - NumberedSlugAt keeps every one within MaxSlugLen.
	for i := 0; ; i++ {
		freeSlug = NumberedSlugAt(base, i)
		if _, taken := ws.recs[freeSlug]; !taken {
			return matches, freeSlug
		}
	}
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
// Every candidate is a valid slug (workSlugAt bounds it to model.MaxSlugLen);
// the bare base already is, coming from Slugify. resolveExistingWork walks this
// same chain to FIND a work, so the bound must live here rather than at either
// call site or the two would stop agreeing on where a work sits.
func workCandidates(base, firstAuthor string) []string {
	out := make([]string, 0, 51)
	out = append(out, base)
	for i := 1; i <= 50; i++ {
		out = append(out, workSlugAt(base, firstAuthor, i))
	}
	return out
}

// workSlugAt builds the i'th disambiguated work-slug candidate (i >= 1):
// "<base>-<firstAuthor>" for i == 1, then "-2", "-3", ... appended for the
// later ones, bounded to model.MaxSlugLen by BoundedSlugTail - so the TITLE is
// what gets shortened. The author credit tells two books sharing a title apart
// and the numeric suffix tells the candidates apart, so neither may be cut away.
//
// The work-specific policy sits on top: when no word boundary fits the pair, the
// credit takes at most half of what the numeric suffix leaves. Otherwise a
// cap-length author slug would swallow the title (every candidate collapsing
// onto the same author string) and a cap-length title would swallow the credit
// (the first candidate repeating the bare base).
//
// Like NumberedSlugAt's, the numbered candidates (i >= 2) are pairwise distinct
// because each ends in its own "-<i>"; candidates 0 and 1 carry no number, so a
// base or a digit-bearing author slug cut at just the wrong offset can make one
// of them equal a later candidate. getOrCreateWork's walk absorbs that as one
// wasted probe of a slug it has already tested.
func workSlugAt(base, firstAuthor string, i int) string {
	numeric := ""
	if i > 1 {
		numeric = fmt.Sprintf("-%d", i)
	}
	credit := "-" + firstAuthor
	if slug, ok := wordBoundedSlugTail(base, credit+numeric); ok {
		return slug
	}
	// TrimRight so a credit cut mid-hyphen cannot meet the numeric suffix as a
	// doubled hyphen; firstAuthor is a valid slug, so a leading run survives.
	if half := (model.MaxSlugLen - len(numeric)) / 2; len(credit) > half {
		credit = strings.TrimRight(credit[:half], "-")
	}
	return BoundedSlugTail(base, credit+numeric)
}

// workSlugTruncated reports whether the i'th candidate had to shorten the title
// to fit MaxSlugLen, i.e. whether the bounded candidate differs from the plain
// "<base>-<author>(-<i>)" formula. Candidate 0 is never shortened here (Slugify
// already bounded the bare base).
func workSlugTruncated(base, firstAuthor string, i int) bool {
	if i == 0 {
		return false
	}
	n := len(base) + len("-"+firstAuthor)
	if i > 1 {
		n += len(fmt.Sprintf("-%d", i))
	}
	return n > model.MaxSlugLen
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
