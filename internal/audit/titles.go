package audit

import (
	"fmt"

	"github.com/kodestar/audiosilo-meta/internal/titlerule"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// detectWorkTitle reports every work whose title carries retailer decoration AND
// for which the audit can propose a SAFE cleaner one.
//
// A title-only class: it never proposes a slug change, because a slug is IDENTITY
// and moving one is a rename with its own referential consequences (every series
// membership and every sidecar that names it). The slug side of the same records is
// F-HYGIENE's, where it is flagged as a rename CANDIDATE and nothing more.
//
// It runs in TWO PASSES, and the second one is why. A proposal must not create a
// same-series title collision: 852 groups of works share a series and would have
// shared a title once their volume markers were stripped (Spice and Wolf runs to
// fourteen), and in those the number is the only thing a human can tell them apart
// by. That cannot be judged one work at a time, so every proposal is composed
// first, then the set is checked against itself.
func detectWorkTitle(ix *index) *findings {
	f := &findings{class: ClassWorkTitle}

	type candidate struct {
		work     *model.Work
		markers  []string
		want     string
		inferred bool // the series name came from the title, not from a membership
	}
	var cands []candidate
	for _, w := range ix.cat.Works {
		d := ix.derived(w)
		if len(d.markers) == 0 {
			continue
		}
		want, ok := titlerule.ProposeTitle(w.Title, d.seriesName)
		if !ok {
			continue
		}
		if titlerule.PrimaryDecoration(d.markers) == "" {
			continue
		}
		cands = append(cands, candidate{work: w, markers: d.markers, want: want, inferred: d.embedded})
	}

	// The title every member of a series would END UP with: its proposal if it has
	// one, else the title it already holds. A key that two members would share is a
	// collision, and every member of it that would CHANGE is refused.
	proposed := make(map[string]string, len(cands))
	for _, c := range cands {
		proposed[c.work.ID] = c.want
	}
	collides := map[string]bool{}
	for _, s := range ix.cat.Series {
		byTitle := map[string][]string{}
		for _, sw := range s.Works {
			w := ix.workByID[sw.Work]
			if w == nil {
				continue
			}
			final := w.Title
			if p, ok := proposed[w.ID]; ok {
				final = p
			}
			key := titlerule.CompareKey(final)
			if key == "" {
				continue
			}
			byTitle[key] = append(byTitle[key], w.ID)
		}
		for _, ids := range byTitle {
			if len(ids) < 2 {
				continue
			}
			for _, id := range ids {
				if _, changes := proposed[id]; changes {
					collides[id] = true
				}
			}
		}
	}

	for _, c := range cands {
		if collides[c.work.ID] {
			continue
		}
		sub := titlerule.PrimaryDecoration(c.markers)
		p := Proposal{
			Op:     OpRetitle,
			Target: c.work.ID,
			Field:  "title",
			From:   c.work.Title,
			To:     c.want,
			Reason: "the slug is identity and is deliberately left alone; see F-HYGIENE for the rename candidates",
		}
		// A series-name strip rests on WHICH series the title names, and when that
		// came from the title itself rather than from a membership the audit is
		// inferring both the series and which side of the separator the book is on.
		// A 30-record sample of the inferred ones still read ~7% wrong ("Doctor Who:
		// Time Lord Fairy Tales" reduced to "Doctor Who"), against ~0% for the
		// membership-backed ones, so the inferred half is advisory.
		if sub == titlerule.DecSeriesName && c.inferred {
			p.Op = OpReview
			p.Advisory = true
			p.Reason = "the series name was inferred from the title, not from a membership, so which side of the separator the " +
				"book's own title is on is a judgement: confirm the series before retitling"
		}
		f.add(Finding{
			Subclass: sub,
			Key:      c.work.ID,
			Works:    []WorkRef{ix.workBrief(c.work)},
			Markers:  c.markers,
			Propose:  p,
		})
	}
	return f
}

// W-NOSERIES subclasses.
const (
	noSeriesAndPosition = "series-and-position"
	noSeriesOnly        = "series-only"
	noPositionOnly      = "position-only"
)

// positionCeiling is how far past a series' highest known position a title-derived
// number may still be plausible. A series running 1..12 can gain a 13th; it cannot
// gain a 2140th, and a number like that is a marketing code or a year.
const positionCeiling = 3

// minImplausiblePosition is the value at or above which a title's number is read as
// a YEAR rather than a volume. No series numbers its volumes in the thousands, and
// "1984", "2140" and "101" arrive in titles constantly.
const minImplausiblePosition = 1500

// detectWorkNoSeries reports works that belong to no series but whose title names
// one, states a volume number, or both - the shape a bulk retailer import leaves
// when the series column was empty and the series was only ever in the title.
//
// The MEMBERSHIP proposal is the dangerous one (measured at ~60% wrong), and three
// things bound it. The series reference must be CORROBORATED - either the title
// carries the exact "<Series>, Book N" boundary shape, or the series already holds a
// work by one of this book's authors - because a series name is often two ordinary
// words that any title may contain. The position must be PLAUSIBLE against the
// series' own span. And the proposals must be consistent as a SET: two works cannot
// both take slot 4, so every claimant of a contested slot is downgraded.
func detectWorkNoSeries(ix *index) *findings {
	f := &findings{class: ClassWorkNoSeries}

	type candidate struct {
		work     *model.Work
		d        *workDerived
		slot     string
		vetoes   []string
		subclass string
	}
	var cands []candidate
	claims := map[string][]string{} // "<series>@<slot>" -> work ids claiming it

	for _, w := range ix.cat.Works {
		if len(ix.memberships[w.ID]) > 0 {
			continue
		}
		d := ix.derived(w)
		if !d.embedded {
			// No series resolved. A title that still spells a volume number out is
			// a membership nobody modeled, but the audit cannot say of what.
			if d.hasSeq {
				f.add(Finding{
					Subclass: noPositionOnly,
					Key:      w.ID,
					Works:    []WorkRef{ix.workBrief(w)},
					Propose: Proposal{
						Op: OpReview, Target: w.ID, Field: "series", To: formatSeq(d.seq),
						Advisory: true,
						Reason:   "the title states a volume number but no series in the catalogue matches it - identify the series first",
					},
				})
			}
			continue
		}
		c := candidate{work: w, d: d}
		if !d.hasSeq {
			c.subclass = noSeriesOnly
			cands = append(cands, c)
			continue
		}
		c.subclass, c.slot = noSeriesAndPosition, formatSeq(d.seq)
		c.vetoes = ix.membershipVetoes(w, d)
		if len(c.vetoes) == 0 {
			key := d.seriesID + "@" + c.slot
			claims[key] = append(claims[key], w.ID)
		}
		cands = append(cands, c)
	}

	for _, c := range cands {
		w, d := c.work, c.d
		fd := Finding{
			Subclass: c.subclass,
			Key:      w.ID,
			Works:    []WorkRef{ix.workBrief(w)},
			Notes:    []string{`title spells the series name "` + d.seriesName + `"`},
		}
		if s := ix.seriesByID[d.seriesID]; s != nil {
			fd.Series = []SeriesRef{ix.seriesRef(s)}
		}
		if c.subclass == noSeriesOnly {
			fd.Propose = Proposal{
				Op: OpAddSeriesMember, Target: w.ID, Series: d.seriesID, Field: "series",
				Advisory: true,
				Reason:   "the title names the series but states no position, so the slot needs a human",
			}
			f.add(fd)
			continue
		}
		vetoes := c.vetoes
		// Consistency as a SET: a slot two proposals both claim is a slot neither
		// may take mechanically.
		if others := claims[d.seriesID+"@"+c.slot]; len(others) > 1 {
			vetoes = append(vetoes, "slot "+c.slot+" of "+d.seriesID+" is claimed by "+
				truncateList(sortedUnique(others), 4)+", so no claimant may take it mechanically")
		}
		fd.Propose = Proposal{
			Op: OpAddSeriesMember, Target: w.ID, Series: d.seriesID, Field: "series", To: c.slot,
		}
		if len(vetoes) > 0 {
			fd.Propose.Op = OpReview
			fd.Propose.Advisory = true
			fd.Propose.Reason = "do not add this membership on this evidence: " + truncateList(vetoes, 4)
		}
		f.add(fd)
	}
	return f
}

// membershipVetoes lists the reasons a title-derived membership must not be added
// mechanically, in a fixed order.
func (ix *index) membershipVetoes(w *model.Work, d *workDerived) []string {
	var out []string

	// The position must be plausible. A number that looks like a year, or one far
	// past everything the series holds, is not a volume.
	switch lo, hi, known := ix.seriesSpan(d.seriesID); {
	case d.seq >= minImplausiblePosition:
		out = append(out, fmt.Sprintf("position %s reads as a year or a marketing number, not a volume", formatSeq(d.seq)))
	case known && (d.seq < lo-positionCeiling || d.seq > hi+positionCeiling):
		out = append(out, fmt.Sprintf("position %s is outside series %s's own span (%s-%s)",
			formatSeq(d.seq), d.seriesID, formatSeq(lo), formatSeq(hi)))
	}

	// The series reference must be corroborated: either the title states it in the
	// exact boundary shape, or one of this book's authors already has works in that
	// series. A series name is often two ordinary words.
	if !ix.seriesCorroborated(w, d) {
		out = append(out, fmt.Sprintf("the series name %q in the title is not corroborated - no author of this work has a work in %s, "+
			"and the title does not state it in the \"<Series>, Book N\" shape", d.seriesName, d.seriesID))
	}

	// A slot the series already fills is a duplicate question, not a membership one.
	if held := ix.positions[d.seriesID][formatSeq(d.seq)]; held != "" && held != w.ID {
		out = append(out, "position already held by "+held+" - resolve as a duplicate (see W-DUP) first")
	}
	return out
}

// seriesCorroborated reports whether a series name found inside a title is really
// this book's series: either an AUTHOR of the work already has a work in it, or the
// title states the reference at a boundary with its number ("<Series>, Book 4"),
// which no coincidence of words produces.
func (ix *index) seriesCorroborated(w *model.Work, d *workDerived) bool {
	if ix.authorSeriesIDs(w.Authors)[d.seriesID] {
		return true
	}
	return titlerule.StatesSeriesAndVolume(w.Title, d.seriesName)
}
