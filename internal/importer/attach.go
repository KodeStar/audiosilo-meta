package importer

import (
	"strconv"

	"github.com/kodestar/audiosilo-meta/internal/titlerule"
)

// attach.go ATTACHES a row to the work the catalogue already holds at the series
// position the row claims, when the row is another edition of that very book: it
// becomes a new recording of the work, or its ASIN is merged onto the recording
// whose narrators match (addRecording). It is off unless the run asks for it
// (Options.AttachEditions / SelectOptions.AttachEditions, `--attach-editions`),
// and it is asked by two callers over the same batch resolution:
//
//   - libex-select, which keeps such a row for attachment instead of refusing it
//     as "series position already claimed"; and
//   - the create path (addBook), which attaches it - or, when the row at an
//     occupied position is NOT that book, refuses it (Summary.SkippedOccupied,
//     code position-claimed), so no run with the option ever plans a second work
//     at an occupied position, whatever subset it is handed.
//
// IT IS NOT A NEW "IS THIS ROW THAT BOOK" RULE. The answer is the importer's
// own identity machinery, constrained to one candidate - the work that held the
// position when the catalogue was LOADED (seriesState.loaded; a work this run
// created is never a target): the create path's slug-chain resolution
// (resolveWork, matchWork for authors) or the duplicate-identity guard's
// normalized title identity (dupidentity.go, check.WorkIdentity) must resolve
// the row to exactly that occupant. What this file adds is only the evidence a
// title cannot give and a position can: the vetoes below - product statements
// over both sides' title and subtitle (productOf: a split-release part, which
// must be the SAME part on both sides, a derived edition, a collection), plus
// the one only a positioned claim can ask, a title stating a volume that
// contradicts the claimed position. They are this rule's alone: the
// duplicate decisions keep their own measured collection rule.
//
// THE DECISION IS SPLIT IN TWO, by what it depends on. attachCandidate is
// CENSUS-INDEPENDENT - the occupant, a title the machinery could resolve to it,
// a compatible language, a narrator, no veto - so libex-select can ask it while
// the export streams. attachFor adds the AUTHORS, which depend on the batch's
// credit decisions (creditContext), and is asked with the batch's context: by
// libex-select's batch re-check and by the import. Both sides therefore agree on
// every row, the fall-through included.
//
// A translated edition is never attached: a work is language-scoped here (a
// translation is a different work - metacheck's cross-language advisory), so
// both languages must be KNOWN and EQUAL - an unknown on either side is no
// evidence the two are one edition's language, and attaches nothing.

// attachCandidate is the census-independent half of the rule: the loaded work
// occupant, when the row could be another edition of it - a title the identity
// machinery could resolve to it, a compatible language, a narrator, and no veto.
// nil otherwise. seriesName is the catalogued series' name, ref the row's
// completion claim.
func (p *planner) attachCandidate(b sourceBook, seriesName, occupant string, ref seriesRef) *workState {
	ws := p.works[occupant]
	if ws == nil || !ref.seqOK || !p.titleCouldName(b, ws) {
		return nil
	}
	lang, ok := mapLanguage(b.str("language"))
	if !ok || lang == "" || ws.lang != lang || len(rowNarratorNamesIn(creditContext{}, b)) == 0 {
		return nil
	}
	if vetoed(b, p.incumbentProduct(ws, seriesName), seriesName, ref) {
		return nil
	}
	return ws
}

// titleCouldName is the title-side precondition of the identity machinery: one
// of the row's titles either starts the occupant's slug chain (resolveWork walks
// the title's slug and its author/numbered successors) or has the normalized
// identity key the catalogue files the occupant under (check.WorkIdentity).
func (p *planner) titleCouldName(b sourceBook, ws *workState) bool {
	series := rowSeriesName(b)
	for _, t := range []string{b.str("title_short"), b.str("title")} {
		if t == "" {
			continue
		}
		t = cleanWorkTitle(t)
		if ts := slugOfTitle(t); !ts.fellBack && hasSlugPrefix(ws.slug, ts.slug) {
			return true
		}
		if p.identity != nil {
			for _, w := range p.identity.Works(p.identity.Key(t, series)) {
				if w.ID == ws.slug {
					return true
				}
			}
		}
	}
	return false
}

// hasSlugPrefix reports whether slug is base or one of base's chain successors
// ("<base>-<author>", "<base>-2").
func hasSlugPrefix(slug, base string) bool {
	return slug == base || (len(slug) > len(base) && slug[:len(base)] == base && slug[len(base)] == '-')
}

// attachFor is the whole rule, under the batch's credit context: the loaded
// occupant of the position the row's completion claim names ("" when that
// position was free at load), and the work the row attaches to - the occupant,
// when attachCandidate admits it and the identity machinery resolves the row to
// exactly it - or nil. workTitle is the row's resolved work title (the batch
// title pre-pass's, resolveWorkTitles).
func (p *planner) attachFor(ctx creditContext, b sourceBook, workTitle string) (ws *workState, occupant string) {
	r, at := completionClaim(b)
	if at < 0 || !r.seqOK {
		return nil, ""
	}
	ss := p.series[r.target.slug]
	if ss == nil {
		return nil, ""
	}
	occupant = ss.loaded[r.seq]
	if occupant == "" {
		return nil, ""
	}
	if ws = p.attachCandidate(b, ss.name, occupant, r); ws == nil {
		return nil, occupant
	}
	authors := p.rowWorkAuthorsROIn(ctx, rowAuthorCreditsIn(ctx, b))
	lang, _ := mapLanguage(b.str("language"))
	if len(authors.identity) == 0 {
		return nil, occupant
	}
	// The create path's own resolution, read-only: the row's title chain (and its
	// full title's, as a merge target) lands on the occupant.
	if p.resolveWork(workTitle, b.str("title"), b.qualifiedTitle, "", authors, lang, nil).ws == ws {
		return ws, occupant
	}
	// The duplicate-identity guard's catalogue half: the row's normalized
	// identity names exactly the occupant (one candidate - the ambiguity veto).
	if p.identity != nil {
		series := rowSeriesName(b)
		ms := p.identity.MatchKey(p.identity.Key(workTitle, series), workTitle, series, lang, authors.allSet(), authors.set())
		if len(ms) == 1 && ms[0].Work.ID == occupant {
			return ws, occupant
		}
	}
	return nil, occupant
}

// incumbentProduct is a work's product statements (productOf over its title and
// subtitle) in a series, computed once per (work, series) for the run: every
// regional sibling row of a volume asks it again.
func (p *planner) incumbentProduct(ws *workState, seriesName string) product {
	k := ws.slug + "\x00" + seriesName
	if pr, ok := p.attachProducts[k]; ok {
		return pr
	}
	if p.attachProducts == nil {
		p.attachProducts = map[string]product{}
	}
	pr := productOf(seriesName, ws.title, ws.subtitle)
	p.attachProducts[k] = pr
	return pr
}

// product is what one side's titles say about which product of the book it is:
// titlerule.ProductOf's derived-edition and collection statements, and WHICH
// split-release part it names (part: "N/M" for a part count, "part N" for a
// part marker, "" for none) - two sides are one product only when they name the
// same part or none.
type product struct {
	adapted, collection bool
	part                string
}

// productOf reads a side's product statements over all its titles. ANY part
// marker in titlerule's vocabulary is a statement, whatever the series: "Dragon
// Wars: Blood Brothers (1 of 10)" names a part count exactly as a split release
// does, and a count cannot say which it means - so it is refused beside a plain
// "Blood Brothers" (a missing recording beats a wrong one).
func productOf(seriesName string, titles ...string) product {
	pr := titlerule.ProductOf(seriesName, titles...)
	out := product{adapted: pr.Adapted, collection: pr.Collection}
	for _, t := range titles {
		if t == "" || out.part != "" {
			continue
		}
		if n, m, ok := titlerule.PartOf(t); ok {
			out.part = strconv.Itoa(n) + "/" + strconv.Itoa(m)
		} else if titlerule.IsSplitPart(t) {
			h, _ := titlerule.VolumeHeadOf(t)
			out.part = "part " + strconv.FormatFloat(h.Volume, 'f', -1, 64)
		}
	}
	return out
}

// vetoed reports whether a title statement refuses the attachment, read over
// EVERY title the row states (short, full, subtitle - a retailer puts
// "Young Readers Edition" in whichever field it likes): a stated volume that
// contradicts the claimed position (statedVolumePosition, seriespos.go's
// reading), or product statements (productOf - a split-release part, a derived
// edition, a collection) that differ from the incumbent's title and subtitle.
func vetoed(b sourceBook, incumbent product, seriesName string, ref seriesRef) bool {
	variants := []string{b.str("title_short"), b.str("title"), b.str("subtitle")}
	for _, t := range variants {
		if t == "" {
			continue
		}
		if _, contradicts := statedVolumePosition(ref, t); contradicts {
			return true
		}
	}
	return productOf(seriesName, variants...) != incumbent
}

// completionClaim is the claim a row completes a series with: the FIRST of its
// claims the batch resolution sent to a series the catalogue holds, and its
// index in b.series (-1 when there is none). libex-select's verdict and the
// import both read it off the row's resolved targets.
func completionClaim(b sourceBook) (seriesRef, int) {
	for i, r := range b.series {
		if r.target.found {
			return r, i
		}
	}
	return seriesRef{}, -1
}

// attachTarget is the create path's side: under Options.AttachEditions, the
// work this row attaches to and the occupant of its claimed position (see
// attachFor), under the run's credit context.
func (p *planner) attachTarget(b sourceBook, workTitle string) (ws *workState, occupant string) {
	if !p.attachEditions || p.mode != ModeCreate {
		return nil, ""
	}
	return p.attachFor(p.credits, b, workTitle)
}

// attachRow writes an attached row onto its work: a new recording, or its ASIN
// merged onto the same-narrator recording (addRecording decides, with every
// guard it applies to any recording - the same-production runtime and abridged
// tests, the serial guard, the region and ISBN rules). Nothing else is written:
// no work fact, no series placement.
//
// Summary.Attached counts the row only when addRecording wrote something; a row
// one of its guards turned away is a position-claimed skip instead, so the sync
// bot memoizes it rather than the selector keeping it every cycle.
func (p *planner) attachRow(ws *workState, b sourceBook, workTitle, asin, lang string, narratorNames []string, warn func(string, ...any)) {
	switch p.addRecording(ws, b, workTitle, asin, lang, p.creditSlugs(narratorNames, warn), warn) {
	case recMerged, recNew:
		p.summary.Attached++
	case recNone:
		p.summary.Skips = appendSkip(p.summary.Skips, asin, reasonPositionTaken)
	}
}
