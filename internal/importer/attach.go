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
// selection, and a row it refuses is never attached.
//
// Measured on the live sync bot (2026-09-21..27), the selector's biggest refusal
// class was a claimed position, and many of those rows were the SAME book under
// another marketplace, re-release or narrator. The rule is conservative on
// purpose: a wrong attachment asserts "this IS that book" and is not recoverable
// by a later run, while a refused row is. So every one of these must hold:
//
//   - SERIES and POSITION: the row's completion claim (the first claim the batch
//     sends to a catalogued series) names the position the incumbent occupies;
//   - AUTHORS: the row's identity authors and the incumbent's cover each other
//     under the importer's same-person rule (SamePerson) - every author on each
//     side is one person with some author on the other;
//   - TITLE: the row's title (short or full) and the incumbent's reduce to one
//     titlerule compare key (StripDecoration against the series name when it
//     proposes a title, then CompareKeyWhole) - the gate internal/issueform's
//     decorated-title check applies. A title that differs, a translated one
//     included, is refused: it may well be the same book, but nothing here can
//     verify a translation - and no row title may state a VOLUME contradicting
//     the claimed position (statedVolumePosition, seriespos.go's reading), nor
//     say it is a different product of the book than the incumbent's does (a
//     split-release part, a collection, a young-readers adaptation);
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

// attachWork returns the catalogued work a row is another edition of, when the
// row's claim ref lands in the catalogued series seriesSlug (the batch's
// resolution of it) and the work at ref's position there is that same book by
// the rule above; nil otherwise.
func (p *planner) attachWork(b sourceBook, seriesSlug string, ref seriesRef) *workState {
	ss := p.series[seriesSlug]
	if ss == nil || ss.isNew || !ref.seqOK {
		return nil
	}
	occupant := ss.occupantAt(ref.seq)
	if occupant == "" {
		return nil
	}
	ws := p.works[occupant]
	if ws == nil || !p.sameBookAs(ws, b, ss.name, ref) {
		return nil
	}
	return ws
}

// occupantAt is the work the series lists at position seq, comparing positions
// in the canonical spelling a claim arrives in (a stored "1.0" is position "1"),
// or "".
func (ss *seriesState) occupantAt(seq string) string {
	if w := ss.positions[seq]; w != "" {
		return w
	}
	for w, pos := range ss.members {
		if norm, ok := NormalizeSequence(pos); ok && norm == seq {
			return w
		}
	}
	return ""
}

// sameBookAs reports whether row b is another edition of work ws, which sits in
// the series named seriesName at the position claim ref names: language, title
// and authors, as attach.go's header states.
//
// A row title that states a VOLUME contradicting the claimed position (the
// seriespos.go reading, statedVolumePosition: "The Lost Coast, Book 2" claimed at
// 1) is never an attachment, whatever the rest says: the decoration strip would
// otherwise remove the one part of the title that disagrees, and a row that
// contradicts itself about which volume it is cannot be asserted to be this one.
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
	want := attachTitleKey(ws.title, seriesName)
	if want == "" {
		return false
	}
	titled := false
	for _, t := range []string{b.str("title_short"), b.str("title")} {
		if t == "" {
			continue
		}
		if _, contradicts := statedVolumePosition(ref, t); contradicts {
			return false
		}
		if attachTitleKey(t, seriesName) == want {
			if productStatementsDiffer(t, ws.title, seriesName) {
				return false
			}
			titled = true
		}
	}
	return titled && p.authorsCover(ws, p.rowAuthorCredits(b))
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

// productStatementsDiffer reports whether one of two titles that reduce to one
// key says it is a different PRODUCT of the book than the other does - the
// statements the decoration strip removes along with the retailer noise: a split
// release's part ("(2 of 2)", "Part 2"), a collection (titlerule's collection
// status), a young-readers adaptation. Each is a one-sided statement that the row
// is not the whole work the incumbent is, so it refuses the attachment.
func productStatementsDiffer(rowTitle, workTitle, seriesName string) bool {
	return splitPart(rowTitle) != splitPart(workTitle) ||
		!titlerule.SameCollectionStatus(rowTitle, seriesName, workTitle, seriesName) ||
		titlerule.IsYoungReadersAdaptation(rowTitle) != titlerule.IsYoungReadersAdaptation(workTitle)
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

// authorsCover reports whether a row's IDENTITY authors (its credits without a
// stated contributor role, or all of them when every one carries a role - the
// identityAuthors rule) and a work's identity authors cover each other under
// SamePerson: every author on either side is one person with an author on the
// other.
func (p *planner) authorsCover(ws *workState, credits []credit) bool {
	type person struct{ slug, name string }
	var row []person
	for _, c := range credits {
		if len(c.roles) == 0 {
			row = append(row, person{p.resolvePerson(c.name).slug, c.name})
		}
	}
	if len(row) == 0 {
		for _, c := range credits {
			row = append(row, person{p.resolvePerson(c.name).slug, c.name})
		}
	}
	if len(row) == 0 || len(ws.authors) == 0 {
		return false
	}
	work := make([]person, 0, len(ws.authors))
	for slug := range ws.authors {
		work = append(work, person{slug, p.people[slug]})
	}
	covered := func(a person, side []person) bool {
		for _, o := range side {
			if SamePerson(a.slug, a.name, o.slug, o.name) {
				return true
			}
		}
		return false
	}
	for _, a := range row {
		if !covered(a, work) {
			return false
		}
	}
	for _, a := range work {
		if !covered(a, row) {
			return false
		}
	}
	return true
}

// completionClaim is the claim a row completes a series with, as libex-select
// reads it (seriesVerdict.observe): the FIRST of its claims the batch resolution
// sent to a series the catalogue holds. ok is false when there is none.
func completionClaim(b sourceBook) (seriesRef, bool) {
	for _, r := range b.series {
		if r.target.found {
			return r, true
		}
	}
	return seriesRef{}, false
}

// attachTarget is the create path's side of the rule: under
// Options.ExistingSeriesOnly, the catalogued work this row is to be ATTACHED to
// instead of planned as a new work, or nil. The claim is the batch's resolution
// (seriesRef.target, decided before any row was planned), so it is the claim
// libex-select's batch re-check judged.
func (p *planner) attachTarget(b sourceBook) *workState {
	if !p.existingSeriesOnly || p.mode != ModeCreate {
		return nil
	}
	r, ok := completionClaim(b)
	if !ok {
		return nil
	}
	return p.attachWork(b, r.target.slug, r)
}

// attachRow writes an attached row onto its work: a new recording, or its ASIN
// merged onto the same-narrator recording (addRecording decides, with every
// guard it applies to any recording - the same-production runtime and abridged
// tests, the serial guard, the region and ISBN rules). Nothing else is written:
// no work fact, no series placement.
func (p *planner) attachRow(ws *workState, b sourceBook, workTitle, asin, lang string, narratorNames []string, warn func(string, ...any)) {
	narratorSlugs := p.creditSlugs(narratorNames, warn)
	if p.addRecording(ws, b, workTitle, asin, lang, narratorSlugs, warn) && asin != "" {
		// addBook's single-owner rule for the ASIN registry: claimed only once
		// the ASIN actually landed on a recording.
		p.asins[asin] = true
	}
	p.summary.Attached++
}
