// Package importer converts external audiobook-library exports into
// audiosilo-meta records on disk. It maps one OpenAudible books.json entry to a
// work + recording (+ people, + series), deduplicating against the existing
// catalog so a contributor's upload lands as a reviewable diff. Only factual
// fields are imported (see LICENSING.md); publisher copy and covers-as-files are
// never touched.
package importer

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
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

// cleanWorkTitle removes the decorations that are not part of a work's identity,
// so two listings of one book resolve to one work. Two rules, in this order
// because the first is a TRAILING marker and the second reads what the title
// ends with:
//
//  1. trailing (Unabridged)/(Abridged)/[Unabridged]/[Abridged] edition markers
//     (all stacked markers in one pass), so "Mageling" and "Mageling
//     (Unabridged)" resolve to one work;
//  2. a mid-title NARRATOR qualifier in front of a volume marker
//     (stripTitleNarratorQualifier), so "... - gelesen von Andreas Lange, Band
//     11" and "... - gelesen von Peter Bocek, Band 11" resolve to the one work
//     the undecorated "..., Band 11" already names.
//
// It never returns an empty string: a title that is ONLY a marker (or trims to
// nothing) is returned unchanged.
func cleanWorkTitle(title string) string {
	cleaned := strings.TrimSpace(title)
	stripped := strings.TrimSpace(editionMarkerRE.ReplaceAllString(cleaned, ""))
	if stripped == "" {
		return cleaned
	}
	return stripTitleNarratorQualifier(stripped)
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
	// seriesPos is every series position the ROW that created this recording
	// claimed (lowercased series name -> position). It is the per-row half of
	// the same-title serial guard: two volumes of a serial published under one
	// title have compatible runtimes and identical narrators, so nothing else in
	// the merge test can tell them apart, and their ASINs merged onto one
	// recording. Empty for a recording loaded from disk (the row that made it is
	// long gone), which is the same "unknown never blocks" posture abridged
	// takes.
	seriesPos map[string]string
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
//
// authors is the IDENTITY author set, which is not the same list as the
// record's authors[]: a person whose only appearance carried a contributor-role
// qualifier is a credit rather than an author for matching purposes (see
// workidentity.go). For a work loaded from disk it is derived from the record's
// authors[] and credits[]; for one created this run, from the row's credits.
type workState struct {
	slug string
	// authors is the IDENTITY set; all is the record's whole credit list, which
	// the subsumption half of matchWork compares against. Keeping both is what
	// lets a work minted before the exclusion rule (no credits[], so its
	// identity IS its whole list) and a role-qualified row recognize each other.
	authors map[string]bool
	all     map[string]bool
	// lang is the work's language. A work is language-scoped: a translated
	// edition is a different work from its original, and merging them makes the
	// work's own language a lie for half its recordings. Empty = unknown, which
	// never blocks a merge (langCompatible).
	lang string
	// posSuffixed records that THIS RUN created the work on the
	// serial-disambiguation path, i.e. that its slug's "book-<position>" tail
	// means what the tail says. A work that merely HAPPENS to be stored under
	// such a slug - 258 of them are in the tree, titled "... Book 3" - is never
	// a merge target for a suffixed row. See getOrCreateWork.
	posSuffixed bool
	recs        map[string]*recInfo
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
	// claimed is every position a work has CLAIMED in this series this run,
	// whether or not the claim became a membership. members only records the
	// claims that landed, and a dropped claim used to make a work look absent
	// from the series - which trivially satisfied the same-position merge test
	// (seriesClaim.compatible), so the next volume of the serial merged into it.
	// First claim wins, exactly as members does.
	claimed map[string]string // work slug -> position it claimed
}

// planner accumulates the writes and warnings for a run.
type planner struct {
	dataDir string
	// people maps every known person slug to the NAME its record carries. The
	// slug is the normalized identity (two names that slug the same are the same
	// person); the name is what the initials probe re-checks a candidate against
	// before it merges two spellings - see getOrCreatePerson and initials.go.
	people map[string]string
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
	// authorCensus / narratorCensus are the evidence universes the two
	// census-consulting cleaning rules read, one per CREDIT SIDE. Both carry the
	// same any-side universe for the studio-concatenation rule (studiotail.go) -
	// the catalogue's person slugs as loaded, plus a census of every credit name
	// the batch carries - and differ only in the side-scoped universe the
	// honorific rule reads (honorific.go). They are SNAPSHOTS taken before any
	// row is planned - see creditCensusesOf - so what a name cleans to cannot
	// depend on the order the rows arrive in. Unrelated to the trust-tier sense
	// of "attested" (attest.go), which is why they do not use that word.
	authorCensus   creditCensus
	narratorCensus creditCensus
	// authorPeople / narratorPeople are the catalogue's own answer to "which
	// side is this person credited on": every person some catalogued work names
	// as an author (or role credit), and every person some catalogued recording
	// names as a narrator. They seed the side-scoped censuses above.
	authorPeople   map[string]bool
	narratorPeople map[string]bool
	// honorificMerges is every credit name the honorific rule resolved onto a
	// bare twin this run, mapped to that twin. A merge of two person records is
	// the least reversible thing an import does, so the wave's list is reported
	// (Summary.HonorificMerges) rather than left to be discovered in a diff.
	honorificMerges map[string]string
	// initialsSurvivors is the initials rule's decision for this run
	// (initials.go): for every person slug whose initials group is written more
	// than one way across the catalogue and the batch, the record that group
	// resolves to. Like creditCensus it is a SNAPSHOT taken before planning, and
	// for the same reason - a merge decided against a map that grows during the
	// run depends on row order, so two runs over the same rows in a different
	// order (or one export split into chunks) would mint different ids.
	//
	// It is consulted BOTH where a person is created (getOrCreatePerson) and
	// where a credit list resolves one (personSlugTarget): the variant slug is
	// never written into p.people, so a credit minted under it has to be
	// redirected here or it would name a record that does not exist.
	initialsSurvivors initialsSurvivors
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
	// titleNoMatchExamples is noWorkExamples' twin for the rows whose TITLE was
	// catalogued but whose credits matched no work under it
	// (Summary.SkippedTitleNoMatch), capped the same way. Kept apart because the
	// two buckets are read for different reasons and mixing the examples would
	// bury the handful that are worth looking at.
	titleNoMatchExamples []string
	// unnamedCredits counts the role-qualified credits workCredits refused
	// because the person's name does not resolve to an identity of their own,
	// and unnamedCreditNames keeps a few distinct spellings for the aggregate
	// warning (capped at maxWarnExamples as it fills). Aggregated because the
	// cause is one property of Slugify, not of any individual row: the seed wave
	// hit it 38 times and 38 identical per-row lines would say no more than one
	// counted line does.
	unnamedCredits     int
	unnamedCreditNames []string
	// unaddressableSeries counts the series claims getOrCreateSeries refused
	// because the series NAME has no addressable slug, and
	// unaddressableSeriesNames keeps a few distinct spellings for the aggregate
	// warning. Aggregated for the same reason as unnamedCredits: the cause is one
	// property of Slugify, and one counted line says everything 32 identical
	// per-row lines would.
	unaddressableSeries      int
	unaddressableSeriesNames []string
	// lostSeriesClaims counts the valid series placements that died with the ROW
	// that claimed them (an unknown language, no narrator, no author, no title),
	// and lostSeriesNames keeps a few series for the aggregate warning. See
	// noteLostSeriesClaims for why this was worth its own counter.
	lostSeriesClaims int
	lostSeriesNames  []string
	// runCredits is the set of contributor credits THIS RUN has written onto
	// each work it created or filled, keyed by work slug. Its PRESENCE is the
	// permission: a work the run itself put credits on (or created) accretes the
	// pairs later rows of the same run state, while a work whose credits came
	// from disk keeps the documented cross-run rule (fill only what is absent).
	// Without it a second row naming a new (person, role) pair on a work the run
	// already touched would be dropped silently.
	runCredits map[string]map[model.Credit]bool
	// sourceType / importDate are the run-wide halves of every provenance stamp
	// (the per-row half is the book's ASIN); setSource composes the three.
	sourceType string
	importDate string
	curSource  OutSource
	// mode is the planning pass this run was asked for. It is kept only so a
	// conflict worklist row can name the run that wrote it; the pass itself is
	// selected once, by runBooks' switch.
	mode Mode
	// conflicts is the run's optional conflict worklist (Options.Conflicts), the
	// durable, machine-readable twin of the contradiction WARNINGS. nil for a run
	// that was not given one, which is every existing caller - see conflicts.go.
	conflicts io.Writer
	fatal     error
	summary   Summary
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
	// through CreditWithRoles, which cleans the name and keeps the roles its
	// trailing qualifier stated) instead of splitting raw's comma-joined
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
	// Refused before ANYTHING reads the batch - before the censuses, before the
	// title pre-pass, before planning - so an AI credit cannot reach the person
	// table, the credit census or a title decision. See refuseAIBooks.
	books, aiRefused := refuseAIBooks(books)
	// Opened before anything is planned: a tree still in the file-per-entity
	// layout is refused here, having written nothing and read nothing it could
	// misinterpret.
	store, err := openStore(opts.DataDir)
	if err != nil {
		return Summary{}, err
	}
	p := &planner{
		dataDir:        opts.DataDir,
		people:         map[string]string{},
		authorPeople:   map[string]bool{},
		narratorPeople: map[string]bool{},
		works:          map[string]*workState{},
		series:         map[string]*seriesState{},
		asins:          map[string]bool{},
		isbns:          map[string]bool{},
		store:          store,
		genres:         audibleGenreTable().withRunMemo(),
		unmappedGenres: map[string]bool{},
		runCredits:     map[string]map[model.Credit]bool{},
		sourceType:     sourceType,
		importDate:     opts.ImportDate,
		mode:           opts.Mode,
		conflicts:      opts.Conflicts,
		userTier:       model.TierOfSource(sourceType) == model.TierUserLibrary,
	}
	if opts.Mode == ModeEnrich || p.userTier {
		p.asinLoc = map[string]RecRef{}
	}
	// Recorded on the summary before planning appends anything, so the AI line
	// is the run's FIRST warning and every return path below carries it.
	p.summary.SkippedRows = aiRefused.n
	if line, warned := aiRefused.warning(); warned {
		p.summary.Warnings = append(p.summary.Warnings, line)
	}
	p.loadExisting()
	p.authorCensus, p.narratorCensus = p.creditCensusesOf(books)
	p.initialsSurvivors = p.decideInitialsOf(books)

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
	p.reportHonorificMerges()
	p.reportUnmappedGenres()
	p.reportUnnamedCredits()
	p.reportUnaddressableSeries()
	p.reportLostSeriesClaims()
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

// ---------------------------------------------------------------------------
// The AI-credit gate
//
// An AI is not a person: importing one mints a person record for a
// text-to-speech engine or a language model. libex refuses such a row at its own
// parse layer (libex.go), where the refusal also has to feed libex-select's
// exclusion reasons - so for a libex run this gate is a NO-OP, by construction
// and not by coincidence: firstAICredit has already rejected every row that
// would trip it.
//
// It lives HERE, in the shared core, because the gate is not a property of one
// source. All three user-library sources are Audible-sourced (pkg/model's trust
// tiers rank openaudible-import, libation-import and audiosilo-books-import
// together) and all three can carry a Virtual Voice title; gating only the
// envelope the site composes let four virtual-voice works into the catalogue.
//
// It reads the credit lists through sourceNames - the ONE place the typed-vs-
// comma-joined choice is made, and the exact list sourceCredits will credit. A
// per-source gate over the source's own array shape could not see an AI name
// INSIDE a comma-joined element ("Jane Doe, Virtual Voice" arrives as one
// element, and a projection may hand narrators over as a plain string), which
// the pipeline then splits into two people. Gate and credits now read the same
// names by construction, so they cannot disagree about what a row credits.
//
// Only the AI vocabulary crosses over. The unidentifiable-name rule stays
// libex-only for the reason documented above firstUnnamedCredit (a user's own
// library keeps the visible catch-all conflation rather than losing them their
// book), and the junk/list/placeholder rules are shapes of a bulk scrape.

// aiRefusals is what a run refused for crediting an AI: every one is counted,
// and the first few are formatted for the aggregated warning. Building the
// example strings is capped at collection time (libex's warningLines does the
// same), so a library that AI-narrates in bulk costs one int per row.
type aiRefusals struct {
	n        int
	examples []string
}

// add records one refusal.
func (r *aiRefusals) add(book, role, name, why string) {
	r.n++
	if len(r.examples) >= maxWarnExamples {
		return
	}
	r.examples = append(r.examples, fmt.Sprintf("%q (%s %q is %s)", book, role, name, why))
}

// warning is the ONE aggregated line the refusals report, in the form every
// aggregated importer warning takes. One line rather than one per book because
// the vocabulary is settled: the news is "these books credit a synthetic voice",
// not which spelling each of them used.
func (r aiRefusals) warning() (string, bool) {
	if r.n == 0 {
		return "", false
	}
	return withExamples(
		fmt.Sprintf("%d books skipped: a credited name is an AI voice or system", r.n),
		r.examples), true
}

// refuseAIBooks drops every book whose author or narrator list names an AI and
// reports what it dropped. The returned slice is the input itself when nothing
// was refused (the common case, and the only case for libex), so a million-row
// enrichment pays no copy.
func refuseAIBooks(books []sourceBook) ([]sourceBook, aiRefusals) {
	var refused aiRefusals
	kept := books
	for i, b := range books {
		role, name, why, isAI := firstAICredit(
			sourceNames(b.authors, b.str("author")),
			sourceNames(b.narrators, b.str("narrated_by")),
		)
		if !isAI {
			if refused.n > 0 {
				kept = append(kept, b)
			}
			continue
		}
		if refused.n == 0 {
			// The first refusal: keep everything before it, with the capacity
			// capped so the appends above allocate rather than overwrite books[i:].
			kept = books[:i:i]
		}
		refused.add(firstNonEmpty(b.str("title_short"), b.str("title")), role, name, why)
	}
	return kept, refused
}

// planCreate is the default (create) planning pass: every book that the
// catalogue does not already hold by ASIN becomes work/recording/person/series
// records, each stamped with the planner's run provenance.
func (p *planner) planCreate(books []sourceBook) {
	normalizeEditionMarkers(books)
	titles := resolveWorkTitles(books)
	// The second title pre-pass, which resolveWorkTitles cannot do on its own:
	// separating rows whose titles are identical even after the full-title
	// fallback and which differ only by series position (workidentity.go).
	suffixes := p.serialPositionSuffixes(books, titles)
	for i, b := range books {
		asin := NormalizeASIN(b.str("asin"))
		p.setSource(asin)
		p.addBook(b, asin, titles[i], suffixes[i])
		if p.fatal != nil {
			return
		}
	}
}

// normalizeEditionMarkers is the batch-boundary title pre-pass every planning
// mode that reads titles runs first. For every book it: (1) derives the abridged
// tri-state from the title's edition marker when the source did not state it,
// then (2) runs cleanWorkTitle over the raw title/title_short. This is the
// SINGLE marker-derivation mechanism for ALL sources (the ABS path already
// cleans its titles locally to fix its subtitle split, but never derives
// abridged), so step 1 must run BEFORE the titles are mutated. Cleaning once
// here means downstream work-title resolution and full-title re-derivation read
// undecorated titles without re-cleaning.
//
// Step 2 is therefore the one place a title CHANGES on its way into identity,
// and cleanWorkTitle performs both of its rules there: the trailing
// (Unabridged)/(Abridged) edition marker, and the mid-title narrator qualifier
// in front of a volume marker ("... - gelesen von Andreas Lange, Band 11" ->
// "..., Band 11", stripTitleNarratorQualifier). A reader chasing "why did this
// title change?" lands here, so both rules are named here rather than only at
// their definitions.
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
//
// It loads THROUGH the store, so the catalogue read and the run's own entry
// reads share one walk and one parse of each pack: the packs the planner then
// composes into are already in hand rather than read a second time.
func (p *planner) loadExisting() {
	cat := check.LoadStore(p.store).Catalog
	if cat == nil {
		return
	}
	for _, person := range cat.People {
		p.people[person.ID] = person.Name
	}
	for _, w := range cat.Works {
		ws := &workState{
			slug:    w.ID,
			authors: diskIdentityAuthors(w.Authors, w.Credits),
			all:     ToSet(w.Authors),
			lang:    w.Language,
			recs:    map[string]*recInfo{},
		}
		for _, c := range w.Credits {
			p.authorPeople[c.Person] = true
		}
		for _, a := range w.Authors {
			p.authorPeople[a] = true
		}
		for _, r := range w.Recordings {
			for _, n := range r.Narrators {
				p.narratorPeople[n] = true
			}
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
			claimed:   map[string]string{},
		}
		for _, sw := range s.Works {
			ss.members[sw.Work] = sw.Position
			ss.positions[sw.Position] = sw.Work
		}
		p.series[s.ID] = ss
	}
	p.seedDiskSeriesPositions(cat.Series)
}

// seedDiskSeriesPositions gives every recording loaded from disk the series
// positions its WORK sits at, so the same-title serial guard (seriesPosConflict)
// works across runs and not only within one.
//
// The guard needs to know which volume a recording is, and the row that created
// it is long gone by the next run. But the fact it needs did not go with the
// row: the work's membership in the series IS that volume number, recorded
// durably, and every recording of a work is a recording of that volume. Without
// this, importing a serial's volumes in two runs reproduced the original
// Bravelands defect exactly - run 2's volume 2 merged its ASIN onto run 1's
// volume 1 recording, because the incumbent stated no position at all.
//
// The key is the series NAME lowercased, matching rowSeriesPositions: the guard
// only ever compares a recording's positions against a row's claims, and a row
// states a name rather than a slug.
func (p *planner) seedDiskSeriesPositions(series []*model.Series) {
	byWork := map[string]map[string]string{}
	for _, s := range series {
		key := strings.ToLower(s.Name)
		for _, sw := range s.Works {
			pos, ok := byWork[sw.Work]
			if !ok {
				pos = map[string]string{}
				byWork[sw.Work] = pos
			}
			// First claim wins, as everywhere else a series position is read.
			if _, taken := pos[key]; !taken {
				pos[key] = sw.Position
			}
		}
	}
	for slug, positions := range byWork {
		ws, known := p.works[slug]
		if !known {
			continue
		}
		for _, ri := range ws.recs {
			ri.seriesPos = positions
		}
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
// (computed once by the caller), workTitle is the pre-pass-resolved title for
// the book's work and posSuffix is the serial-disambiguation tail its work slug
// must carry (empty for almost every row; see serialPositionSuffixes). It
// returns quietly (recording a warning or a skip) whenever the entry cannot be
// imported cleanly.
func (p *planner) addBook(b sourceBook, asin, workTitle, posSuffix string) {
	warn := p.bookWarn(b)

	// Dedup first: an already-present ASIN is a skip, not a warning. It is also
	// the one place a USER's own library meets a record the libex mirror seeded,
	// so the skip is where the trust-tier attestation happens (attest.go); for a
	// libex run attestExisting is a no-op and the skip is exactly what it was.
	if p.dedupeByASIN(asin) {
		p.attestExisting(b, asin)
		return
	}

	lang, narratorNames, ok := p.admitRecordingFacts(b, warn)
	if !ok {
		p.noteLostSeriesClaims(b)
		return
	}
	authorCredits := p.rowAuthorCredits(b)
	if len(authorCredits) == 0 {
		warn("no author; a work requires an author; skipped")
		p.noteLostSeriesClaims(b)
		return
	}

	if workTitle == "" {
		warn("no title; skipped")
		p.noteLostSeriesClaims(b)
		return
	}

	// The row's author slugs, split into the list the record stores and the
	// subset work identity is matched on (workidentity.go). Resolving them here
	// is what creates their person records, exactly as creditSlugs did.
	authors := p.rowWorkAuthors(authorCredits, warn)
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
	// The contributor credits ride along on the same terms and for the same
	// reason: they are a work-creation fact here, and filling them onto a work
	// that already exists is the enrichment pass's job (applyToWork). They travel
	// raw for the same reason too - resolving them is wasted work on every row
	// that merges.
	facts := workFacts{genres: b.genres, credits: authorCredits}
	ws := p.getOrCreateWork(workTitle, b.str("title"), authors, lang, claim, facts, posSuffix, warn)
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
func (p *planner) admitRecordingFacts(b sourceBook, warn func(string, ...any)) (lang string, narratorNames []string, ok bool) {
	lang, ok = mapLanguage(b.str("language"))
	if !ok {
		warn("unknown language %q; skipped", b.str("language"))
		return "", nil, false
	}
	narratorNames = p.rowNarratorNames(b)
	if len(narratorNames) == 0 {
		warn("no narrator; a recording requires narrators; skipped")
		return "", nil, false
	}
	return lang, narratorNames, true
}

// rowAuthorCredits is a row's cleaned AUTHOR-side credit list, each entry
// carrying the roles its trailing qualifier stated. It is the author side only:
// a narrator-side qualifier is stripped exactly as before and states nothing.
//
// That asymmetry is deliberate, not an omission. A qualifier on a narrator
// credit sits on a RECORDING, and work.credits is a fact about the WORK, so
// promoting it would assert a work-level credit from edition-level evidence
// (the same book's other narration need not carry it). It is also negligible:
// measured over the full libex dump, 4.8% of author credits carry a trailing
// qualifier against 0.027% of narrator credits, and most of that 0.027% is the
// same scraping junk - production-company names and bio fragments - that the
// vocabulary refuses anyway. Recording-level credits stay unmodeled until
// there is evidence worth modeling.
func (p *planner) rowAuthorCredits(b sourceBook) []credit {
	return sourceCredits(b.authors, b.str("author"), p.authorCensus)
}

// rowNarratorNames is a row's cleaned narrator list, read from the source's
// structured list when it has one and from its comma-joined string otherwise
// (sourceCredits owns that choice). There is no author-side twin: every author
// path needs the ROLES too, so it goes through rowAuthorCredits.
func (p *planner) rowNarratorNames(b sourceBook) []string {
	return creditNamesOf(sourceCredits(b.narrators, b.str("narrated_by"), p.narratorCensus))
}

// creditCensusOf builds this run's credit census: the set of slugs a name must
// land on to count as "independently a credit somewhere", which is the evidence
// the studio-concatenation rule's third tier consults (studiotail.go).
//
// The report that specified that rule measured the question against the whole
// 1.13M-book libex dump, which the importer does not have and must never carry a
// copy of (LICENSING.md's import posture: a bounded source, never a mirror). The
// two things it DOES have are exactly the two the report names as the practical
// substitute, and both are evidence of the same kind - a name somebody actually
// credited:
//
//	the catalogue  every person record already committed, as loaded (14.9k after
//	               seed wave 1). This is where "Alex Hyde-White" and "Punch
//	               Audio" both come from: the studio has a record of its own,
//	               which is precisely what makes the concatenation visible.
//	the batch      a census of every credit name in the rows being imported,
//	               author and narrator alike, in the source's own spelling AND
//	               in its self-evidencing cleaned form (tiers 1-2, which need no
//	               census). The cleaned form is what lets one row's "<narrator>
//	               for HotGhost Productions" attest the narrator for a second row
//	               that spells the same credit bare.
//
// It is deliberately a SNAPSHOT, taken after loadExisting and before the first
// row is planned, rather than a live read of p.people: consulting a set that
// grows as records are created would make a name's cleaning depend on the order
// the rows happen to arrive in, and two runs over the same export could disagree.
// This is the same batch-pre-pass shape resolveWorkTitles uses, for the same
// reason.
//
// The universe being SMALLER than the dump only ever costs a missed cleanup
// (the name imports as the source spelled it, which is what happens today and is
// a maintainer PR away from fixed). It cannot cost a wrong one: every tier that
// consults it requires MORE evidence than the tiers that do not.
// The SIDE-scoped universes are built in the same walk and from the same names,
// and they are the honorific rule's evidence. Their catalogue half cannot come
// from p.people, which records only that a person exists: it comes from what the
// catalogue says each person DID (p.authorPeople / p.narratorPeople, collected
// in loadExisting), because that is the question the rule asks.
func (p *planner) creditCensusesOf(books []sourceBook) (author, narrator creditCensus) {
	// Most rows repeat an author and a narrator the catalogue or an earlier row
	// already carries, so the batch contributes far fewer new keys than it has
	// credits; a per-row hint over-allocates by ~90MB on a 1M-row dump.
	universe := make(map[string]bool, len(p.people)+len(books)/2)
	for slug := range p.people {
		universe[slug] = true
	}
	authorSide := make(map[string]bool, len(p.authorPeople)+len(books)/4)
	for slug := range p.authorPeople {
		authorSide[slug] = true
	}
	narratorSide := make(map[string]bool, len(p.narratorPeople)+len(books)/4)
	for slug := range p.narratorPeople {
		narratorSide[slug] = true
	}
	record := func(side map[string]bool, name string) {
		slug := Slugify(name)
		if slug == "" {
			return
		}
		universe[slug] = true
		side[slug] = true
	}
	recordSide := func(side map[string]bool, names []string) {
		for _, name := range names {
			record(side, name)
			// The name as the self-evidencing tiers would clean it. Passing a
			// zero census is what keeps this bootstrap honest: the census is
			// built from rules that never consult the census. The overwhelming
			// majority of names clean to themselves, and re-slugging those is
			// pure waste.
			if cleaned, _ := creditWithRoles(name, nil); cleaned != name {
				record(side, cleaned)
			}
		}
	}
	for _, b := range books {
		recordSide(authorSide, sourceNames(b.authors, b.str("author")))
		recordSide(narratorSide, sourceNames(b.narrators, b.str("narrated_by")))
	}
	seenIn := func(set map[string]bool) creditSeenFunc {
		return func(name string) bool { return set[Slugify(name)] }
	}
	anySide := seenIn(universe)
	author = creditCensus{anySide: anySide, sameSide: seenIn(authorSide), onHonorific: p.noteHonorific}
	narrator = creditCensus{anySide: anySide, sameSide: seenIn(narratorSide), onHonorific: p.noteHonorific}
	return author, narrator
}

// noteHonorific records one honorific merge for the run's report. It is a SET
// of (credited spelling -> bare twin) pairs rather than a count: the same
// spelling is cleaned once per row it appears on, and what a maintainer audits
// is which merges happened, not how often each fired.
func (p *planner) noteHonorific(from, to string) {
	if p.honorificMerges == nil {
		p.honorificMerges = map[string]string{}
	}
	p.honorificMerges[from] = to
}

// reportHonorificMerges publishes the run's honorific merges, sorted, as
// "<credited> -> <bare>" lines.
func (p *planner) reportHonorificMerges() {
	if len(p.honorificMerges) == 0 {
		return
	}
	out := make([]string, 0, len(p.honorificMerges))
	for from, to := range p.honorificMerges {
		out = append(out, from+" -> "+to)
	}
	sort.Strings(out)
	p.summary.HonorificMerges = out
}

// decideInitialsOf is the initials rule's batch pre-pass (initials.go): which
// spelling each initials group resolves to, decided over the catalogue as loaded
// plus every credit name the batch carries, before a single row is planned.
//
// It runs AFTER creditCensusOf and cleans each name through the finished census,
// because the name getOrCreatePerson will be handed is the CLEANED one - a row
// spelling a narrator "<name> for HotGhost Productions" contributes that
// narrator's spelling, not the concatenation's. That is also why this cannot be
// folded into the census loop: the census has to be complete before a name can
// be cleaned through it.
func (p *planner) decideInitialsOf(books []sourceBook) initialsSurvivors {
	c := newInitialsCensus()
	for slug, name := range p.people {
		c.addCatalogue(slug, name)
	}
	for _, b := range books {
		// Each side is cleaned through ITS OWN census, exactly as the import
		// itself will clean it: the honorific rule is side-scoped, so a pre-pass
		// reading both sides through one census would decide an initials group
		// against a spelling the import never produces.
		for _, name := range sourceNames(b.authors, b.str("author")) {
			cleaned, _ := creditWithRolesSided(name, p.authorCensus)
			c.addBatch(cleaned)
		}
		for _, name := range sourceNames(b.narrators, b.str("narrated_by")) {
			cleaned, _ := creditWithRolesSided(name, p.narratorCensus)
			c.addBatch(cleaned)
		}
	}
	return c.decide()
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

// makeSeriesRef builds a book's claim to a position in a named series,
// validating the raw position token through the shared rules. Every source
// builds its refs here so one spelling of a position ("1.0") can never become a
// different position from another ("1"), and it normalizes a trailing narrator
// qualifier out of the NAME (cleanSeriesName) so one serial cannot fork into a
// series per narrator.
func makeSeriesRef(name, rawSeq string) seriesRef {
	pos, ok := NormalizeSequence(rawSeq)
	return seriesRef{name: cleanSeriesName(name), seq: pos, seqOK: ok, rawSeq: rawSeq}
}

// sourceCredits resolves one credit list (authors or narrators). A source that
// parsed structured credits passes them in typed, and they are used verbatim
// (trimmed, credit-cleaned, empties dropped) - splitting them on commas
// would tear "Alexandre Dumas, pere" into two people. A source that only has the
// retailer's comma-joined string passes it as joined and it is split.
//
// Either way each entry keeps the roles its qualifier stated, so the two shapes
// a source can hand credits over in produce the same facts.
func sourceCredits(typed []string, joined string, c creditCensus) []credit {
	names := sourceNames(typed, joined)
	if len(names) == 0 {
		return nil
	}
	out := make([]credit, 0, len(names))
	for _, name := range names {
		cleaned, roles := creditWithRolesSided(name, c)
		out = append(out, credit{name: cleaned, roles: roles})
	}
	return out
}

// sourceNames is the raw name list a source states for one credit side, in the
// SOURCE's own spelling: the structured list when the source parsed one, else
// its comma-joined string split on commas. Either way the names are trimmed and
// empties dropped.
//
// It is the ONE place that typed-vs-joined choice is made. Both the credit
// pipeline (sourceCredits) and the batch census (creditCensusOf) read a row
// through it, so the census can never be built from a different set of names
// than the import itself reads.
func sourceNames(typed []string, joined string) []string {
	if len(typed) == 0 {
		return splitRawNames(joined)
	}
	out := make([]string, 0, len(typed))
	for _, name := range typed {
		if n := strings.TrimSpace(name); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// workCredits turns a row's author-side credits into the work's credits list:
// one (person, role) entry per stated role, in sorted, deduplicated order.
//
// It filters to people the planner KNOWS - already on disk or created earlier
// this run - which is what makes the emitted list satisfy metacheck's
// credit-integrity rule by construction. On the create path every author has
// just been created, so nothing is dropped; on the enrichment path, which
// creates nothing, an unknown person is silently skipped rather than written as
// a dangling reference.
//
// A name that slugs away to nothing is dropped too, and that one is a FACTS
// rule rather than a referential one. personSlug substitutes the shared
// catch-all "person" record for a name written entirely in a script Slugify
// folds (Korean, Cyrillic, CJK); on the authors list that fallback is a known,
// visible conflation, but a credit is an explicit claim about who did what, so
// writing {person: "person", role: "translator"} would assert that one shared
// record translated every such book - metacheck-green and false. An
// unidentifiable person cannot be credited. The drop is reported in aggregate
// (reportUnnamedCredits) rather than silently, because the fix is upstream in
// Slugify, not in the row.
//
// The person keeps their ordinary membership in authors: a credit ADDS the role
// the source stated, it never replaces the credit list the identity model is
// built on.
func (p *planner) workCredits(credits []credit) []model.Credit {
	var out []model.Credit
	seen := map[model.Credit]bool{}
	for _, c := range credits {
		if len(c.roles) == 0 {
			continue
		}
		slug, fellBack := personSlug(c.name)
		if fellBack {
			p.noteUnnamedCredit(c.name)
			continue
		}
		// The person may have been created under another spelling of their
		// initials, which is a record this credit must NAME rather than miss:
		// resolving "A.B. Kovacs" straight through personSlug lands on a slug
		// nothing created, and the role credit was silently dropped.
		slug = p.personSlugTarget(slug)
		if _, known := p.people[slug]; !known {
			continue
		}
		for _, role := range c.roles {
			entry := model.Credit{Person: slug, Role: role}
			if seen[entry] {
				continue
			}
			seen[entry] = true
			out = append(out, entry)
		}
	}
	sortCredits(out)
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
//
// The one identity Slugify cannot see is an initials group respelled across the
// separator boundary ("A.B. Kovacs" -> a-b-kovacs, "AB Kovacs" -> ab-kovacs).
// The run's pre-pass has already decided which spelling each such group resolves
// to (initials.go), and it is consulted only when the minted slug is about to
// become a NEW record: an id already in the catalogue or already created this
// run is returned untouched, so nothing on disk ever moves and nothing needs
// migrating.
//
// When the decision names a spelling that does not exist yet, the record is
// created under THAT spelling rather than the one this row happens to use.
// Otherwise the surviving name would be whichever row arrived first, which is
// the order dependence the pre-pass exists to remove.
func (p *planner) getOrCreatePerson(name string, warn func(string, ...any)) string {
	slug, fellBack := personSlug(name)
	if fellBack {
		warn("name %q produced an empty slug; using %q", name, slug)
	}
	if _, known := p.people[slug]; known {
		return slug
	}
	if survivor, merges := p.initialsMerge(name); merges {
		if _, known := p.people[survivor.slug]; known {
			return survivor.slug
		}
		return p.createPerson(survivor.slug, survivor.name)
	}
	return p.createPerson(slug, name)
}

// createPerson emits a new person record. The caller has already established
// that slug is free.
func (p *planner) createPerson(slug, name string) string {
	p.people[slug] = name
	p.putNewEntry(pack.FamilyPeople, slug, OutPerson{
		ID: slug, Name: name, License: licenseCC0, Sources: []OutSource{p.curSource},
	})
	p.summary.NewPeople++
	return slug
}

// personSlugTarget resolves a minted person slug onto the record that actually
// holds that person. It is the read-only half of the initials merge: the variant
// slug is deliberately never written into p.people (an id nothing created is a
// dangling reference), so every path that RESOLVES a credit rather than creating
// one has to ask here or it would drop the credit - the merged record is real,
// but it sits at the other spelling's address.
//
// A slug that names an existing record is always returned as-is. That is the
// guard that keeps the decision from redirecting credits away from a record the
// catalogue already holds: a pre-existing pair of spellings stays a pair, each
// serving its own credits, until a maintainer merges them.
func (p *planner) personSlugTarget(slug string) string {
	if _, known := p.people[slug]; known {
		return slug
	}
	if survivor, decided := p.initialsSurvivors[slug]; decided {
		return survivor.slug
	}
	return slug
}

// personSlug derives a credit name's person identity, substituting the shared
// catch-all record when the name slugs away to nothing (a name in a script that
// folds entirely). fellBack reports that substitution so a caller that CREATES
// the record can warn about it, while a caller that only MATCHES
// (rowWorkAuthorsRO)
// stays silent. Both go through here so a name resolves to one identity
// everywhere.
//
// The rule itself is model.PersonSlug: pkg/check verifies committed ids against
// it (checkPersonSlug) and cannot import this package, so the minting and the
// checking share one definition rather than two that can drift.
func personSlug(name string) (slug string, fellBack bool) { return model.PersonSlug(name) }

// seriesClaim is a book's claim to a position in an already-known series.
type seriesClaim struct {
	ss  *seriesState
	pos string
}

// compatible reports whether merging the book into work ws is consistent with
// its series claim. No claim, a work that has neither taken nor claimed a
// position in the series, or the same position all merge; the same series at a
// DIFFERENT position means ws is a different volume that merely shares the
// title.
//
// It reads claimed as well as members because the two answer different
// questions. members is where the work ENDED UP, and a placement can be dropped
// (its position was already taken by a sibling edition) - which left the work
// looking absent from the series and made every later volume compatible with
// it. claimed is what the work ASKED for, which is the fact this test needs.
func (c *seriesClaim) compatible(ws *workState) bool {
	if c == nil {
		return true
	}
	if existing, in := c.ss.members[ws.slug]; in {
		return existing == c.pos
	}
	if wanted, asked := c.ss.claimed[ws.slug]; asked {
		return wanted == c.pos
	}
	return true
}

// places is compatible's POSITIVE half: it reports whether the series says ws
// sits at exactly the position this row claims, rather than merely failing to
// contradict it. It reads the same two maps for the same reasons, and differs
// only in what it makes of silence: a work the series has never heard of is
// compatible with every row but placed by none.
//
// That distinction is what lets a suffixed slug be a merge target at all. A
// work stored as "<title>-book-3" is either volume 3 of the row's serial or an
// unrelated book whose TITLE ends "Book 3" (258 of them are in the tree), and
// the series record is the only thing that can tell them apart.
func (c *seriesClaim) places(ws *workState) bool {
	if c == nil {
		return false
	}
	if existing, in := c.ss.members[ws.slug]; in {
		return existing == c.pos
	}
	wanted, asked := c.ss.claimed[ws.slug]
	return asked && wanted == c.pos
}

// position reduces the claim to the (series, position) pair the suffix formulas
// need. The series NAME is the catalogued one rather than the row's spelling;
// findSeries matched the two case-insensitively, so they slugify alike and the
// probe lands where the pre-pass mints.
func (c *seriesClaim) position() positionClaim {
	if c == nil {
		return positionClaim{}
	}
	return positionClaim{series: c.ss.name, pos: c.pos}
}

// workFacts are the facts a row contributes ONLY to a work it creates: the raw
// genre claims and the row's source credits. Both travel RAW and are resolved at
// the point of storage (mapGenres, workCredits), so a row that merges into an
// existing work pays for neither. They travel as one value so the full-title
// retry below carries them through unchanged, and so adding a creation-only fact
// is one field rather than one more parameter on every hop.
type workFacts struct {
	genres  []genreClaim
	credits []credit
}

// getOrCreateWork returns the work identified by (title-slug, identity author
// set), creating it when new. A same-author work that the book's series claim
// rules out (same series, different position) is not a merge target: the slug is
// re-derived from the full title, with the candidate chain (author suffix,
// then numeric) only as the last-resort collision fallback. A collision with a
// different author set appends the first author's slug, then numeric suffixes,
// and warns. facts are the creation-only facts (genres, credits); they are
// stored only on the branch that creates a work, which is the only place they
// can be stored, and ride through the full-title retry unchanged.
//
// Three tests can rule a candidate out even when its authors answer, and each
// one exists because it was measured firing the wrong way:
//
//   - the LANGUAGE test. A work is language-scoped, so a German translation may
//     not merge into its English original however identical their credits are
//     (langCompatible; a 20,000-row wave-6 simulation left 82 more
//     cross-language recordings in the tree without it).
//   - the SERIES CLAIM test. A same-author work the row's series claim rules out
//     is a different volume sharing a short title; the slug is re-derived from
//     the full title once, with the candidate chain only as the last resort.
//   - the POSITION-SUFFIX test. See posSuffix below.
//
// posSuffix is the serial-disambiguation tail (workidentity.go): when the batch
// pre-pass found that this row shares its title with a sibling volume, the tail
// is appended to the title base BEFORE the collision chain, so each volume gets
// its own slug instead of the chain merging them. It is empty for almost every
// row.
//
// A SUFFIXED candidate - the row's own suffixed base, or one of the position
// probes workCandidates adds for a claim-bearing row - may only be merged into
// when something says its "book-<position>" tail means what it says: either THIS
// RUN created the work on the suffixed path (workState.posSuffixed), or the
// row's series claim PLACES that work at exactly the position it claims
// (seriesClaim.places). The tree holds 258 works whose slug already looks like
// "<something>-book-3" because their TITLE ends that way, and they are not
// volume 3 of the row's serial - they are unrelated books that happen to spell
// the slug the pre-pass mints. Neither test can reach one: no run created it,
// and no series records it at that position. Merging into one silently files a
// recording under a different book, which is exactly what the pre-pass exists to
// prevent.
//
// The placement test is what makes the suffix survive its own run. A batch mints
// "<title>-book-1" and records it in the series; the NEXT run's row for that
// volume - alone, so the pre-pass never fires, or batched, so it composes the
// same suffix - finds it, because the series says that work is volume 1. Without
// it the second run mints a duplicate whichever path it takes.
//
// The full-title retry does NOT fire under a posSuffix, and that is structural
// rather than an omission: a row only carries a suffix when resolveWorkTitles
// left its resolved title equal to its full title (a suffix is minted precisely
// for the rows the full-title fallback could not separate), so the retry's own
// precondition - a full title that differs - can never hold. It is skipped
// explicitly so that reading the code says so.
func (p *planner) getOrCreateWork(title, fullTitle string, authors workAuthors, lang string, claim *seriesClaim, facts workFacts, posSuffix string, warn func(string, ...any)) *workState {
	base := Slugify(title)
	if base == "" {
		base = "untitled"
		warn("title %q produced an empty slug; using %q", title, base)
	}
	// A row that states a series position may LOOK at the suffixed slugs the
	// serial pre-pass mints for that position, so a lone volume finds the work an
	// earlier batch created there. Not when this row is itself suffixed: its base
	// already carries the tail, and probing a second one would address
	// "<title>-book-1-book-1".
	probe := positionClaim{}
	if posSuffix != "" {
		base = BoundedSlugTail(base, "-"+posSuffix)
	} else {
		probe = claim.position()
	}
	cands, primary := workCandidates(base, authors, probe)

	// The walk grades every candidate rather than taking the first that answers:
	// two candidates can both reduce to the row's identity set (the-iliad and
	// the-iliad-robert-fitzgerald both reduce to Homer) and only the whole
	// credit list says which of the two the row is.
	best, bestKind, free, blocked := -1, matchNone, -1, false
	for i, cand := range cands {
		ws, exists := p.works[cand.slug]
		if !exists {
			if free < 0 && !cand.probeOnly {
				free = i
			}
			if free >= 0 && i >= primary {
				break
			}
			continue
		}
		kind := matchWork(ws, authors)
		if kind == matchNone || !langCompatible(ws.lang, lang) {
			continue
		}
		if (posSuffix != "" || cand.posSuffixed) && !ws.posSuffixed && !claim.places(ws) {
			continue
		}
		if !claim.compatible(ws) {
			blocked = true
			continue
		}
		if kind > bestKind {
			best, bestKind = i, kind
		}
		if bestKind == matchExact {
			break
		}
	}

	if best >= 0 {
		ws := p.works[cands[best].slug]
		// A later row of this run, merging into a work the run created: its
		// credits are not a second source's account of an existing work, they are
		// more of the same import, so the pairs the entry does not carry yet are
		// merged in (a no-op for a work loaded from disk, which is never in
		// runCredits).
		p.mergeCreatedWorkCredits(ws.slug, facts.credits)
		// A merge onto a SHORTENED candidate is the one case where the slug no
		// longer carries the whole title: two different long titles by one author
		// agreeing up to the cut land here as a single work. The identity model
		// accepts that collision risk, but it must not be silent - on the
		// unbounded formula it surfaced as an invalid slug.
		if !cands[best].probeOnly && workSlugTruncated(base, authors.first(), best) {
			warn("work slug for %q was shortened to fit; merging into existing work %q - verify these are the same book", title, ws.slug)
		}
		return ws
	}

	// Nothing answered. A candidate the SERIES CLAIM ruled out means the row is
	// a different volume that merely shares this short title, so re-derive from
	// the full title once before minting anything.
	if blocked && posSuffix == "" {
		if full := Slugify(fullTitle); fullTitle != title && full != "" && full != base {
			return p.getOrCreateWork(fullTitle, "", authors, lang, claim, facts, posSuffix, warn)
		}
	}
	if free < 0 {
		// Unreachable in practice: 50 numeric candidates never all collide.
		return nil
	}
	slug := cands[free].slug
	if slug != base {
		warn("work slug %q taken by a different book; using %q for %q", base, slug, title)
	}
	ws := &workState{
		slug: slug, authors: authors.set(), all: authors.allSet(), lang: lang,
		posSuffixed: posSuffix != "", recs: map[string]*recInfo{},
	}
	p.works[slug] = ws
	// added_at is stamped here and only here for a work: this is the branch that
	// CREATES one. A merge onto an existing work, and every enrichment backfill,
	// leave the field as they found it.
	//
	// putNewEntry, not putEntry: p.works comes from a best-effort catalogue load,
	// so a work the loader could not decode looks free here, and a plain upsert
	// would replace its whole composite entry - every recording included.
	credits := p.workCredits(facts.credits)
	p.putNewEntry(pack.FamilyWorks, slug, outWork{
		ID: slug, Title: title, Authors: authors.all, Language: lang,
		Credits: credits,
		Genres:  p.genres.mapGenres(facts.genres, p.unmappedGenres),
		AddedAt: p.importDate,
		License: licenseCC0, Sources: []OutSource{p.curSource},
	})
	p.summary.NewWorks++
	p.summary.Credits += len(credits)
	p.recordRunCredits(slug, credits)
	return ws
}

// findSeries returns the already-known series (existing on disk or created this
// run) that name resolves to, or nil - it never creates. It walks the same
// candidate chain as getOrCreateSeries so both resolve a name identically,
// including the refusal: a name with no addressable slug resolves to nothing,
// because nothing was ever minted under it.
func (p *planner) findSeries(name string) *seriesState {
	base := Slugify(name)
	if base == "" {
		return nil
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
			// A sibling recording whose row claimed a DIFFERENT position in a
			// series this row also claims is a different volume, however alike the
			// two productions look. Checked before the runtime and abridged guards
			// because it is the only one that can tell two volumes of a serial
			// apart.
			if series, incumbent, want, conflict := seriesPosConflict(m.info, b); conflict {
				warn("recording %q is at position %q of series %q; this row claims %q - not merging its ASIN",
					m.slug, incumbent, series, want)
				continue
			}
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

	ri := &recInfo{
		narrators: narrSet, asins: map[string]bool{}, runtimeMin: b.runtimeMin,
		abridged: b.abridged, seriesPos: rowSeriesPositions(b),
	}
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

// noteUnnamedCredit records one credit workCredits refused because the name has
// no identity of its own (see the fell-back branch there). Distinct spellings
// only, capped, so the aggregate line names examples without listing a run's
// worth of them; the COUNT is what says how much was dropped.
func (p *planner) noteUnnamedCredit(name string) {
	p.unnamedCredits++
	if len(p.unnamedCreditNames) >= maxWarnExamples || slices.Contains(p.unnamedCreditNames, name) {
		return
	}
	p.unnamedCreditNames = append(p.unnamedCreditNames, name)
}

// reportUnnamedCredits appends one run-level warning for the credits dropped
// because their person could not be identified. It is deliberately a warning
// and not a silent drop: the names are real contributors the catalogue is
// failing to represent, and the line is the standing evidence for fixing
// Slugify's handling of non-Latin scripts (which would also un-conflate the
// authors those same names already produce).
func (p *planner) reportUnnamedCredits() {
	if p.unnamedCredits == 0 {
		return
	}
	p.summary.Warnings = append(p.summary.Warnings, withExamples(
		fmt.Sprintf("%d role-qualified credits dropped: the credited name does not resolve to an identifiable person",
			p.unnamedCredits),
		p.unnamedCreditNames))
}

// noteUnaddressableSeries records one series claim refused because the name has
// no addressable slug (see getOrCreateSeries). Distinct spellings only, capped,
// on the same terms as noteUnnamedCredit.
func (p *planner) noteUnaddressableSeries(name string) {
	p.unaddressableSeries++
	if len(p.unaddressableSeriesNames) >= maxWarnExamples || slices.Contains(p.unaddressableSeriesNames, name) {
		return
	}
	p.unaddressableSeriesNames = append(p.unaddressableSeriesNames, name)
}

// noteLostSeriesClaims records the series memberships a row takes down with it
// when the row itself cannot be imported. It is the one series-drop path that
// used to be entirely invisible: the row's own warning says why the ROW was
// dropped, and nothing at all said that a series lost a volume.
//
// Measured over seed wave 5, 388 refused rows carried 389 valid positioned
// claims that vanished this way - and 241 of those rows were refused only for a
// language the map did not carry, which is exactly the kind of fixable cause an
// aggregate count surfaces and a per-row silence hides.
//
// It is deliberately a COUNT with examples rather than a line per claim: the
// cause is never the series, it is always the row, which has already been
// reported on its own terms.
func (p *planner) noteLostSeriesClaims(b sourceBook) {
	for _, r := range b.series {
		if !r.seqOK {
			continue
		}
		p.lostSeriesClaims++
		if len(p.lostSeriesNames) >= maxWarnExamples || slices.Contains(p.lostSeriesNames, r.name) {
			continue
		}
		p.lostSeriesNames = append(p.lostSeriesNames, r.name)
	}
}

// reportLostSeriesClaims appends one run-level warning for those claims.
func (p *planner) reportLostSeriesClaims() {
	if p.lostSeriesClaims == 0 {
		return
	}
	p.summary.Warnings = append(p.summary.Warnings, withExamples(
		fmt.Sprintf("%d series placements lost: the row claiming the position could not be imported",
			p.lostSeriesClaims),
		p.lostSeriesNames))
}

// reportUnaddressableSeries appends one run-level warning for the series claims
// dropped because their name has no addressable slug. Like reportUnnamedCredits
// it is a warning and not a silent drop: these are real series the catalogue is
// failing to represent, and the line is the standing evidence for teaching
// Slugify to transliterate.
func (p *planner) reportUnaddressableSeries() {
	if p.unaddressableSeries == 0 {
		return
	}
	p.summary.Warnings = append(p.summary.Warnings, withExamples(
		fmt.Sprintf("%d series claims dropped: the series name does not resolve to an addressable slug",
			p.unaddressableSeries),
		p.unaddressableSeriesNames))
}

// recordRunCredits remembers what this run wrote onto a work, and is what a
// later row of the same run merges into (see runCredits). Called on the branch
// that CREATES a work - with an empty list when the row stated no role, because
// the permission to accrete comes from the run having created the work, not
// from the first row happening to carry a credit.
func (p *planner) recordRunCredits(workSlug string, credits []model.Credit) {
	set := make(map[model.Credit]bool, len(credits))
	for _, c := range credits {
		set[c] = true
	}
	p.runCredits[workSlug] = set
}

// addRunCredits merges the (person, role) pairs a later row of the SAME run
// states onto a work this run already wrote, and reports how many were new.
//
// It exists because the two planning paths both resolve several rows onto one
// work - the create path merges every same-author row into it, and an
// enrichment run matches every ASIN of one book to it - and the first row was
// the only one whose credits were kept: every later row met a non-empty list
// and the fill-absent rule dropped it. Within a run that rule is the wrong one.
// It says an EXISTING description of a work is not to be spliced together from
// two sources, and a work this run is itself writing has no such description to
// protect.
//
// Cross-run semantics are untouched: p.runCredits starts empty every run, so a
// work whose credits are on disk is never merged into, and a re-run of an
// identical import writes nothing.
//
// The merged list is built from the TRACKED set rather than from the record's
// decoded credits. They are the same list - the run wrote it, and every branch
// that writes updates the set in the same breath - and building it from what
// the run knows keeps the merge from depending on re-decoding a raw JSON array
// it just serialized. added is 0, and merged nil, when the row states nothing
// the work does not already carry, so the caller writes nothing.
func (p *planner) addRunCredits(workSlug string, stated []model.Credit) (merged []model.Credit, added int) {
	have, touched := p.runCredits[workSlug]
	if !touched {
		return nil, 0
	}
	for _, c := range stated {
		if have[c] {
			continue
		}
		have[c] = true
		added++
	}
	if added == 0 {
		return nil, 0
	}
	merged = make([]model.Credit, 0, len(have))
	for c := range have {
		merged = append(merged, c)
	}
	sortCredits(merged)
	return merged, added
}

// mergeCreatedWorkCredits is the create path's half of the in-run merge: a row
// that resolved onto a work THIS RUN created contributes the (person, role)
// pairs the work does not carry yet.
//
// It is a no-op - and costs no store read - for a work loaded from disk, for a
// row that states no role, and for a row whose every pair is already there,
// which is the overwhelming majority of rows. A row that does add one stamps
// its provenance on the work, because the work now records a fact that came
// from it. (A store read that fails is fatal to the whole run, so the tracked
// set having moved ahead of the record is never observable.)
func (p *planner) mergeCreatedWorkCredits(workSlug string, stated []credit) {
	if _, touched := p.runCredits[workSlug]; !touched {
		return
	}
	merged, added := p.addRunCredits(workSlug, p.workCredits(stated))
	if added == 0 {
		return
	}
	raw := p.workEntryRaw(workSlug)
	if raw == nil {
		return
	}
	raw["credits"] = merged
	p.summary.Credits += added
	p.stampSource(raw)
	p.putWorkEntry(workSlug, raw)
}

// sortCredits orders credits by (person, role), which is the ONE byte-form a
// given set of credits ever has in an imported record. The list is not
// source-ordered like authors: an author list's order is the source's statement
// about billing, while a credit list is a set of independent (person, role)
// facts, so a total order is what keeps two runs that state the same facts in a
// different sequence from producing two different files.
func sortCredits(credits []model.Credit) {
	sort.Slice(credits, func(i, j int) bool {
		if credits[i].Person != credits[j].Person {
			return credits[i].Person < credits[j].Person
		}
		return credits[i].Role < credits[j].Role
	})
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

// rowSeriesPositions is a row's valid series claims as a lookup (lowercased
// series name -> position), for the recording-level serial guard. The name is
// lowercased rather than slugified because it is only ever compared against
// another row's claim, and findSeries already treats the name
// case-insensitively. First claim wins, matching addToSeries.
func rowSeriesPositions(b sourceBook) map[string]string {
	var out map[string]string
	for _, r := range b.series {
		if !r.seqOK {
			continue
		}
		key := strings.ToLower(r.name)
		if out == nil {
			out = map[string]string{}
		}
		if _, taken := out[key]; !taken {
			out[key] = r.seq
		}
	}
	return out
}

// seriesPosConflict reports whether a row and an existing recording state
// DIFFERENT positions in the same series, which makes them different volumes
// however compatible their runtimes are. It names the series and both positions
// so the refusal to merge can say what it saw.
//
// A recording with no recorded claims (every recording loaded from disk) never
// conflicts: the guard only ever fires on evidence, never on absence.
func seriesPosConflict(ri *recInfo, b sourceBook) (series, incumbent, want string, conflict bool) {
	if len(ri.seriesPos) == 0 {
		return "", "", "", false
	}
	for _, r := range b.series {
		if !r.seqOK {
			continue
		}
		if have, in := ri.seriesPos[strings.ToLower(r.name)]; in && have != r.seq {
			return r.name, have, r.seq, true
		}
	}
	return "", "", "", false
}

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
	if ss == nil {
		// The name has no addressable slug (see getOrCreateSeries). The row still
		// imports; the work is simply not placed in this series.
		return
	}
	// Recorded BEFORE the two drop tests, and for every claim: what a work asked
	// for is what seriesClaim.compatible needs to know, and a claim that is
	// dropped here is exactly the case where members cannot say (see
	// seriesState.claimed).
	if _, asked := ss.claimed[work]; !asked {
		ss.claimed[work] = pos
	}
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
//
// It returns nil when the name has NO addressable slug - a name written entirely
// in a script Slugify keeps nothing of (Cyrillic, Japanese, Arabic; the same
// property behind the unidentifiable-credit refusal in libex.go). This used to
// fall back to the base "series", which is the one collision base that is
// guaranteed to collide: every such name in a run takes the next free
// series-2/series-3/... slug, so a series is addressed by WHEN it was seen rather
// than by what it is called. One wave minted 32 of them, and because the order
// rows arrive in is not stable, a re-run of the same input places different
// series at the same slugs - a degenerate, non-reproducible identity.
//
// Refusing the claim mirrors the unidentifiable-credit posture exactly: an
// identity this catalogue cannot address is not minted. The cost is bounded and
// honest - the work still imports, it is simply not placed in the series, which
// is true - and it is cheap to reverse the day Slugify learns to transliterate.
func (p *planner) getOrCreateSeries(name string, warn func(string, ...any)) *seriesState {
	base := Slugify(name)
	if base == "" {
		p.noteUnaddressableSeries(name)
		return nil
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
				claimed:   map[string]string{},
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

// workCandidate is one slug a row's work may sit on. probeOnly marks a slug
// that is a place to LOOK but never a place to create: see workCandidates.
// posSuffixed marks a slug that carries a serial-position tail, which is what
// makes it subject to getOrCreateWork's suffixed-merge-target rule.
type workCandidate struct {
	slug        string
	probeOnly   bool
	posSuffixed bool
}

// workCandidates yields the ordered slug candidates for a work, and how many of
// them are PRIMARY (the non-numeric ones). Every candidate is a valid slug
// (workSlugAt bounds it to model.MaxSlugLen); the bare base already is, coming
// from Slugify. resolveExistingWork walks this same chain to FIND a work, so
// the bound must live here rather than at either call site or the two would
// stop agreeing on where a work sits.
//
// The chain is: the bare title slug, the title plus the first IDENTITY author,
// the title plus EVERY OTHER author the row credits, then numeric suffixes on
// the identity form. Only the first two are slugs a new work may be minted at;
// the rest are probes, marked probeOnly.
//
// Probing every author is what makes the chain independent of the ORDER a
// source lists credits in. The suffix is built from the FIRST author, and two
// editions of one book routinely disagree about who that is: a German Sherlock
// Holmes audio drama credited "Arthur Conan Doyle, S. Pomej" on one release and
// "S. Pomej, Arthur Conan Doyle" on the next produced two works with byte-equal
// author SETS, because each row looked only where its own first author would
// have put it. Ten such pairs were minted by a single wave.
//
// The POSITION probes are the third kind, and pos is what turns them on: a row
// that states a series position looks at the slugs the serial pre-pass mints
// for that position (posSuffixSlugs) as well as at the bare base, so a lone
// volume of a serial finds the suffixed work an earlier BATCH created instead of
// minting a duplicate beside it. They sit after the author probes because the
// bare base is where the overwhelming majority of works are; a row that states
// no claim passes the zero positionClaim and gets no position probe at all.
//
// The other probe is the LEGACY one, and it is the same idea a generation
// earlier: every work created before the credited-contributor exclusion
// (workidentity.go) took its suffix from the first entry of the whole author
// list, so a book whose edition listed its translator first sits at
// "firstborn-julia-schwenk" while today's chain would only look at
// "firstborn-m-j-hastings". The measured cost of not looking is both halves of
// one defect: the create path mints a duplicate of a work it cannot see, and
// --recordings-only silently drops the alternate narration of one.
//
// A probe can only ever FIND a work; nothing is minted at one, so the extra
// locations do not grow, and a probe that hits still has to satisfy every merge
// test (identity, language, series claim) before it is used.
//
// The PRIMARY count is what stops the walk from claiming a free slug too early:
// a work can sit on a probe candidate while the mintable one is free, so all of
// the primaries must be probed before a new work is created at the first free
// one. Past them, the first free slug ends the walk as it always did - the
// chain beyond it can only be empty, because getOrCreateWork would have claimed
// exactly that slug.
func workCandidates(base string, authors workAuthors, pos positionClaim) (cands []workCandidate, primary int) {
	cands = make([]workCandidate, 0, 55)
	cands = append(cands, workCandidate{slug: base})
	mintable := workSlugAt(base, authors.first(), 1)
	cands = append(cands, workCandidate{slug: mintable})
	seen := map[string]bool{base: true, mintable: true}
	// identity first, then the role-credited people the full list adds: a probe
	// order that reads from the most likely location to the least.
	for _, who := range append(append([]string{}, authors.identity...), authors.all...) {
		slug := workSlugAt(base, who, 1)
		if seen[slug] {
			continue
		}
		seen[slug] = true
		cands = append(cands, workCandidate{slug: slug, probeOnly: true})
	}
	for _, slug := range posSuffixSlugs(base, pos) {
		if seen[slug] {
			continue
		}
		seen[slug] = true
		cands = append(cands, workCandidate{slug: slug, probeOnly: true, posSuffixed: true})
	}
	primary = len(cands)
	for i := 2; i <= 50; i++ {
		cands = append(cands, workCandidate{slug: workSlugAt(base, authors.first(), i)})
	}
	return cands, primary
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
