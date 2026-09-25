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

	"github.com/kodestar/audiosilo-meta/internal/titlerule"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

var (
	asinPattern = regexp.MustCompile(`^[A-Z0-9]{10}$`)
	datePattern = regexp.MustCompile(`^\d{4}(-\d{2}(-\d{2})?)?$`)
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
//
// The edition-marker half is titlerule.StripEditionMarkers, the ONE definition of
// what such a marker is (see titlerule/edition.go for why it lives there).
func cleanWorkTitle(title string) string {
	cleaned := strings.TrimSpace(title)
	stripped := titlerule.StripEditionMarkers(cleaned)
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
	// claims is every series position this recording is known to be at - the
	// per-recording half of the same-title serial guard: two volumes of a serial
	// published under one title have compatible runtimes and identical
	// narrators, so nothing else in the merge test can tell them apart. Empty is
	// "unknown", which never blocks - the same posture abridged takes.
	claims []posClaim
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
	// title is the work's stored title. A walk reads it only when it hits a
	// candidate the slug cap CUT (walkWorkChain), where the slug alone no longer
	// says which book it is.
	title string
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
	// runGenresOwned says THIS RUN wrote the work's genre set (created the work,
	// or filled an empty set), which is the permission for a later row of the
	// same run to add the genres it maps - several rows of one book in one run
	// are one account of it. runGenres is that set as written, sorted, kept in
	// memory so the common "nothing new" row is decided without a store read.
	runGenresOwned bool
	runGenres      []string
	// runAttested says a user-library row of THIS RUN attested the work. A later
	// row of the run meeting it is part of the same account, so it stamps its
	// provenance too even when it changes nothing - otherwise which rows a
	// work's sources name would depend on which row came first.
	runAttested bool
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
	// credentialMerges is the same record for the credential fold
	// (credential.go, Summary.CredentialMerges): the same kind of decision -
	// two spellings are one human - made at the other end of the name.
	credentialMerges map[string]string
	// initialsSurvivors is the initials rule's decision for this run
	// (initials.go): for every person slug whose initials group is written more
	// than one way across the catalogue and the batch, the record that group
	// resolves to. Like creditCensus it is a SNAPSHOT taken before planning, and
	// for the same reason - a merge decided against a map that grows during the
	// run depends on row order, so two runs over the same rows in a different
	// order (or one export split into chunks) would mint different ids.
	//
	// It is consulted wherever a credit is resolved, created or not
	// (resolvePerson): the variant slug is never written into p.people, so a
	// credit minted under it has to be redirected here or it would name a record
	// that does not exist.
	initialsSurvivors initialsSurvivors
	// redirects is the catalogue's slug TOMBSTONE table, off the same load as the
	// identity maps, and tombstoneRides every retired slug this run resolved onto
	// its survivor rather than minting there (tombstone.go).
	redirects      model.Redirects
	tombstoneRides map[string]string
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
	// identity is the run's NORMALIZED WORK IDENTITY index over the catalogue as
	// loaded - the create path's duplicate guard (dupidentity.go). nil in every
	// other mode, which is what makes the guard a create-only rule and costs the
	// other two passes nothing (building it cleans every catalogued title once).
	identity *check.WorkIdentity
	// runIdentity and runIdentified are the same index over the works THIS RUN has
	// created or merged into: normalized identity key -> work slugs, and slug -> the
	// title and series name that key was derived from. The disk index cannot hold
	// them, and a wave arrives in batches, so without these two rows of ONE batch
	// carrying two spellings of one title would both create.
	runIdentity   map[string][]string
	runIdentified map[string]runWorkIdentity
	// dupIdentityExamples labels a FEW of the rows the duplicate-identity guard
	// refused, for the run's one aggregated warning (reportDuplicateIdentities);
	// the COUNT is Summary.SkippedDuplicateIdentity. Capped as it fills, like every
	// other example list here.
	dupIdentityExamples []string
	// seriesLookup answers "where does this ASIN sit in its series" for a row
	// whose own source stated no position (Options.SeriesLookup, seriespos.go).
	// nil for every run that was not given one - which is every existing caller,
	// every offline run and the default CLI - and nil is also what makes the rule
	// OFF rather than merely unused.
	seriesLookup SeriesPositionLookup
	// seriesLookupLeft is the lookups this run may still perform, counted down
	// only by a row that actually needs one. -1 is "no cap" (a caller asking for
	// it explicitly); 0 stops the pass.
	seriesLookupLeft int
	// seriesPositionsFilled counts the positions the lookup supplied, and
	// seriesPositionExamples names a few for the run's one aggregated note
	// (reportSeriesPositionLookups). Aggregated for the reason every bulk fact is:
	// the news is "this wave's files carry a series tag and no part number", and a
	// personal library states that 97 times in 123 rows.
	seriesPositionsFilled  int
	seriesPositionExamples []string
	// seriesLookupFailed counts the lookups that did not answer, with a few
	// messages for the same aggregated report. A lookup never fails the run - the
	// service is external and free - so this line is the only thing that tells a
	// run whose lookups were all refused from a run that needed none.
	seriesLookupFailed   int
	seriesLookupFailures []string
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
	// conflictWarnings and rowWarnings are the per-row lines, kept apart from
	// summary.Warnings (the run-level lines) so result() can order the three
	// tiers: conflicts are what recordingContradicts raised, rows everything
	// else bookWarn did.
	conflictWarnings []string
	rowWarnings      []string
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
// names the row it came from. Row lines are held apart from the run-level ones
// until result() puts them after them.
func (p *planner) bookWarn(b sourceBook) func(string, ...any) {
	return p.warnInto(b, &p.rowWarnings)
}

// warnInto is bookWarn over a chosen tier: the ordinary row lines, or the
// conflict lines recordingContradicts raises (see result).
func (p *planner) warnInto(b sourceBook, tier *[]string) func(string, ...any) {
	label := bookLabel(b)
	return func(format string, args ...any) {
		*tier = append(*tier, label+": "+fmt.Sprintf(format, args...))
	}
}

// result is the run's Summary with its warnings in REPORTING order, three tiers
// each in the order it was raised:
//
//   - the run-level lines (the aggregated refusals, the duplicate-identity
//     skips, the catalogue's own collisions, the per-class reports);
//   - the CONFLICT lines, one per row refused for contradicting a recorded
//     runtime or release date - per-row, but the ones the intake verdict's
//     "disagreed ... see the warnings below" note sends a maintainer to;
//   - every other per-row line, in row order.
//
// A run-level or conflict line is one a maintainer acts on, where a run over a
// large library can raise hundreds of ordinary row lines; a reader that shows
// only the head of the list (the intake bot's bounded verdict) must never lose
// them to the detail. Summary.RunLevelWarnings says where the first tier ends.
func (p *planner) result() Summary {
	sum := p.summary
	sum.RunLevelWarnings = len(p.summary.Warnings)
	sum.Warnings = slices.Concat(p.summary.Warnings, p.conflictWarnings, p.rowWarnings)
	return sum
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
	// The run's trust tier, asked here as well as by newPlanner because the AI
	// gate below runs before the planner exists and needs the same answer: a person's own library (or a hand submission) may admit a
	// synthetic narration under the canonical record, the bulk mirror may not.
	// See the planner's userTier field and synthetic.go.
	userTier := model.TierOfSource(sourceType) == model.TierUserLibrary
	// Refused before ANYTHING reads the batch - before the censuses, before the
	// title pre-pass, before planning - so an AI credit cannot reach the person
	// table, the credit census or a title decision. See refuseAIBooks.
	books, aiRefused, synthetic := refuseAIBooks(books, userTier)
	// Opened before anything is planned: a tree still in the file-per-entity
	// layout is refused here, having written nothing and read nothing it could
	// misinterpret.
	store, err := openStore(opts.DataDir, opts.Profile)
	if err != nil {
		return Summary{}, err
	}
	p := newPlanner(store, sourceType, opts)
	if opts.Mode == ModeEnrich || p.userTier {
		p.asinLoc = map[string]RecRef{}
	}
	// The series-position lookup is a USER-LIBRARY CREATE rule (seriespos.go),
	// and it is off unless the caller supplied one. The gap it fills belongs to a
	// personal library export - a file tagged with a series and no part number -
	// and only the create path places a work in a series at all, so a libex run
	// and the two catalogue-bounded modes never reach it even when a lookup is
	// passed.
	if opts.SeriesLookup != nil && p.userTier && opts.Mode == ModeCreate {
		p.seriesLookup = opts.SeriesLookup
		p.seriesLookupLeft = seriesLookupCap(opts.SeriesLookupLimit)
	}
	// Recorded on the summary before planning appends anything, so the AI line
	// is the run's FIRST warning and the summary carries it however run ends.
	// The synthetic-narration note rides along for the same reason: a run that
	// fails later still says what it admitted.
	p.summary.SkippedRows = aiRefused.n
	if line, warned := aiRefused.warning(); warned {
		p.summary.Warnings = append(p.summary.Warnings, line)
	}
	if line, noted := synthetic.note(); noted {
		p.summary.Notes = append(p.summary.Notes, line)
	}
	err = p.run(books, opts)
	return p.result(), err
}

// newPlanner returns an empty planner for a run of sourceType writing through
// store, before anything is loaded: the one place its maps are made, so a test
// that drives the planner directly builds it the way a run does.
func newPlanner(store *pack.Store, sourceType string, opts Options) *planner {
	return &planner{
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
		runIdentity:    map[string][]string{},
		runIdentified:  map[string]runWorkIdentity{},
		sourceType:     sourceType,
		importDate:     opts.ImportDate,
		mode:           opts.Mode,
		conflicts:      opts.Conflicts,
		userTier:       model.TierOfSource(sourceType) == model.TierUserLibrary,
	}
}

// run plans the batch, reports, and (unless a dry run) writes and validates the
// tree. It returns only the error: its one caller turns the planner into the
// Summary through result(), so every way out of a run reports its warnings in
// the same order.
func (p *planner) run(books []sourceBook, opts Options) error {
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
		return p.fatal
	}
	p.finalizeSeries()
	p.reportCreditMerges()
	p.reportUnmappedGenres()
	p.reportUnnamedCredits()
	p.reportUnaddressableSeries()
	p.reportLostSeriesClaims()
	p.reportSeriesPositionLookups()
	p.reportDuplicateIdentities()
	p.reportTombstoneRides()
	if p.fatal != nil {
		return p.fatal
	}

	if opts.DryRun {
		return nil
	}

	if err := p.flush(); err != nil {
		return err
	}
	if res := check.LoadProfile(opts.DataDir, opts.Profile); !res.OK() {
		return fmt.Errorf("post-import validation failed:\n%s", problemLines(res.Problems))
	}
	return nil
}

// ---------------------------------------------------------------------------
// The AI-credit gate
//
// An AI is not a person: crediting one as an AUTHOR mints a person record for a
// language model. libex refuses such a row at its own parse layer (libex.go),
// where the refusal also has to feed libex-select's exclusion reasons - so for a
// libex run this gate is a NO-OP, by construction and not by coincidence:
// firstAICredit has already rejected every row that would trip it.
//
// It lives HERE, in the shared core, because the gate is not a property of one
// source. All three user-library sources are Audible-sourced (pkg/model's trust
// tiers rank openaudible-import, libation-import and audiosilo-books-import
// together) and all three can carry a Virtual Voice title; gating only the
// envelope the site composes let four virtual-voice works into the catalogue.
//
// What it does with such a row DIFFERS BY TIER, which is the maintainer's
// decision recorded in synthetic.go:
//
//   - a USER-LIBRARY row whose NARRATION is synthetic is ADMITTED. The book is
//     in somebody's library, so it is a book this catalogue wants; the credit
//     folds onto the one canonical `virtual-voice` record and the run reports an
//     aggregated NOTE rather than a refusal. The fold itself happens in the
//     credit pipeline (creditWithRolesSided); all this gate does is decline to
//     refuse the row.
//   - an AUTHOR-side AI credit still refuses the row, on every source and in
//     either tier, and so does a generative SYSTEM credited as the narrator.
//   - a libex row still refuses whatever it credits, at the parse layer, exactly
//     as before.
//
// It reads the credit lists through sourceNames - the ONE place the typed-vs-
// comma-joined choice is made, and the exact list sourceCredits will credit. A
// per-source gate over the source's own array shape could not see an AI name
// INSIDE a comma-joined element ("Jane Doe, Virtual Voice" arrives as one
// element, and a projection may hand narrators over as a plain string), which
// the pipeline then splits into two people. Gate and credits now read the same
// names by construction, so they cannot disagree about what a row credits - and
// that is what makes "admitted" and "folded" the same set of credits rather than
// two rules that happen to agree today.
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

// refuseAIBooks drops every book whose credits name an AI the catalogue will not
// admit, and reports both what it dropped and what it let through under the
// synthetic-narration fold. The returned slice is the input itself when nothing
// was refused (the common case, and the only case for libex), so a million-row
// enrichment pays no copy.
//
// foldNarration is the run's trust tier: true for a user's own library or a hand
// submission, false for the bulk mirror. With it false the rule is exactly what
// it always was - any AI credit on either list refuses the row.
func refuseAIBooks(books []sourceBook, foldNarration bool) ([]sourceBook, aiRefusals, syntheticNarrations) {
	var refused aiRefusals
	var folded syntheticNarrations
	kept := books
	// keep / refuse are the two things this loop can do with a row, and they
	// share one piece of bookkeeping: the kept slice is the INPUT until the
	// first refusal, and a copy of everything before it afterwards.
	keep := func(b sourceBook) {
		if refused.n > 0 {
			kept = append(kept, b)
		}
	}
	refuse := func(i int, title, role, name, why string) {
		if refused.n == 0 {
			// The first refusal: keep everything before it, with the capacity
			// capped so keep's appends allocate rather than overwrite books[i:].
			kept = books[:i:i]
		}
		refused.add(title, role, name, why)
	}
	for i, b := range books {
		authors := sourceNames(b.authors, b.str("author"))
		narrators := sourceNames(b.narrators, b.str("narrated_by"))
		title := firstNonEmpty(b.str("title_short"), b.str("title"))

		// The bulk-mirror rule, unchanged: any AI credit on either list refuses.
		if !foldNarration {
			if role, name, why, isAI := firstAICredit(authors, narrators); isAI {
				refuse(i, title, role, name, why)
			} else {
				keep(b)
			}
			continue
		}

		// The user-library rule, in two halves. The AUTHOR side is judged first
		// and on its own terms - firstAICredit with an empty narrator list is the
		// same call the bulk rule makes, restricted to the side the decision did
		// not change - so a book written by a model is refused whoever read it.
		if role, name, why, isAI := firstAICredit(authors, nil); isAI {
			refuse(i, title, role, name, why)
			continue
		}
		// The NARRATOR side: a synthetic voice folds (and is noted), while a
		// generative system in the narrator column is not a narration credit at
		// all and still refuses. The system arm wins where a row states both,
		// because a refusal is a whole-row verdict.
		voice, system := firstSyntheticNarrator(narrators)
		if system != "" {
			refuse(i, title, "narrator", system, "an AI system, not a person")
			continue
		}
		if voice != "" {
			folded.add(title, voice)
		}
		keep(b)
	}
	return kept, refused, folded
}

// firstSyntheticNarrator splits a narrator list into the two AI verdicts the
// user-library rule needs: the first credit that names a synthetic VOICE (which
// folds) and the first that names a generative SYSTEM (which refuses the row).
// Both are returned because a row can state one, the other, or both, and the
// caller decides which verdict wins.
func firstSyntheticNarrator(narrators []string) (voice, system string) {
	for _, n := range narrators {
		switch {
		case namesSyntheticVoice(n):
			if voice == "" {
				voice = n
			}
		case namesAISystemCredit(n):
			if system == "" {
				system = n
			}
		}
	}
	return voice, system
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
	res := check.LoadStore(p.store)
	// The create path's duplicate-identity index, off the load that is already
	// happening: it is the one index the guard probes per row (dupidentity.go), and
	// building it anywhere else would mean a second pass over the catalogue.
	p.identity = runWorkIdentityIndex(res, p.mode)
	cat := res.Catalog
	if cat == nil {
		return
	}
	p.redirects = cat.Redirects
	for _, person := range cat.People {
		p.people[person.ID] = person.Name
	}
	for _, w := range cat.Works {
		ws := &workState{
			slug:    w.ID,
			title:   w.Title,
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
				// One ASIN listed under several regions is ONE identifier of this
				// recording: register it once, so locateASIN never sees the
				// recording collide with itself, and a collision with another
				// recording is reported once rather than once per region.
				if ri.asins[a.ASIN] {
					continue
				}
				ri.asins[a.ASIN] = true
				p.asins[a.ASIN] = true
				p.locateASIN(a.ASIN, w.ID, r.ID)
			}
			for _, isbn := range r.ISBN {
				// The VALUE is what has to be globally unique; whether the
				// record states a region for it is beside the point.
				p.isbns[strings.ToUpper(isbn.ISBN)] = true
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
// A disk claim carries the series SLUG it was read from, which is what a row's
// claim resolves to (seriesKeyOf) - so a row stating a retired spelling meets
// the survivor's positions rather than none at all.
func (p *planner) seedDiskSeriesPositions(series []*model.Series) {
	byWork := map[string][]posClaim{}
	for _, s := range series {
		for _, sw := range s.Works {
			byWork[sw.Work] = append(byWork[sw.Work], posClaim{key: s.ID, pos: rowPosition{source: sw.Position}})
		}
	}
	for slug, claims := range byWork {
		ws, known := p.works[slug]
		if !known {
			continue
		}
		for _, ri := range ws.recs {
			ri.claims = claims
		}
	}
}

// locateASIN records which catalogued recording an ASIN sits on, for the
// identifier match of enrichment and of a user-tier run's attestation. It is a
// no-op unless asinLoc was allocated (any other run needs the p.asins
// membership test alone).
//
// Uniqueness upstream is (region, ASIN), so one ASIN STRING can appear more than
// once. On ONE recording, under several marketplaces, that is ordinary data, and
// the caller (loadExisting) passes each recording's DISTINCT ASINs once, so it
// never reaches here twice. On two DIFFERENT recordings it is a real collision -
// an export row states one bare ASIN and nothing that could pick between them -
// so the FIRST recording (in the catalogue's stable load order) keeps the match
// and the collision is reported, once per colliding recording, rather than
// silently decided by whichever file loaded last.
func (p *planner) locateASIN(asin, workSlug, recSlug string) {
	if p.asinLoc == nil {
		return
	}
	if prev, taken := p.asinLoc[asin]; taken {
		p.summary.Warnings = append(p.summary.Warnings, fmt.Sprintf(
			"catalogue: ASIN %s is recorded on both %s and %s; an export row naming it is matched to the first",
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

	// A series claim the row states with NO position is filled from the lookup
	// here, before anything reads one (seriespos.go). It has to happen at this
	// point rather than at placement: a filled position is a membership, and the
	// claim below, the duplicate-identity guard and the placement at the end of
	// this function must all read the same one. It is also after every admission
	// test above, so a row that is about to be dropped never spends a lookup.
	p.fillSeriesPositions(b, asin, workTitle)

	// The book's series claims (one for OpenAudible, possibly several for
	// Libation). The first that resolves to an already-known series (on disk or
	// created earlier this run) is used to refuse merging into a same-titled work
	// that sits in that series at a different position.
	//
	// Resolved BEFORE the row's people are, which it can be because findSeries only
	// reads: the duplicate-identity guard below needs the claim (it asks
	// resolveWork, as the create path does), and the guard has to run before
	// anything is created or a refused row would leave orphan person records behind.
	claim := p.rowSeriesClaim(b, workTitle, narratorNames)

	// The duplicate-identity guard: a row naming a book the catalogue already holds
	// under a differently-spelled title is dropped and reported rather than minting a
	// second work (dupidentity.go). It skips rather than merging, on purpose - a
	// refused row is re-importable, a wrong merge is not.
	ident := p.rowIdentityOf(b, workTitle)
	if p.refuseDuplicateIdentity(b, ident, workTitle, b.str("title"), posSuffix, lang, authorCredits, claim) {
		p.noteLostSeriesClaims(b)
		return
	}

	// The row's author slugs, split into the list the record stores and the
	// subset work identity is matched on (workidentity.go). Resolving them here
	// is what creates their person records, exactly as creditSlugs did.
	authors := p.rowWorkAuthors(authorCredits, warn)
	narratorSlugs := p.creditSlugs(narratorNames, warn)

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
	walk := p.resolveWork(workTitle, b.str("title"), posSuffix, authors, lang, claim)
	ws := p.getOrCreateWork(walk, authors, lang, facts, warn)
	// The title the row was RESOLVED by - its own, or its full title when the
	// work was found or created on the full title's chain - is the one the
	// recording's serial guard and the series placement read a stated volume
	// from, so all three judge the row's volume off the same title: a row
	// "Towerbound" / "Towerbound, Book 6" that met volume 6 through its full title
	// is volume 6 to the recording guard and to placement too.
	resolvedTitle := walk.title
	// The row's normalized identity now names a work this run knows about, so a LATER
	// row of the same run carrying another spelling of this title meets it (see
	// dupidentity.go's identityMatch). Registered whether the work was created or
	// merged into: either way this row and that work are one book.
	if ws != nil {
		p.rememberIdentity(ident, ws.slug, workTitle)
	}
	recorded := p.addRecording(ws, b, resolvedTitle, asin, lang, narratorSlugs, warn)

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
			// placementPosition is the title-versus-source arbitration
			// (seriespos.go); it returns r.seq unchanged for every row whose title
			// states no volume or states the same one, which is almost all of them.
			p.addToSeries(r.name, ws.slug, p.placementPosition(r, ws.slug, resolvedTitle, warn), warn)
		}
	}
}

// rowSeriesClaim is the row's claim on the FIRST series it states (with a usable
// position) that resolves to a series the planner already knows, or nil. It only
// reads, so a caller may ask before anything is created. workTitle is the title
// the claim's title arm reads a stated volume from, and narratorNames the row's
// narrator credits the production it is corroborated by is resolved from.
// (The recordings-only matcher deliberately asks no work-level claim; see
// resolveExistingWork.)
func (p *planner) rowSeriesClaim(b sourceBook, workTitle string, narratorNames []string) *seriesClaim {
	for _, r := range b.series {
		if !r.seqOK {
			continue
		}
		if ss := p.findSeries(r.name); ss != nil {
			return newSeriesClaim(ss, r, workTitle, p.rowProductionOf(b, narratorNames))
		}
	}
	return nil
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
	notify := func(sameSide creditSeenFunc, foldSynthetic bool) creditCensus {
		return creditCensus{
			anySide:            anySide,
			sameSide:           sameSide,
			foldSyntheticVoice: foldSynthetic,
			onHonorific:        p.noteHonorific,
			onCredential:       p.noteCredential,
		}
	}
	// The AI-narration fold is the NARRATOR side of a USER-LIBRARY run and
	// nothing else (synthetic.go). Setting it here rather than at the call sites
	// is what makes every reader of a narrator name fold identically: the import
	// itself (sourceCredits), the batch credit census and the initials pre-pass
	// all resolve a synthetic credit to the canonical, so a persona spelling
	// never becomes evidence of its own name.
	return notify(seenIn(authorSide), false), notify(seenIn(narratorSide), p.userTier)
}

// noteHonorific records one honorific merge for the run's report. It is a SET
// of (credited spelling -> bare twin) pairs rather than a count: the same
// spelling is cleaned once per row it appears on, and what a maintainer audits
// is which merges happened, not how often each fired.
func (p *planner) noteHonorific(from, to string) {
	p.honorificMerges = noteMerge(p.honorificMerges, from, to)
}

// noteCredential records one academic-credential merge (credential.go), on the
// same terms and for the same reason - it is the same kind of decision made at
// the other end of the name.
func (p *planner) noteCredential(from, to string) {
	p.credentialMerges = noteMerge(p.credentialMerges, from, to)
}

// noteMerge adds one (from -> to) pair to a merge set, creating the set on first
// use.
func noteMerge(set map[string]string, from, to string) map[string]string {
	if set == nil {
		set = map[string]string{}
	}
	set[from] = to
	return set
}

// reportCreditMerges publishes the run's credit merges, sorted, as
// "<credited> -> <bare>" lines.
func (p *planner) reportCreditMerges() {
	p.summary.HonorificMerges = mergeLines(p.honorificMerges)
	p.summary.CredentialMerges = mergeLines(p.credentialMerges)
}

// mergeLines renders one merge set as sorted "<credited> -> <bare>" lines, nil
// when the rule never fired.
func mergeLines(set map[string]string) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for from, to := range set {
		out = append(out, from+" -> "+to)
	}
	sort.Strings(out)
	return out
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
		return SplitRawNames(joined)
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
		// resolvePerson, not personSlug: the person may have been created under
		// another spelling of their initials, which is a record this credit must
		// NAME rather than miss - resolving "A.B. Kovacs" straight through
		// personSlug lands on a slug nothing created, and the role credit was
		// silently dropped.
		r := p.resolvePerson(c.name)
		if r.fellBack {
			p.noteUnnamedCredit(c.name)
			continue
		}
		slug := r.slug
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
	r := p.resolvePerson(name)
	if r.fellBack {
		warn("name %q produced an empty slug; using %q", name, r.slug)
	}
	if r.create {
		return p.createPerson(r.slug, r.name)
	}
	if r.from != r.slug {
		p.noteTombstone(model.RedirectPeople, r.from, r.slug)
	}
	return r.slug
}

// personResolution is resolvePerson's answer: the slug a credit lands on, and
// whether getOrCreatePerson has to create the record there (under name) or found
// it live (reached from the slug from, which differs from slug when a tombstone
// was ridden).
type personResolution struct {
	slug     string
	create   bool
	name     string
	from     string
	fellBack bool
}

// resolvePerson is getOrCreatePerson's DECISION with none of its effects - the
// one resolution the creating path and every read-only resolver of a credit
// (rowWorkAuthorsRO, rowProductionOf, workCredits) go through, so a prediction of
// who a credit names cannot disagree with the record the create path mints.
//
// A read-only answer and the created one agree across a run: resolving a credit
// can only CREATE the very slug this function names for it, and any later credit
// reaching that slug resolves to it either way (live, or named as the one to
// create).
func (p *planner) resolvePerson(name string) personResolution {
	slug, fellBack := personSlug(name)
	// livePerson also follows a retired slug to its survivor (tombstone.go): the
	// person is never re-created at the address a merge took them off.
	from := slug
	live, ok := p.livePerson(from)
	if !ok {
		survivor, merges := p.initialsMerge(name, slug, fellBack)
		if !merges {
			return personResolution{slug: slug, create: true, name: name, fellBack: fellBack}
		}
		from = survivor.slug
		if live, ok = p.livePerson(from); !ok {
			return personResolution{slug: survivor.slug, create: true, name: survivor.name, fellBack: fellBack}
		}
	}
	return personResolution{slug: live, from: from, fellBack: fellBack}
}

// createPerson emits a new person record. The caller has already established
// that slug is free.
// The record carries a kind only when the NAME decides one (PersonKindFor): the
// canonical synthetic-voice record, and nothing else. Every other kind is a
// human classification made through the correct-data form, never an inference.
func (p *planner) createPerson(slug, name string) string {
	p.people[slug] = name
	p.putNewEntry(pack.FamilyPeople, slug, OutPerson{
		ID: slug, Name: name, Kind: PersonKindFor(name), License: licenseCC0, Sources: []OutSource{p.curSource},
	})
	p.summary.NewPeople++
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
	ss *seriesState
	// pos is where the row puts its volume - the source's position and the
	// title's (rowPosition); a work placed at the title's is the same volume only
	// when the title is corroborated (holds).
	pos rowPosition
	// prod is what the row states about its production, which titleCorroborated
	// reads.
	prod *rowProduction
	// name is the series name the ROW states. It usually equals ss.name up to case,
	// but not after a tombstone ride (seriesChain): a retired base joins a survivor
	// whose name is a different spelling, and the position probe must be composed
	// from the spelling the serial pre-pass mints with, which is the row's.
	name string
	// ref is the row's own claim, kept so forTitle can read its position against
	// another title.
	ref seriesRef
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
		return c.holds(ws, existing)
	}
	if wanted, asked := c.ss.claimed[ws.slug]; asked {
		return c.holds(ws, wanted)
	}
	return true
}

// holds reports whether ws recorded at pos is at the row's position: the
// source's outright, the title's only when titleCorroborated.
func (c *seriesClaim) holds(ws *workState, pos string) bool {
	return c.pos.names(pos, func() bool { return titleCorroborated(ws, c.prod) })
}

// newSeriesClaim is row r's claim on the known series ss, with the row's work
// title (which the title arm is read from) and its production (which that arm is
// corroborated by) - the one constructor, so no claim can be built without them.
func newSeriesClaim(ss *seriesState, r seriesRef, title string, prod *rowProduction) *seriesClaim {
	return &seriesClaim{ss: ss, pos: rowPositionOf(r, title), prod: prod, name: r.name, ref: r}
}

// forTitle is the same claim with its title arm read off another title - the
// FULL title, when resolveWork walks it. nil stays nil.
func (c *seriesClaim) forTitle(title string) *seriesClaim {
	if c == nil {
		return nil
	}
	return newSeriesClaim(c.ss, c.ref, title, c.prod)
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
		return c.holds(ws, existing)
	}
	wanted, asked := c.ss.claimed[ws.slug]
	return asked && c.holds(ws, wanted)
}

// position reduces the claim to the (series, position) pair the suffix formulas
// need. The series NAME is the row's spelling, which is what the serial pre-pass
// (serialPositionSuffixes) mints a series-scoped suffix from, so the probe lands
// where the pre-pass mints. Without a tombstone ride it slugifies exactly as the
// catalogued name does (findSeries matched the two case-insensitively); through
// one, the catalogued name is the SURVIVOR's other spelling and would probe a
// slug no pre-pass ever minted.
func (c *seriesClaim) position() positionClaim {
	if c == nil {
		return positionClaim{}
	}
	name := c.name
	if name == "" {
		name = c.ss.name
	}
	// The SOURCE position, which is what the serial pre-pass mints from.
	return positionClaim{series: name, pos: c.pos.source}
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

// getOrCreateWork acts on the decision resolveWork made for a row: it merges the
// row into the existing work the walk found, or creates the work on the walk's
// chain. facts are the creation-only facts (genres, credits); they are stored only
// on the branch that creates a work, which is the only place they can be stored.
// WHICH work a row belongs to, and why, is resolveWork's to say.
func (p *planner) getOrCreateWork(walk workWalk, authors workAuthors, lang string, facts workFacts, warn func(string, ...any)) *workState {
	ws := p.mergeOrCreate(walk, authors, lang, facts, warn)
	// Said after the decision so it names the slug the row actually landed on,
	// not the fallback base the chain composed for the unslugifiable title.
	if ws != nil && walk.untitled != "" {
		warn("title %q produced an empty slug; using %q", walk.untitled, ws.slug)
	}
	return ws
}

func (p *planner) mergeOrCreate(walk workWalk, authors workAuthors, lang string, facts workFacts, warn func(string, ...any)) *workState {
	if ws := walk.ws; ws != nil {
		if walk.via != "" {
			p.noteTombstone(model.RedirectWorks, walk.via, ws.slug)
		}
		// A later row of this run, merging into a work the run created: its
		// credits are not a second source's account of an existing work, they are
		// more of the same import, so the pairs the entry does not carry yet are
		// merged in (a no-op for a work loaded from disk, which is never in
		// runCredits).
		p.mergeCreatedWorkFacts(ws, facts)
		return ws
	}

	// Nothing answered: create on the chain resolveWork chose - the row's own
	// title's, or the full title's when the series claim blocked the short one
	// (then the work is also TITLED by the full title).
	title, base, slug := walk.title, walk.chain.base, walk.free
	if slug == "" {
		// Unreachable in practice: 50 numeric candidates never all collide.
		return nil
	}
	survivor, retired := p.redirects.Survivor(model.RedirectWorks, base)
	_, survivorHeld := p.works[survivor]
	switch {
	case model.IsReservedSlug(base):
		warn("work slug %q is reserved for an API route; using %q for %q", base, slug, title)
	case slug != base && retired && survivorHeld:
		warn("work slug %q was retired by a merge onto %q, which this row does not match; using %q for %q",
			base, survivor, slug, title)
	case slug != base && retired:
		warn("work slug %q was retired by a merge onto %q, which the catalogue does not hold; using %q for %q",
			base, survivor, slug, title)
	case slug != base:
		warn("work slug %q taken by a different book; using %q for %q", base, slug, title)
	}
	ws := &workState{
		slug: slug, title: title, authors: authors.set(), all: authors.allSet(), lang: lang,
		posSuffixed: walk.chain.suffixed, recs: map[string]*recInfo{},
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
	ws.runGenresOwned = true
	ws.runGenres = p.genres.mapGenres(facts.genres, p.unmappedGenres)
	p.putNewEntry(pack.FamilyWorks, slug, outWork{
		ID: slug, Title: title, Authors: authors.all, Language: lang,
		Credits: credits,
		Genres:  ws.runGenres,
		AddedAt: p.importDate,
		License: licenseCC0, Sources: []OutSource{p.curSource},
	})
	p.summary.NewWorks++
	p.summary.Credits += len(credits)
	p.recordRunCredits(slug, credits)
	return ws
}

// mergeCreatedWorkFacts is the create path's half of the in-run merge: a later
// row that resolved onto a work THIS RUN created contributes the (person, role)
// pairs and the genres the work does not carry yet. Nothing is removed.
//
// Both questions are answered from what the run already holds in memory
// (runCredits, workState.runGenres), so it is a no-op - and costs no store read -
// for a work loaded from disk and for a row that adds nothing, which is the
// overwhelming majority of rows. A row that does add something is written with
// one read and one put, and stamps its provenance on the work, because the work
// now records a fact that came from it. (A store read that fails is fatal to the
// whole run, so the tracked state having moved ahead of the record is never
// observable.)
func (p *planner) mergeCreatedWorkFacts(ws *workState, facts workFacts) {
	var credits []model.Credit
	added := 0
	if _, touched := p.runCredits[ws.slug]; touched {
		credits, added = p.addRunCredits(ws.slug, p.workCredits(facts.credits))
	}
	var genres []string
	if ws.runGenresOwned && len(facts.genres) > 0 {
		if g := UnionGenres(ws.runGenres, p.genres.mapGenres(facts.genres, p.unmappedGenres)); len(g) > len(ws.runGenres) {
			genres = g
		}
	}
	if added == 0 && genres == nil {
		return
	}
	raw := p.workEntryRaw(ws.slug)
	if raw == nil {
		return
	}
	if added > 0 {
		raw["credits"] = credits
		p.summary.Credits += added
	}
	if genres != nil {
		ws.runGenres = genres
		raw["genres"] = genres
	}
	p.stampSource(raw)
	p.putWorkEntry(ws.slug, raw)
}

// unionRawGenres unions add into raw's genre set (UnionGenres, so it stays
// sorted and duplicate-free, and nothing is ever removed), and returns the new
// set, or nil when nothing was added.
func unionRawGenres(raw map[string]any, add []string) []string {
	var cur []string
	arr, _ := raw["genres"].([]any)
	for _, g := range arr {
		if s, ok := g.(string); ok {
			cur = append(cur, s)
		}
	}
	out := UnionGenres(cur, add)
	if len(out) == len(cur) {
		return nil
	}
	raw["genres"] = out
	return out
}

// workWalk is one walk of a title's candidate chain: the existing work it would
// merge into (ws, found at candidate hit, through the retired candidate via when
// that is non-empty), the first slug a new work could be created at (free, "" when
// none), and whether a same-author candidate was ruled out by the row's series
// claim (blocked). untitled is the row's title when it slugged to nothing, for the
// warning only the creating caller can give.
type workWalk struct {
	title    string
	chain    workChain
	hit      workCandidate
	ws       *workState
	via      string
	free     string
	blocked  bool
	untitled string
}

// walkWorkChain walks a title's candidate chain and grades every candidate
// against the row. It only READS p.works: deciding and creating are the caller's.
//
// The walk grades every candidate rather than taking the first that answers:
// two candidates can both reduce to the row's identity set (the-iliad and
// the-iliad-robert-fitzgerald both reduce to Homer) and only the whole credit
// list says which of the two the row is.
//
// Three tests can rule a candidate out even when its authors answer:
//
//   - the LANGUAGE test. A work is language-scoped, so a translation may not
//     merge into its original however identical their credits are
//     (langCompatible).
//   - the SERIES CLAIM test. A same-author work the row's series claim places at
//     a DIFFERENT position is a different volume sharing the title
//     (seriesClaim.compatible); the walk reports it as blocked.
//   - the SUFFIXED-SLUG test. A candidate carrying a serial-position tail - the
//     row's own suffixed base, or a position probe - is a merge target only when
//     something says its "book-<position>" tail means what it says: THIS run
//     created the work on the suffixed path (workState.posSuffixed), or the row's
//     series claim PLACES that work at exactly the position it claims
//     (seriesClaim.places). A work whose slug looks like "<something>-book-3"
//     because its TITLE ends that way is not volume 3 of the row's serial, and
//     neither test can reach it.
//
// A CUT candidate (workCandidate.shortened) is a hit only when the work there
// carries the walked title WHOLE (titlerule.CompareKeyWhole). The slug cap cuts a
// long retail title's TAIL, which is exactly where "..., Book 3)" and "..., Book
// 4)" differ, so two volumes by one author meet on one cut slug; a cut hit whose
// titles differ is some other book, and the walk steps past it like any occupied
// slug - a lower-ranked candidate can still answer, and a row nothing answers
// creates. On every walk: the create path's, the blocked retry, the full-title
// merge and the recordings-only matcher.
//
// A RETIRED candidate is judged as its survivor (workAt, tombstone.go): the row
// merges into it on exactly the rules a live record there would get, and
// otherwise the candidate is occupied and the walk steps past it - nothing is
// ever created at a tombstoned slug.
func (p *planner) walkWorkChain(title string, chain workChain, authors workAuthors, lang string, claim *seriesClaim) workWalk {
	w := workWalk{title: title, chain: chain}
	bestKind := matchNone
	wholeKey := "" // the walked title's CompareKeyWhole, computed on the first cut hit
	for i := 0; ; i++ {
		cand, ok := chain.at(i)
		if !ok {
			break
		}
		ws, via := p.workAt(cand.slug)
		if ws == nil {
			if via == "" { // free: neither live nor retired
				if w.free == "" && !cand.probeOnly {
					w.free = cand.slug
				}
				if w.free != "" && i >= len(chain.primary) {
					break
				}
			}
			continue
		}
		if cand.shortened {
			if wholeKey == "" {
				wholeKey = titlerule.CompareKeyWhole(title)
			}
			if titlerule.CompareKeyWhole(ws.title) != wholeKey {
				continue
			}
		}
		kind := matchWork(ws, authors)
		if kind == matchNone || !langCompatible(ws.lang, lang) {
			continue
		}
		if (chain.suffixed || cand.posSuffixed) && !ws.posSuffixed && !claim.places(ws) {
			continue
		}
		if !claim.compatible(ws) {
			w.blocked = true
			continue
		}
		if kind > bestKind {
			bestKind, w.hit, w.ws, w.via = kind, cand, ws, via
		}
		if bestKind == matchExact {
			break
		}
	}
	return w
}

// resolveWork is THE answer to "which existing work does this row belong to, and
// if none, on which chain is its work created" - read-only, so the create path and
// the duplicate-identity guard ask the one question the same way.
//
// That is the guard/create agreement, and it rests on two things: both call this
// function (the guard to learn whether the row would create a work, addBook to
// act), and both hand it the row's authors resolved the same way (resolvePerson,
// which getOrCreatePerson acts on and rowWorkAuthorsRO reads). addBook does not
// reuse the guard's walk: it resolves again with the author set the create path
// actually minted, so an in-row difference between the read-only and the created
// resolution can never make the create path act on an answer it did not reach.
//
// The row's resolved title is walked first; that is where the overwhelming
// majority of works are. When it holds no match, the FULL title's chain is walked
// too, in one of two roles:
//
//   - as the CREATE chain, when the series claim BLOCKED a same-author candidate
//     on the short chain (a different volume that merely shares the short
//     title): the long-standing full-title retry.
//   - otherwise as a MERGE TARGET only (mergeOnlyWalk): a user-library export
//     titles a work by its short title ("Nightfall") where the bulk mirror
//     created the same production under its full retailer title ("Nightfall - A
//     Fantasy Adventure (Dragon Centurion, Book 4)"), and the short chain cannot
//     see that work. A miss leaves the row on its own title's chain; nothing is
//     ever created at a full-title slug by this role.
//
// Neither fires under a posSuffix, and that is structural rather than an
// omission: a row only carries a suffix when resolveWorkTitles left its resolved
// title equal to its full title (a suffix is minted precisely for the rows the
// full-title fallback could not separate), so the precondition - a full title
// that differs - can never hold. It is skipped explicitly so that reading the
// code says so.
func (p *planner) resolveWork(title, fullTitle, posSuffix string, authors workAuthors, lang string, claim *seriesClaim) workWalk {
	ts := slugOfTitle(title)
	short := p.walkWorkChain(title, newWorkChain(ts, posSuffix, authors, claim.position()), authors, lang, claim)
	if ts.fellBack {
		// Only the short walk can be on the "untitled" fallback, so only it
		// carries the warning; a full-title walk is on a real slug.
		short.untitled = title
	}
	if short.ws != nil || posSuffix != "" || fullTitle == title {
		return short
	}
	fs := slugOfTitle(fullTitle)
	if fs.fellBack || fs.slug == short.chain.base {
		return short
	}
	// A walk of the FULL title is judged by a claim read off the full title, so
	// the title arm (rowPositionOf) sees the volume the full title states: a row
	// titled "Towerbound" / "Towerbound, Book 6" at the retailer's position 8 is
	// volume 6, which only the full title says.
	fullClaim := claim.forTitle(fullTitle)
	if short.blocked {
		return p.walkWorkChain(fullTitle, newWorkChain(fs, "", authors, fullClaim.position()), authors, lang, fullClaim)
	}
	if long, ok := p.mergeOnlyWalk(fullTitle, fs, authors, lang, fullClaim); ok {
		return long
	}
	return short
}

// mergeOnlyWalk walks a title's chain as a place to LOOK, never to create: the
// full-title merge (resolveWork) and every title candidate of the recordings-only
// matcher (resolveExistingWork) go through it. It reports a walk only when the
// walk found a work.
//
// The hit must clear every test walkWorkChain applies. Beyond those it is
// narrower than a create walk in two ways:
//
//   - NO POSITION PROBES. A place to look is looked for under its own title;
//     probing the serial pre-pass's "<title>-book-<n>" slugs on top of a title
//     that already spells its volume addresses "...-book-4-book-4" shapes nothing
//     ever created.
//   - ONLY WHEN THE BARE SLUG IS OCCUPIED (bareSlugOccupied). A work sits on a
//     suffixed candidate only because the bare one was taken when it was
//     created, so a free bare slug means the chain holds nothing - and the gate
//     keeps the walk off the overwhelming majority of titles, which name nothing
//     we hold.
//
// A cut slug is judged as on every walk: a hit there counts only when the work
// carries the walked title whole (walkWorkChain).
//
// It notes no tombstone ride: it is asked read-only (the guard asks resolveWork
// without importing the row), so the ride is recorded by whoever acts on the walk.
func (p *planner) mergeOnlyWalk(title string, ts titleSlug, authors workAuthors, lang string, claim *seriesClaim) (workWalk, bool) {
	if ts.fellBack || !p.bareSlugOccupied(ts.slug) {
		return workWalk{}, false
	}
	w := p.walkWorkChain(title, newWorkChain(ts, "", authors, positionClaim{}), authors, lang, claim)
	return w, w.ws != nil
}

// bareSlugOccupied reports whether a title's bare slug is taken - by a live work,
// by a tombstone, or by being an API route literal nothing may be created at (the
// work then sits on the author-suffixed candidate while the bare slug stays
// free). It is mergeOnlyWalk's gate and the recordings-only matcher's cheap
// pre-check before it resolves a row's credits.
func (p *planner) bareSlugOccupied(slug string) bool {
	ws, via := p.workAt(slug)
	return ws != nil || via != "" || model.IsReservedSlug(slug)
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
	if ans := p.seriesChainFor(base, name); ans.found {
		return p.series[ans.slug]
	}
	return nil
}

// seriesChainFor walks name's chain over the planner's series (tombstone.go's
// seriesChain, the walker every twin shares).
func (p *planner) seriesChainFor(base, name string) seriesChainAnswer {
	return seriesChain(base, name, p.redirects, func(slug string) (string, bool) {
		ss, exists := p.series[slug]
		if !exists {
			return "", false
		}
		return ss.name, true
	})
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
//
// title is the row's work title, the one placement arbitrates a stated volume
// against (rowPositionOf), so the serial guard reads the row's position as
// placement does.
func (p *planner) addRecording(ws *workState, b sourceBook, title, asin, lang string, narratorSlugs []string, warn func(string, ...any)) (asinRecorded bool) {
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
	claims := rowSeriesClaims(b, title)
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
		rowKeyed := p.keyClaims(claims)
		prod := resolvedRowProduction(b, narrSet)
		for _, m := range matches {
			// A sibling recording whose row claimed a DIFFERENT position in a
			// series this row also claims is a different volume, however alike the
			// two productions look. Checked before the runtime and abridged guards
			// because it is the only one that can tell two volumes of a serial
			// apart.
			if series, incumbent, want, conflict := p.seriesPosConflict(m.info, ws, rowKeyed, prod); conflict {
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
				// The claim happens INSIDE the merge, which is where the target
				// record's own isbn[] can be read - see claimISBNsFor.
				p.mergeRecordingASIN(m.info, ws.slug, m.slug, region, asin, b.isbns, warn)
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
		abridged: b.abridged, claims: claims,
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

// claimISBNsFor is claimISBNs against an EXISTING record: it drops the ISBNs raw
// already carries before claiming the rest.
//
// The filter is what makes the claim mean what its warning says. p.isbns is
// seeded from the whole catalogue, so a record's own ISBN is already in the set
// - hand it straight to claimISBNs and the run reports "already recorded on
// another recording" about the very record it is writing to. Both paths that
// add an ISBN to a record already on disk (the enrichment fill and the ASIN
// merge) go through here for that reason. Reading the record's isbn[] goes
// through model.ISBNRefOf - the one reader of the on-disk spellings - so a
// region-scoped entry counts as present too.
func (p *planner) claimISBNsFor(raw map[string]any, isbns []string, warn func(string, ...any)) []string {
	if len(isbns) == 0 {
		return nil
	}
	existing, _ := raw["isbn"].([]any)
	have := make(map[string]bool, len(existing))
	for _, v := range existing {
		if entry, ok := model.ISBNRefOf(v); ok {
			have[strings.ToUpper(entry.ISBN)] = true
		}
	}
	var candidates []string
	for _, isbn := range isbns {
		if !have[strings.ToUpper(isbn)] {
			candidates = append(candidates, isbn)
		}
	}
	return p.claimISBNs(candidates, warn)
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

// posClaim is one series position a recording sits at, for the serial guard. A
// disk membership carries its series slug as key; a row's claim carries the
// series name it stated and no key until keyClaims resolves it.
type posClaim struct {
	name, key string
	pos       rowPosition
}

// keyOf is the series a claim names: its key when resolved, else the series its
// name resolves to. A row's claim is resolved when compared rather than when its
// recording is made, because the row's own series may not exist yet at that
// point (addRecording runs before addToSeries).
func (c posClaim) keyOf(p *planner) string {
	if c.key != "" {
		return c.key
	}
	return p.seriesKeyOf(c.name)
}

// rowSeriesClaims is a row's valid series claims, in the order it states them,
// each at the positions rowPositionOf reads against title.
func rowSeriesClaims(b sourceBook, title string) []posClaim {
	var out []posClaim
	for _, r := range b.series {
		if r.seqOK {
			out = append(out, posClaim{name: r.name, pos: rowPositionOf(r, title)})
		}
	}
	return out
}

// keyClaims resolves each claim's key, so one row's keys are computed once
// however many sibling recordings it is compared against.
func (p *planner) keyClaims(claims []posClaim) []posClaim {
	out := make([]posClaim, len(claims))
	for i, c := range claims {
		out[i] = posClaim{name: c.name, key: c.keyOf(p), pos: c.pos}
	}
	return out
}

// seriesKeyOf is the slug of the series a name resolves to (findSeries, so a
// retired spelling is its survivor), else the lowercased name in a form no slug
// can take - which keeps two claims on a series nothing has minted comparable.
func (p *planner) seriesKeyOf(name string) string {
	if ss := p.findSeries(name); ss != nil {
		return ss.slug
	}
	return "name:" + strings.ToLower(name)
}

// seriesPosConflict reports whether a row (its claims keyed by keyClaims) and an
// existing recording state DIFFERENT positions in the same series, which makes
// them different volumes however compatible their runtimes are. It names the
// series and both positions so the refusal to merge can say what it saw. The
// recording's first claim on a series is the one compared, and a recording with
// no known position never conflicts: the guard fires on evidence, never on
// absence. A match through a title arm counts only when titleCorroborated for
// the recording's work ws and the row's production - the same test
// seriesClaim's is - or when ws itself sits at the row's SOURCE position.
func (p *planner) seriesPosConflict(ri *recInfo, ws *workState, row []posClaim, prod *rowProduction) (series, incumbent, want string, conflict bool) {
	if len(row) == 0 || len(ri.claims) == 0 {
		return "", "", "", false
	}
	for _, r := range row {
		for _, c := range ri.claims {
			if c.keyOf(p) != r.key {
				continue
			}
			corroborated := func() bool {
				ss := p.series[r.key]
				// The work SITTING at the row's source position is the recorded fact
				// a disk claim carries (seedDiskSeriesPositions), so it agrees here
				// exactly as it would across two runs - a recording created this run
				// still carries its row's raw claim, whose title arm is what placed
				// the work there.
				if ss != nil && r.pos.source != "" {
					if at, in := ss.members[ws.slug]; in && SameSlot(at, r.pos.source) {
						return true
					}
				}
				return titleCorroborated(ws, prod)
			}
			if !c.pos.agrees(r.pos, corroborated) {
				return r.name, c.pos.source, r.pos.source, true
			}
			break
		}
	}
	return "", "", "", false
}

// mergeRecordingASIN appends {region, asin} (and whichever of the row's ISBNs
// this entry does not already carry) to an existing recording and re-queues it,
// preserving every other field byte-for-byte. The recording is read from inside
// its work's composite entry, queued-write-first (so a recording written earlier
// in the same run is the one edited), and the whole entry goes back. It never
// stamps added_at: the recording being merged into entered the database earlier.
// The caller has already checked that asin is not present on ri.
//
// The ISBNs arrive RAW rather than pre-claimed, because the claim has to see the
// target's own isbn[] to be honest - a row restating an ISBN the target already
// holds would otherwise be reported as a collision with "another recording"
// (claimISBNsFor). Nothing is claimed on the bail path below either, which is
// the right side to err on: an entry that could not be read was not written.
func (p *planner) mergeRecordingASIN(ri *recInfo, workSlug, recSlug, region, asin string, isbns []string, warn func(string, ...any)) {
	if p.fatal != nil {
		return
	}
	entry, raw := p.recordingRaw(workSlug, recSlug)
	if raw == nil {
		return
	}
	arr, _ := raw["asin"].([]any)
	raw["asin"] = append(arr, map[string]any{"region": region, "asin": asin})
	appendISBNs(raw, p.claimISBNsFor(raw, isbns, warn))
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
// when new. Numeric suffixes resolve a collision with a differently-named series,
// and a name whose base slug a merge RETIRED joins the series it was merged into
// (seriesChain, tombstone.go) - it never re-creates the retired duplicate.
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
	// A tombstoned base joins the series it was merged into, and any other retired
	// candidate is stepped past as occupied (tombstone.go).
	ans := p.seriesChainFor(base, name)
	if ans.found {
		if ans.via != "" {
			p.noteTombstone(model.RedirectSeries, ans.via, ans.slug)
		}
		return p.series[ans.slug]
	}
	slug := ans.slug
	switch {
	case model.IsReservedSlug(base):
		warn("series slug %q is reserved for an API route; using %q for %q", base, slug, name)
	case slug != base:
		warn("series slug %q taken by a different series; using %q for %q", base, slug, name)
	}
	ss := &seriesState{
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
// that is a place to LOOK but never a place to create: see primaryWorkCandidates.
// posSuffixed marks a slug that carries a serial-position tail, which is what
// makes it subject to walkWorkChain's suffixed-slug test. shortened marks a slug
// whose TITLE part was cut to fit MaxSlugLen - the chain's base was cut, or
// composing this candidate cut it - so two long titles agreeing up to the cut
// meet on it.
type workCandidate struct {
	slug        string
	probeOnly   bool
	posSuffixed bool
	shortened   bool
}

// titleSlug is a title's slug as a work chain is built on it: the slug, whether
// the MaxSlugLen cap cut it, and whether the title slugged to nothing (the slug is
// then the "untitled" fallback, which the creating caller warns about).
type titleSlug struct {
	slug     string
	cut      bool
	fellBack bool
}

// slugOfTitle slugs a work title once, for every chain built on it.
func slugOfTitle(title string) titleSlug {
	slug, cut := model.SlugifyCut(title)
	if slug == "" {
		return titleSlug{slug: "untitled", fellBack: true}
	}
	return titleSlug{slug: slug, cut: cut}
}

// workChain is the slug-candidate chain a row's work is looked up and created on:
// the PRIMARY candidates (primaryWorkCandidates), built eagerly, and the numbered
// collision candidates after them, composed on demand by at because a walk almost
// never reaches them.
//
// It is ONE type because every walk of a work chain goes through it - the create
// path, the duplicate-identity guard (through resolveWork) and the recordings-only
// matcher (through mergeOnlyWalk) - so "the same chain in the same order" is
// structural rather than a comment at three sites.
type workChain struct {
	base     string // the title's slug, with the serial-position tail when the row carries one
	cut      bool   // base was cut to fit MaxSlugLen
	suffixed bool   // base carries the serial-position tail
	first    string // the first identity author, the numbered candidates' credit
	primary  []workCandidate
}

// maxWorkCandidate is the last numbered collision suffix a chain composes.
const maxWorkCandidate = 50

// newWorkChain composes a chain over ts. posSuffix is the serial-disambiguation
// tail (workidentity.go): when the batch pre-pass found that this row shares its
// title with a sibling volume, the tail is appended to the title base BEFORE the
// collision chain, so each volume gets its own slug instead of the chain merging
// them. It is empty for almost every row.
//
// probe is the series position whose serial-suffixed slugs a row may LOOK at
// (the zero positionClaim for none), so a lone volume finds the work an earlier
// batch created there - but never for a row that is itself suffixed: its base
// already carries the tail, and probing a second one would address
// "<title>-book-1-book-1".
func newWorkChain(ts titleSlug, posSuffix string, authors workAuthors, probe positionClaim) workChain {
	c := workChain{base: ts.slug, cut: ts.cut, first: authors.first()}
	if posSuffix != "" {
		var cut bool
		c.base, cut = boundedSlugTail(c.base, "-"+posSuffix)
		c.cut = c.cut || cut
		c.suffixed = true
		probe = positionClaim{}
	}
	c.primary = primaryWorkCandidates(c.base, c.cut, authors, probe)
	return c
}

// at is the chain's i'th candidate: a primary one, then the numbered collision
// candidates "<base>-<first author>-2" through "-50". ok is false past the end.
func (c workChain) at(i int) (workCandidate, bool) {
	if i < len(c.primary) {
		return c.primary[i], true
	}
	n := i - len(c.primary) + 2
	if n > maxWorkCandidate {
		return workCandidate{}, false
	}
	slug, cut := workSlugAt(c.base, c.first, n)
	return workCandidate{slug: slug, shortened: c.cut || cut}, true
}

// primaryWorkCandidates yields the chain's PRIMARY candidates, the ones every
// walk probes before it may create at the first free slug. Every candidate is a
// valid slug (workSlugAt bounds it to model.MaxSlugLen); the bare base already
// is, coming from Slugify. baseCut marks every candidate shortened when the base
// itself was cut.
//
// The chain is: the bare title slug, the title plus the first IDENTITY author,
// the title plus EVERY OTHER author the row credits, then the position probes.
// Only the first two are slugs a new work may be created at; the rest are probes,
// marked probeOnly. The numbered candidates follow (workChain.at).
//
// Probing every author is what makes the chain independent of the ORDER a
// source lists credits in. The suffix is built from the FIRST author, and two
// editions of one book routinely disagree about who that is: a German Sherlock
// Holmes audio drama credited "Arthur Conan Doyle, S. Pomej" on one release and
// "S. Pomej, Arthur Conan Doyle" on the next produced two works with byte-equal
// author SETS, because each row looked only where its own first author would
// have put it.
//
// The POSITION probes are the third kind, and pos is what turns them on: a row
// that states a series position looks at the slugs the serial pre-pass mints
// for that position (posSuffixSlugs) as well as at the bare base, so a lone
// volume of a serial finds the suffixed work an earlier BATCH created instead of
// creating a duplicate beside it. They sit after the author probes because the
// bare base is where the overwhelming majority of works are; a row that states
// no claim passes the zero positionClaim and gets no position probe at all.
//
// The other probe is the LEGACY one, and it is the same idea a generation
// earlier: every work created before the credited-contributor exclusion
// (workidentity.go) took its suffix from the first entry of the whole author
// list, so a book whose edition listed its translator first sits at
// "firstborn-julia-schwenk" while today's chain would only look at
// "firstborn-m-j-hastings". Not looking costs both halves of one defect: the
// create path creates a duplicate of a work it cannot see, and --recordings-only
// silently drops the alternate narration of one.
//
// A probe can only ever FIND a work; nothing is created at one, so the extra
// locations do not grow, and a probe that hits still has to satisfy every merge
// test (identity, language, series claim) before it is used.
//
// The PRIMARY count is what stops the walk from claiming a free slug too early:
// a work can sit on a probe candidate while the creatable one is free, so all of
// the primaries must be probed before a new work is created at the first free
// one. Past them, the first free slug ends the walk - the chain beyond it can
// only be empty, because the create path would have claimed exactly that slug.
func primaryWorkCandidates(base string, baseCut bool, authors workAuthors, pos positionClaim) []workCandidate {
	cands := make([]workCandidate, 0, 4)
	// A base that IS an API route literal ("search", "latest") is a place to
	// LOOK and never a place to create: a work stored there is unreachable
	// through /api/v1/works/{id} (pkg/check's checkReservedSlug refuses it), so
	// the row steps onto the author-suffixed candidate exactly as it would for a
	// slug another book had taken. Probing it still matters - the tree could hold
	// a record created before the rule, and merging into it beats forking beside
	// it.
	cands = append(cands, workCandidate{slug: base, probeOnly: model.IsReservedSlug(base), shortened: baseCut})
	mintable, cut := workSlugAt(base, authors.first(), 1)
	cands = append(cands, workCandidate{slug: mintable, shortened: baseCut || cut})
	seen := map[string]bool{base: true, mintable: true}
	// identity first, then the role-credited people the full list adds: a probe
	// order that reads from the most likely location to the least.
	for _, who := range append(append([]string{}, authors.identity...), authors.all...) {
		slug, cut := workSlugAt(base, who, 1)
		if seen[slug] {
			continue
		}
		seen[slug] = true
		cands = append(cands, workCandidate{slug: slug, probeOnly: true, shortened: baseCut || cut})
	}
	for _, probe := range posSuffixSlugs(base, pos) {
		if seen[probe.slug] {
			continue
		}
		seen[probe.slug] = true
		cands = append(cands, workCandidate{slug: probe.slug, probeOnly: true, posSuffixed: true, shortened: baseCut || probe.cut})
	}
	return cands
}

// workSlugAt builds the i'th disambiguated work-slug candidate (i >= 1):
// "<base>-<firstAuthor>" for i == 1, then "-2", "-3", ... appended for the
// later ones, bounded to model.MaxSlugLen by BoundedSlugTail - so the TITLE is
// what gets shortened, and cut says whether it was. The author credit tells two
// books sharing a title apart and the numeric suffix tells the candidates apart,
// so neither may be cut away.
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
// of them equal a later candidate. A walk absorbs that as one wasted probe of a
// slug it has already tested.
func workSlugAt(base, firstAuthor string, i int) (slug string, cut bool) {
	numeric := ""
	if i > 1 {
		numeric = fmt.Sprintf("-%d", i)
	}
	credit := "-" + firstAuthor
	if slug, ok := wordBoundedSlugTail(base, credit+numeric); ok {
		return slug, len(base)+len(credit)+len(numeric) > model.MaxSlugLen
	}
	// TrimRight so a credit cut mid-hyphen cannot meet the numeric suffix as a
	// doubled hyphen; firstAuthor is a valid slug, so a leading run survives.
	if half := (model.MaxSlugLen - len(numeric)) / 2; len(credit) > half {
		credit = strings.TrimRight(credit[:half], "-")
	}
	return boundedSlugTail(base, credit+numeric)
}

// AuthorSuffixedWorkSlug is the FIRST disambiguated work-slug candidate -
// "<base>-<firstAuthor>", bounded exactly as the bulk chain bounds it - exported
// for internal/issueform, which steps a reserved title slug off the route
// literal the same way this package does. One formula, so the two composers can
// never compose two different answers to "where does the work titled Search go".
func AuthorSuffixedWorkSlug(base, firstAuthor string) string {
	slug, _ := workSlugAt(base, firstAuthor, 1)
	return slug
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
