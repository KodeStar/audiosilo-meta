package importer

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/titlerule"
)

// seriespos.go settles ONE question per series claim a row carries: which
// position the work is actually placed at. Two rules, both measured on real
// intakes and both narrow:
//
//  1. A MISSING position is filled from the live libex service (Part 1). A
//     personal library export states the series a file is tagged with and, very
//     often, no part number at all - 97 of 123 series rows on the two intakes
//     that prompted this. Every one of them warned "missing or invalid position"
//     and left the work out of its series, which is a fact the retailer knows and
//     the row simply did not carry. The lookup is INJECTABLE and nil by default,
//     so an offline run and every existing caller are byte-for-byte what they
//     were.
//
//  2. A position the row's own TITLE contradicts is arbitrated in the TITLE's
//     favour (Part 2). "Towerbound, Book 6" arrived with series_position 8,
//     because Audible's series index counts the side stories the catalogue
//     numbers 3.1 and 5.1, and the work was placed at 8 until a maintainer moved
//     it by hand. A title that spells out its own volume is the book talking
//     about itself; the index is the retailer's shelf.
//
// Neither rule ever silently overrides a placement: the fill reports one
// aggregated note per run and the arbitration warns per row, both naming the
// position they chose.

// SeriesPosition is one series membership a SeriesPositionLookup reported, in
// the SOURCE's own spelling. Both fields are normalized here through
// makeSeriesRef - the same call every parser builds a ref with - so a looked-up
// claim and a row's own claim can never be read by two different rules.
type SeriesPosition struct {
	// Name is the series name the record states.
	Name string
	// Position is the position within it, as the record spells it ("3", "3.0",
	// "1-3", or something the grammar rejects).
	Position string
}

// SeriesPositionLookup answers "where does this ASIN sit in its series" for a
// row whose own source did not say.
//
// It is an interface on Options so the network stays out of the importer: the
// real implementation is LibexClient (libexfill.go) and a test passes a map. A
// nil lookup is the default and means the rule is OFF - which is what keeps
// every existing caller, every offline run and every fixture unchanged.
//
// It is best-effort by contract. The libex service is free, public and run by
// one person, so an error here is counted and reported, never fatal: the row
// keeps the warning it would have had.
type SeriesPositionLookup interface {
	SeriesRefs(ctx context.Context, asin string) ([]SeriesPosition, error)
}

// defaultSeriesLookupCap bounds the lookups ONE run may perform when the caller
// named no cap, in the spirit of libex-fill's --limit: a wave is a bounded
// request against somebody else's free service, and an unbounded default would
// make a million-row import a denial of service by accident. Only a row that
// actually needs a position is counted against it.
const defaultSeriesLookupCap = 100

// seriesLookupCap resolves Options.SeriesLookupLimit: 0 takes the default, and a
// negative value is read as the caller deliberately asking for no cap.
func seriesLookupCap(limit int) int {
	switch {
	case limit == 0:
		return defaultSeriesLookupCap
	case limit < 0:
		return -1
	default:
		return limit
	}
}

// fillSeriesPositions fills the row's MISSING series positions from the lookup
// (Part 1), rewriting the refs in place.
//
// It runs before the row's series CLAIM is resolved, before the
// duplicate-identity guard and before placement, so a filled position is the one
// position every one of them reads. b.series is a slice, so the rewrite is
// visible to the batch entry too - harmless, because a row is planned once.
//
// It is called only for a row the planner has already admitted (a language, a
// narrator, an author and a title), so a dropped row never spends a lookup.
func (p *planner) fillSeriesPositions(b sourceBook, asin, workTitle string) {
	if p.seriesLookup == nil || asin == "" || !needsSeriesPosition(b) {
		return
	}
	if p.seriesLookupLeft == 0 {
		return
	}
	if p.seriesLookupLeft > 0 {
		p.seriesLookupLeft--
	}
	refs, err := p.seriesLookup.SeriesRefs(context.Background(), asin)
	if err != nil {
		p.noteSeriesLookupFailure(asin, err)
		return
	}
	for i := range b.series {
		if b.series[i].seqOK {
			continue
		}
		pos, ok := lookedUpPosition(refs, b.series[i].name)
		if !ok {
			continue
		}
		b.series[i].seq, b.series[i].seqOK = pos, true
		p.noteSeriesPositionFilled(workTitle, b.series[i].name, pos)
	}
}

// needsSeriesPosition reports whether any of the row's claims is missing its
// position. It is what makes the cap count ROWS THAT NEED A LOOKUP rather than
// rows: a library whose files are all tagged with a part number spends nothing.
func needsSeriesPosition(b sourceBook) bool {
	for _, r := range b.series {
		if !r.seqOK {
			return true
		}
	}
	return false
}

// lookedUpPosition is the position the lookup stated for THIS series, or nothing.
//
// Every candidate goes through makeSeriesRef first, so the name is cleaned and
// the position validated by exactly the rules the row's own claim went through,
// and the name comparison is sameSeriesName - the importer's own test for "these
// two names are one series". A record that names a DIFFERENT series, or names
// this one with no usable position, states nothing about this row: the caller
// leaves the claim as it found it and the row keeps its warning.
func lookedUpPosition(refs []SeriesPosition, name string) (string, bool) {
	for _, got := range refs {
		cand := makeSeriesRef(got.Name, got.Position)
		if cand.seqOK && sameSeriesName(cand.name, name) {
			return cand.seq, true
		}
	}
	return "", false
}

// sameSeriesName reports whether two series names are ONE series, by the
// importer's own rule rather than a looser one invented here: findSeries and
// getOrCreateSeries both bucket a name by its slug (which folds case, diacritics
// and punctuation) and then require a case-insensitive name match inside that
// bucket. A name with no addressable slug is no series at all
// (getOrCreateSeries refuses to mint one), so it matches nothing.
func sameSeriesName(a, b string) bool {
	slug := Slugify(a)
	return slug != "" && slug == Slugify(b) && strings.EqualFold(a, b)
}

// placementPosition is the position a row is actually placed at in series r:
// the source's, unless the row's own TITLE states a different volume for that
// series, in which case the title wins (Part 2).
//
// The title is read through titlerule.StatedVolume against the series NAME the
// row states - the same call, on the same two strings, that the
// duplicate-identity guard's positive volume test makes (dupidentity.go), so the
// two readings of one title cannot disagree about which volume it claims to be.
// They use that answer for different questions: the guard asks whether the
// catalogue confirms the volume, this asks where the row belongs.
//
// The collision rules are untouched. A work the series already lists keeps its
// entry (addToSeries reports that, as it always has), a title position another
// work holds is not free to move into so the source's position stands, and a
// source position that is taken too reaches addToSeries's own "position already
// taken" warning by the same path it always did.
func (p *planner) placementPosition(r seriesRef, work, title string, warn func(string, ...any)) string {
	pos, ok := statedVolumePosition(r, title)
	if !ok {
		return r.seq
	}
	if ss := p.findSeries(r.name); ss != nil {
		if _, member := ss.members[work]; member {
			return r.seq
		}
		if other, taken := ss.positions[pos]; taken && other != work {
			return r.seq
		}
	}
	warn("series %q: source position %q disagrees with the title's Book %s; placed at %s", r.name, r.seq, pos, pos)
	return pos
}

// statedVolumePosition is the position the TITLE states for series r, when that
// contradicts the position r's source stated. It answers nothing otherwise: a
// title that states no volume, one that agrees, and one whose number the
// vocabulary cannot read all leave the source's position alone.
//
// Two bounds, both deliberate:
//
//   - the SOURCE position must be a single slot. A range is an omnibus saying
//     which volumes it collects, and a title's single number does not contradict
//     it.
//   - the TITLE must carry a VOLUME MARKER (titlerule.HasVolumeMarker: "Book 6",
//     "Vol 2", "Book One"). StatedVolume also reads a residual that is nothing
//     but a number, which is right for the duplicate gates - a false positive
//     there only costs a missed refusal - and wrong here: a work titled "1984" or
//     "2001" would otherwise be placed at position 1984. A year is not a series
//     position, and the marker is what tells a book stating its volume from a book
//     whose title happens to be a number.
func statedVolumePosition(r seriesRef, title string) (string, bool) {
	if _, _, isRange := PositionRange(r.seq); isRange {
		return "", false
	}
	if !titlerule.HasVolumeMarker(title) {
		return "", false
	}
	v, stated := titlerule.StatedVolume(title, r.name)
	if !stated {
		return "", false
	}
	pos, ok := NormalizeSequence(strconv.FormatFloat(v, 'f', -1, 64))
	if !ok || SameSlot(pos, r.seq) {
		return "", false
	}
	return pos, true
}

// noteSeriesPositionFilled records one filled position for the run's aggregated
// note.
func (p *planner) noteSeriesPositionFilled(title, series, pos string) {
	p.seriesPositionsFilled++
	if len(p.seriesPositionExamples) < maxWarnExamples {
		p.seriesPositionExamples = append(p.seriesPositionExamples,
			fmt.Sprintf("%q in %q at %s", title, series, pos))
	}
}

// noteSeriesLookupFailure records one lookup that did not answer.
func (p *planner) noteSeriesLookupFailure(asin string, err error) {
	p.seriesLookupFailed++
	if len(p.seriesLookupFailures) < maxWarnExamples {
		p.seriesLookupFailures = append(p.seriesLookupFailures, fmt.Sprintf("%s: %v", asin, err))
	}
}

// reportSeriesPositionLookups appends the run's aggregated lines for the lookup:
// what it filled, and what it could not reach.
//
// The failures are reported rather than swallowed for the same reason the fill
// pass prints its errors: a bot run whose lookups all failed and one that never
// needed a lookup produce the same records, and only this line tells them apart.
func (p *planner) reportSeriesPositionLookups() {
	if p.seriesPositionsFilled > 0 {
		p.summary.Warnings = append(p.summary.Warnings, withExamples(
			fmt.Sprintf("%d series position(s) taken from libex", p.seriesPositionsFilled),
			p.seriesPositionExamples))
	}
	if p.seriesLookupFailed > 0 {
		p.summary.Warnings = append(p.summary.Warnings, withExamples(
			fmt.Sprintf("%d series-position lookup(s) failed; those rows keep the position their source stated",
				p.seriesLookupFailed),
			p.seriesLookupFailures))
	}
}

// SeriesRefs implements SeriesPositionLookup against the live libex service. It
// is the same record the fill pass fetches (fetchOne, mirror first then the live
// Audible path) read for its `series` array alone, through libexSeries - so what
// the importer learns here is exactly what a libex row would have told it.
//
// An ASIN libex does not hold is not an error: plenty of a personal library is
// not on Audible at all, and "no record" and "a record naming no series" are one
// outcome to the caller - nothing to fill.
//
// The Pause is slept before EVERY request rather than between them. The fill
// pass can pace against its own loop; a lookup is made one row at a time from
// inside the planner, with no loop to be first in, and one pause per lookup is
// what keeps a run polite at the service's own rate.
func (c *LibexClient) SeriesRefs(ctx context.Context, asin string) ([]SeriesPosition, error) {
	if c.Pause > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(c.Pause):
		}
	}
	rec, err := c.fetchOne(ctx, asin)
	if errors.Is(err, errLibexNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	refs := libexSeries(rec["series"])
	if len(refs) == 0 {
		return nil, nil
	}
	out := make([]SeriesPosition, 0, len(refs))
	for _, r := range refs {
		// The RAW position, not the normalized one: the caller normalizes every
		// candidate itself, so a fake lookup in a test and this one hand their
		// answers over in the same, un-pre-digested shape.
		out = append(out, SeriesPosition{Name: r.name, Position: r.rawSeq})
	}
	return out, nil
}
