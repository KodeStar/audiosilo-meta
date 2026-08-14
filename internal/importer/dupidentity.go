package importer

import (
	"fmt"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/titlerule"
	"github.com/kodestar/audiosilo-meta/pkg/check"
)

// dupidentity.go is the CREATE path's duplicate-identity guard: a row whose book
// the catalogue already holds under a differently-spelled title does not mint a
// second work.
//
// WHAT IT CLOSES. Work identity here is (title slug, author set), and that pair
// cannot see through a retailer's decoration: "Hammered" and "Hammered: The Iron
// Druid Chronicles, Book 3" slug differently, so the second row minted a sibling
// work of a book the tree already had. A full audit of the seeded tree found 4,596
// near-duplicate work clusters, most of them exactly this - the bulk waves' own
// output. The rules that produce a work's slug cannot be widened to fix it: a
// work's slug is where the record LIVES, and cleaning titles harder would move
// records and change ids for every future import. So the guard sits beside the
// identity rule rather than inside it, and answers a different question - not
// "where does this row's work live" but "does the catalogue already hold this
// book".
//
// IT SKIPS, IT NEVER MERGES. A match here is coarser evidence than the slug chain
// (that is the whole point), and internal/audit measured five separate vetoes a
// MERGE has to clear - a position conflict inside a series, disjoint series
// memberships, a collection on one side only, an impossible runtime ratio, a
// decorated survivor - every one of them a question a title cannot answer. A wrong
// merge is unrecoverable: recordings, ASINs and provenance land on the wrong book
// and no later run can tell. A skipped row is recoverable to the point of being
// cheap - the row is still in the export, and re-importing it after a maintainer
// resolves the pair costs one command. So the guard drops the row, counts it
// (Summary.SkippedDuplicateIdentity), names a few in one aggregated warning and -
// when the run was given a worklist - writes a triageable NDJSON row naming both
// titles.
//
// ROUTING IT TO THE ALTERNATE-NARRATION PASS is the obvious next step and is
// deliberately NOT taken here. recordings.go could take such a row as a new
// recording under the matched work, and for a true duplicate that is the right
// outcome - but attaching a recording asserts "this IS that book" on exactly the
// evidence the audit found insufficient to merge on, and it writes data rather than
// declining to. The worklist is what makes the follow-up possible: a wave's refused
// rows are a measurable population to decide about, not a guess. Recorded as a
// follow-up in CLAUDE.md.
//
// COST. The probe is a map lookup per row against ONE index built per run
// (check.NewWorkIdentity, built in loadExisting only for a create run), which is
// the derivedCache lesson from the audit: the key costs a title clean, so cleaning
// 280k titles once is affordable and cleaning them per row is not.

// duplicateIdentityMatch is the catalogued work a row was refused in favour of.
type duplicateIdentityMatch struct {
	work  string // the catalogued work's slug
	title string // its title, so the warning shows both spellings
	key   string // the normalized identity both titles reduced to
}

// refuseDuplicateIdentity reports whether this row must be dropped because the
// catalogue (or an earlier row of this run) already holds its book under another
// spelling of the title. It is called from addBook BEFORE any person record is
// resolved, so a refused row leaves nothing behind - a guard that ran later would
// mint orphan people, which is a defect of its own (pkg/check's orphan-person
// advisory).
//
// It is a no-op unless the run built the index, which is create mode only:
// enrichment matches by ASIN and never creates, and the recordings-only pass
// resolves a work it must already hold.
//
// It applies to EVERY create-path source, not only the bulk mirror: a personal
// library export can carry the same decorated listing, and the tree's integrity is
// not a property of who is importing. The submitter is not left in the dark - the
// intake bot reports the refusals and routes the submission to a maintainer rather
// than closing it as a duplicate (internal/issueform's import composer).
func (p *planner) refuseDuplicateIdentity(b sourceBook, workTitle, fullTitle, posSuffix, lang string, credits []credit, claim *seriesClaim) bool {
	if p.identity == nil || workTitle == "" {
		return false
	}
	// A row the SERIAL PRE-PASS suffixed is a distinct volume the batch has already
	// ruled on: its siblings carry the same title by construction (that is what the
	// pre-pass fires on), so their identities collide by construction too. Six rows
	// titled "Bravelands" at positions 1-6 are six books, and refusing five of them
	// as duplicates of the first would resurrect the very collapse the pre-pass
	// exists to prevent (workidentity.go).
	if posSuffix != "" {
		return false
	}
	// The READ-ONLY author resolution: the same identity rules getOrCreateWork will
	// apply (personSlug plus the initials merge) with nothing created.
	authors := p.rowWorkAuthorsRO(credits)
	if len(authors.identity) == 0 {
		return false
	}
	series := rowSeriesName(b)

	match, found := p.identityMatch(workTitle, series, lang, authors)
	if !found {
		return false
	}
	// A match the row's OWN slug candidates reach is not a duplicate the guard has
	// any business refusing: getOrCreateWork will merge the row into that very work,
	// which is the behaviour that has always been correct. Only a match the chain
	// CANNOT see - which is what a decorated title produces - would become a second
	// record.
	if p.slugChainReaches(match.work, workTitle, fullTitle, posSuffix, authors, claim) {
		return false
	}
	// The POSITION veto, the strongest of the five internal/audit measured: the row
	// claims a place in a series the matched work occupies DIFFERENTLY, so the
	// catalogue itself says they are different volumes - whatever their titles
	// reduce to. It is read from the series record (seriesClaim.compatible: where the
	// work ended up, else what it asked for), never from the titles, because 14% of
	// the audit's clusters are same-title distinct volumes whose titles state no
	// number at all.
	if ws := p.works[match.work]; ws != nil && !claim.compatible(ws) {
		return false
	}

	p.summary.SkippedDuplicateIdentity++
	if len(p.dupIdentityExamples) < maxWarnExamples {
		p.dupIdentityExamples = append(p.dupIdentityExamples,
			fmt.Sprintf("%q -> %q (%q)", workTitle, match.work, match.title))
	}
	// The durable, machine-readable twin of the warning, in the same worklist the
	// contradiction guards write to: run, ASIN, the work whose identity was already
	// recorded, and both titles. `field` names WHICH disagreement this is, so a
	// worklist holding runtime and release-date rows stays sortable.
	p.recordConflict(b, RecRef{Work: match.work}, conflictFieldWorkIdentity, match.title, workTitle)
	return true
}

// conflictFieldWorkIdentity is the Conflict.Field value a refused duplicate row
// carries. It is not a schema field like "runtime_min": the disagreement is about
// which BOOK the row is, so the name says that rather than naming `title`, which
// would read as "these two titles disagree" when the point is that they agree.
const conflictFieldWorkIdentity = "work_identity"

// identityMatch resolves a row's normalized identity against the catalogue AND
// against the works this run has already created.
//
// The run half matters as much as the disk half: a wave is imported in batches, and
// two rows of one batch carrying two decorated spellings of one title would
// otherwise both create - the second finding nothing in an index built from disk.
// It is answered with the importer's OWN matchWork rule over the run's workState,
// which is the rule of record for "is this row that work" and already has the
// author sets in hand, plus the same stated-volume test the disk half applies.
//
// It requires exactly ONE candidate, and that is the AMBIGUITY veto rather than an
// implementation convenience. A key that names several works is a key that cannot
// say which of them the row is: the measured shape is a serial published under its
// bare series name, where the tree holds "bravelands-book-1" and
// "bravelands-book-2" and a row titled just "Bravelands" with no position stated
// could be either, or a third volume. Refusing it would drop a book we may not
// hold; attributing it to one of them would file a recording under the wrong
// volume. So the row proceeds on the long-standing behaviour (it mints its own
// work) and the resulting cluster is what metacheck's census reports.
func (p *planner) identityMatch(workTitle, series, lang string, authors workAuthors) (duplicateIdentityMatch, bool) {
	key := p.identity.Key(workTitle, series)
	if key == "" {
		return duplicateIdentityMatch{}, false
	}
	var found []duplicateIdentityMatch
	for _, slug := range p.runIdentity[key] {
		ws, ok := p.works[slug]
		if !ok || !langCompatible(ws.lang, lang) || matchWork(ws, authors) == matchNone {
			continue
		}
		was := p.runIdentified[slug]
		if !titlerule.SameStatedVolume(workTitle, series, was.title, was.series) {
			continue
		}
		found = append(found, duplicateIdentityMatch{work: slug, title: was.title, key: key})
	}
	for _, m := range p.identity.Match(workTitle, series, lang, authors.allSet(), authors.set()) {
		// A work this run created is in p.works with the run's own state; the disk
		// index cannot hold it, so a hit here is always a catalogued record - and
		// cannot be a repeat of one the run half already named.
		found = append(found, duplicateIdentityMatch{work: m.Work.ID, title: m.Work.Title, key: key})
	}
	if len(found) != 1 {
		return duplicateIdentityMatch{}, false
	}
	return found[0], true
}

// runWorkIdentity is what the run remembers about a work it has written to, for the
// run half of the probe: the title and series name its identity key was derived
// from, which is what the stated-volume test needs on that side.
type runWorkIdentity struct {
	title  string
	series string
}

// rememberIdentity records the normalized identity of a work this run created (or
// merged a row into) under the key that row was judged by, so a later row of the
// same run meets it. See identityMatch for why the run's own output has to be in
// the index.
//
// It registers a MERGED row's key too, deliberately: the merge established that the
// row and the work are one book, so the key it was judged by names that work as
// truly as the work's own does. The FIRST row to reach a work wins the remembered
// title, so what a later row is compared against does not drift row by row.
func (p *planner) rememberIdentity(key, slug, title, series string) {
	if key == "" || slug == "" {
		return
	}
	if _, seen := p.runIdentified[slug]; !seen {
		p.runIdentified[slug] = runWorkIdentity{title: title, series: series}
	}
	for _, have := range p.runIdentity[key] {
		if have == slug {
			return
		}
	}
	p.runIdentity[key] = append(p.runIdentity[key], slug)
}

// slugChainReaches reports whether the row's own work-slug candidates include the
// matched work - in which case getOrCreateWork will merge into it and no duplicate
// can be minted.
//
// It walks the same chains getOrCreateWork walks and in the same order
// (workCandidates over the resolved title, plus the FULL-title retry it falls back
// to), because "would the create path find this work" has exactly one answer and it
// is that function's. A probe-only candidate counts: the row can merge into it, it
// simply may not create there.
func (p *planner) slugChainReaches(want, workTitle, fullTitle, posSuffix string, authors workAuthors, claim *seriesClaim) bool {
	for _, title := range []string{workTitle, fullTitle} {
		base := Slugify(title)
		if base == "" {
			continue
		}
		probe := positionClaim{}
		if posSuffix != "" {
			base = BoundedSlugTail(base, "-"+posSuffix)
		} else {
			probe = claim.position()
		}
		cands, _ := workCandidates(base, authors, probe)
		for _, c := range cands {
			if c.slug == want {
				return true
			}
		}
	}
	return false
}

// rowSeriesName is the series NAME a row states, or "" - the name its title is
// cleaned against for the identity key.
//
// The FIRST claim with a usable position, which is the same one addBook resolves a
// seriesClaim from, so the key is derived against the series the row is actually
// about. A row stating no position states no membership either, and a title cleaned
// against nothing simply keeps whatever series name it spells out - which is why
// the intake gate has a second, series-aware door and this one does not need it.
func rowSeriesName(b sourceBook) string {
	for _, r := range b.series {
		if r.seqOK && strings.TrimSpace(r.name) != "" {
			return r.name
		}
	}
	return ""
}

// reportDuplicateIdentities appends the run's ONE aggregated warning for the rows
// the guard refused. Aggregated for the reason every other bulk refusal is: the
// news is "this wave carried N books we already have under other titles", and a
// per-row line would bury it in a wave that carries thousands.
func (p *planner) reportDuplicateIdentities() {
	if p.summary.SkippedDuplicateIdentity == 0 {
		return
	}
	p.summary.Warnings = append(p.summary.Warnings, withExamples(
		fmt.Sprintf("%d row(s) name a book the catalogue already holds under a differently-spelled title; "+
			"no work was created for them (they are recoverable: re-import once a maintainer has resolved the pair)",
			p.summary.SkippedDuplicateIdentity),
		p.dupIdentityExamples))
}

// newWorkIdentityIndex builds the run's normalized-identity index, for the CREATE
// mode only. cat may be nil (a best-effort load found nothing), in which case there
// is nothing to guard against.
func newWorkIdentityIndex(res check.Result, mode Mode) *check.WorkIdentity {
	if mode != ModeCreate || res.Catalog == nil {
		return nil
	}
	return check.NewWorkIdentity(res.Catalog)
}
