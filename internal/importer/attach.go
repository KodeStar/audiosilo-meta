package importer

import (
	"regexp"

	"github.com/kodestar/audiosilo-meta/internal/titlerule"
)

// attach.go is the ONE decision "this row is another edition of the volume the
// catalogue already holds at the position it claims", which two callers ask:
//
//   - libex-select, where it turns a row that used to be refused as
//     "series position already claimed" into a row SELECTED FOR ATTACHMENT; and
//   - the create path under Options.ExistingSeriesOnly (addBook), where such a
//     row becomes a recording of that work - a new one when its narrators differ
//     from every recording's, or its ASIN merged onto the recording whose
//     narrators match (addRecording's same-narrator merge, the path
//     Summary.MergedASINs counts).
//
// Both ask the same function over the same batch resolution, so a row the
// selector keeps for attachment is attached by the import of exactly that
// selection, and a row it refuses is never attached. The FALL-THROUGH agrees
// too: a row whose completion claim lands on an occupied position and does not
// attach is refused by both - libex-select as position-claimed, and the import
// (under ExistingSeriesOnly) skips it with a warning rather than planning a
// sibling work beside the incumbent, whatever subset the caller passes.
//
// Why a title key and not the create path's slug-chain walk (resolveWork /
// mergeOnlyWalk): the walk finds a work by its title's SLUG CHAIN, and the rows
// this rule exists for are exactly the ones whose retailer title decorates the
// catalogued one ("Lord of The System" beside "Lord of The System: Book 7", "The
// Hunter's Code" beside "The Hunter's Code: Book 13") - their slugs differ, the
// walk cannot see the occupant, and the create path would have planned them as
// sibling works. The occupant is already known here (the position names it), so
// the only title question is "do these two titles name one book", which is the
// titlerule question the intake gate asks. The AUTHOR question is the
// importer's own (matchWork); only the title reading is this file's.
//
// Measured on the live sync bot (2026-09-21..27), the selector's biggest refusal
// class was a claimed position, and many of those rows were the SAME book under
// another marketplace, re-release or narrator. The rule is conservative on
// purpose: a wrong attachment asserts "this IS that book" and is not recoverable
// by a later run, while a refused row is. So every one of these must hold:
//
//   - SERIES and POSITION: the row's completion claim (the first claim the batch
//     sends to a catalogued series) names the position the incumbent occupies;
//   - OCCUPANT: the work held the position when the catalogue was LOADED - a
//     work this run created is never an attachment target, so a same-run
//     sibling row of a new completion goes through the create path exactly as
//     it always did;
//   - AUTHORS: the importer's own work-identity test (matchWork, the one
//     walkWorkChain and the recordings-only resolver apply to a candidate), asked
//     of the occupant alone: identity sets nested both ways, persons resolved by
//     resolvePerson, so a translator's role credit and a spelling the person
//     rules fold do not block and an extra or a different author does;
//   - TITLE: the row's title (short or full) and the incumbent's reduce to one
//     titlerule compare key (StripDecoration against the series name when it
//     proposes a title, then CompareKeyWhole) - the gate internal/issueform's
//     decorated-title check applies. A title that differs, a translated one
//     included, is refused: it may well be the same book, but nothing here can
//     verify a translation;
//   - NO VETO in ANY of the row's title variants (short title, full title,
//     subtitle), not only the one that matched: none may state a VOLUME
//     contradicting the claimed position (statedVolumePosition, seriespos.go's
//     reading), and none may say the row is a different PRODUCT of the book than
//     the incumbent's title says it is (a split-release part, a collection, a
//     young-readers adaptation) - the statements the decoration strip removes
//     along with the retailer noise;
//   - a NARRATOR: a row the import cannot record attaches nothing;
//   - LANGUAGE: compatible under the importer's langCompatible. A work is
//     language-scoped in this catalogue (a translation is a different work,
//     metacheck's cross-language-recording advisory), so a same-titled row in
//     another language is refused rather than attached as a cross-language
//     recording.
//
// What an attachment may do is narrower still: it never creates a work, never
// places anything in a series (the incumbent is already there, and its other
// memberships are not the row's to change), never moves the incumbent and never
// rewrites a recorded value - addRecording writes a new recording or appends an
// ASIN (and any unclaimed ISBN) with this run's provenance, nothing else.

// attachWork returns the work the row is another edition of, when occupant - the
// work the catalogue held at the position the row's claim ref names, as the
// catalogue was LOADED (seriesState.loaded; libex-select's positions are the
// same map) - is that same book by the rule above; nil otherwise. seriesName is
// the catalogued series' name, the title rule's context.
func (p *planner) attachWork(b sourceBook, seriesName, occupant string, ref seriesRef) *workState {
	if occupant == "" || !ref.seqOK {
		return nil
	}
	ws := p.works[occupant]
	if ws == nil || !p.sameBookAs(ws, b, seriesName, ref) {
		return nil
	}
	return ws
}

// sameBookAs reports whether row b is another edition of work ws, which sits in
// the series named seriesName at the position claim ref names, as attach.go's
// header states.
func (p *planner) sameBookAs(ws *workState, b sourceBook, seriesName string, ref seriesRef) bool {
	lang, ok := mapLanguage(b.str("language"))
	if !ok || !langCompatible(ws.lang, lang) {
		return false
	}
	// A row the import cannot make a recording of (no narrator:
	// admitRecordingFacts) attaches nothing, so it is not selected as though it
	// would - and on the import side it never reaches this question.
	if len(p.rowNarratorNames(b)) == 0 {
		return false
	}
	want := p.incumbentTitleKey(ws, seriesName)
	if want == "" {
		return false
	}
	titled := false
	for _, t := range []string{b.str("title_short"), b.str("title")} {
		if t != "" && attachTitleKey(t, seriesName) == want {
			titled = true
		}
	}
	if !titled || vetoed(rowTitleVariants(b), workTitleVariants(ws), seriesName, ref) {
		return false
	}
	return matchWork(ws, p.rowWorkAuthorsRO(p.rowAuthorCredits(b))) != matchNone
}

// incumbentTitleKey is a work's attachTitleKey within a series, computed once
// per (work, series) for the run: every regional sibling row of a volume asks
// it again.
func (p *planner) incumbentTitleKey(ws *workState, seriesName string) string {
	k := ws.slug + "\x00" + seriesName
	if key, ok := p.attachKeys[k]; ok {
		return key
	}
	if p.attachKeys == nil {
		p.attachKeys = map[string]string{}
	}
	key := attachTitleKey(ws.title, seriesName)
	p.attachKeys[k] = key
	return key
}

// attachTitleKey is a title's comparison key for the attach rule: the edition
// markers the import strips (cleanWorkTitle), then the retailer decoration
// titlerule can remove against the series name - only when it proposes a title,
// exactly as internal/issueform's gate uses it - then CompareKeyWhole.
func attachTitleKey(title, seriesName string) string {
	t := cleanWorkTitle(title)
	if stripped, _, ok := titlerule.StripDecoration(t, seriesName); ok {
		t = stripped
	}
	return titlerule.CompareKeyWhole(t)
}

// workTitleVariants is every title the incumbent states: its title, its
// subtitle, and the two joined as a retailer would print them.
func workTitleVariants(ws *workState) []string {
	if ws.subtitle == "" {
		return []string{ws.title}
	}
	return []string{ws.title, ws.subtitle, ws.title + ": " + ws.subtitle}
}

// rowTitleVariants is every title a row states: the short title, the full
// "Title: Subtitle" and the subtitle on its own.
func rowTitleVariants(b sourceBook) []string {
	var out []string
	for _, key := range []string{"title_short", "title", "subtitle"} {
		if t := b.str(key); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// vetoed reports whether the attachment is refused by a title statement: a row
// variant stating a volume that contradicts the claimed position, or a
// one-sided PRODUCT statement - a split-release part, a collection, a
// young-readers adaptation - that one side makes in any of its variants and the
// other makes in none, in either direction. Every variant is read on both
// sides, since a retailer or a contributor puts it in whichever field they like
// ("The Lost Coast" with the subtitle "Young Readers Edition").
func vetoed(rowVariants, workVariants []string, seriesName string, ref seriesRef) bool {
	for _, t := range rowVariants {
		if _, contradicts := statedVolumePosition(ref, t); contradicts {
			return true
		}
	}
	row, work := productStatements(rowVariants, seriesName), productStatements(workVariants, seriesName)
	return row != work
}

// productStatements is which one-sided product statements any of a side's
// title variants makes.
func productStatements(variants []string, seriesName string) (s struct{ part, coll, young bool }) {
	for _, t := range variants {
		s.part = s.part || splitPart(t)
		s.coll = s.coll || titlerule.IsCollectionIn(t, seriesName)
		s.young = s.young || titlerule.IsYoungReadersAdaptation(t)
	}
	return s
}

// splitOfRE is a split release's bracketed part count - "(1 of 2)", "[Part 2 of
// 3]", "(Teil 1 von 2)" - with no marker word but a PART word in front of the
// number, so the retailer's series count "(Book 1 of 3)" is not one.
var splitOfRE = regexp.MustCompile(`(?i)[(\[]\s*(?:(?:part|pt\.?|teil)\s*)?\d+\s+(?:of|von)\s+\d+\s*[)\]]`)

// splitPart reports whether a title names itself one PART of a split release:
// a bracketed part count, or a volume marker of the part words proper ("Part 2",
// "Pt. 2"). "Volume N" is deliberately not one here: it is the commonest series
// numbering there is, and a volume that contradicts the claimed position is
// already refused (statedVolumePosition).
func splitPart(title string) bool {
	if splitOfRE.MatchString(title) {
		return true
	}
	h, ok := titlerule.VolumeHeadOf(title)
	return ok && (h.Marker == "part" || h.Marker == "parts" || h.Marker == "pt" || h.Marker == "pts")
}

// completionClaim is the claim a row completes a series with, as libex-select
// reads it (seriesVerdict.observe): the FIRST of its claims the batch resolution
// sent to a series the catalogue holds, and its index in b.series. ok is false
// (and at -1) when there is none.
func completionClaim(b sourceBook) (r seriesRef, at int, ok bool) {
	for i, r := range b.series {
		if r.target.found {
			return r, i, true
		}
	}
	return seriesRef{}, -1, false
}

// attachTarget is the create path's side of the rule, under
// Options.ExistingSeriesOnly: the loaded work at the position the row's
// completion claim names (occupant, "" when the position was free at load), and
// the work the row is to be ATTACHED to (nil when it is not another edition of
// it). The claim is the batch's resolution (seriesRef.target, decided before any
// row was planned), so it is the claim libex-select's batch re-check judged.
func (p *planner) attachTarget(b sourceBook) (ws *workState, occupant string) {
	if !p.existingSeriesOnly || p.mode != ModeCreate {
		return nil, ""
	}
	r, _, ok := completionClaim(b)
	if !ok || !r.seqOK {
		return nil, ""
	}
	ss := p.series[r.target.slug]
	if ss == nil {
		return nil, ""
	}
	occupant = ss.loaded[r.seq]
	return p.attachWork(b, ss.name, occupant, r), occupant
}

// attachRow writes an attached row onto its work: a new recording, or its ASIN
// merged onto the same-narrator recording (addRecording decides, with every
// guard it applies to any recording - the same-production runtime and abridged
// tests, the serial guard, the region and ISBN rules). Nothing else is written:
// no work fact, no series placement.
//
// Summary.Attached counts the row only when addRecording actually wrote
// something - a recording, or the ASIN merged onto one - so a row one of its
// guards turned away is not reported as attached.
func (p *planner) attachRow(ws *workState, b sourceBook, workTitle, asin, lang string, narratorNames []string, warn func(string, ...any)) {
	narratorSlugs := p.creditSlugs(narratorNames, warn)
	recs, merged := p.summary.NewRecordings, p.summary.MergedASINs
	if p.addRecording(ws, b, workTitle, asin, lang, narratorSlugs, warn) && asin != "" {
		// addBook's single-owner rule for the ASIN registry: claimed only once
		// the ASIN actually landed on a recording.
		p.asins[asin] = true
	}
	if p.summary.NewRecordings > recs || p.summary.MergedASINs > merged {
		p.summary.Attached++
	}
}

// noteSkip records a row the run refused for a reason with a refusal code, for
// the --skipped worklist (Summary.Skips). A row with no ASIN cannot be named
// and is not listed.
func (p *planner) noteSkip(asin, reason string) {
	if asin == "" {
		return
	}
	p.summary.Skips = append(p.summary.Skips, RowSkip{ASIN: asin, Reason: refusalCodeOf[reason]})
}
