package audit

import (
	"github.com/kodestar/audiosilo-meta/internal/titlerule"
)

// detectWorkTitle reports every work whose title carries retailer decoration AND
// for which the audit can propose a cleaner one.
//
// A title-only class: it never proposes a slug change, because a slug is IDENTITY
// and moving one is a rename with its own referential consequences (every series
// membership and every sidecar that names it). The slug side of the same records is
// F-HYGIENE's, where it is flagged as a rename CANDIDATE and nothing more.
func detectWorkTitle(ix *index) *findings {
	f := &findings{class: ClassWorkTitle}
	for _, w := range ix.cat.Works {
		d := ix.derived(w)
		if len(d.markers) == 0 || d.want == w.Title || d.want == "" {
			continue
		}
		// A proposal that reduces a title to PACKAGING names no book, and a wrong
		// retitle is worse than none: "The Legend of Dave the Villager, Books 16-20"
		// cleans to "Books 16-20" once the series name comes off, and "Seeds of
		// Chaos Omnibus" to "Omnibus". Those residuals are still the right W-DUP
		// KEY (which is why Clean produces them) but they are not a title, so the
		// record is withheld here rather than proposing nonsense.
		if !titlerule.CarriesIdentity(d.want) && titlerule.CarriesIdentity(w.Title) {
			continue
		}
		sub := titlerule.PrimaryDecoration(d.markers)
		if sub == "" {
			continue
		}
		f.add(Finding{
			Subclass: sub,
			Key:      w.ID,
			Works:    []WorkRef{ix.workBrief(w)},
			Markers:  d.markers,
			Propose: Proposal{
				Op:     OpRetitle,
				Target: w.ID,
				Field:  "title",
				From:   w.Title,
				To:     d.want,
				Reason: "the slug is identity and is deliberately left alone; see F-HYGIENE for the rename candidates",
			},
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

// detectWorkNoSeries reports works that belong to no series but whose title names
// one, states a volume number, or both - the shape a bulk retailer import leaves
// when the series column was empty and the series was only ever in the title.
func detectWorkNoSeries(ix *index) *findings {
	f := &findings{class: ClassWorkNoSeries}
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
						Op:       OpReview,
						Target:   w.ID,
						Field:    "series",
						To:       formatSeq(d.seq),
						Advisory: true,
						Reason:   "the title states a volume number but no series in the catalogue matches it - identify the series first",
					},
				})
			}
			continue
		}
		fd := Finding{
			Key:   w.ID,
			Works: []WorkRef{ix.workBrief(w)},
			Notes: []string{`title spells the series name "` + d.seriesName + `"`},
		}
		if s := ix.seriesByID[d.seriesID]; s != nil {
			fd.Series = []SeriesRef{ix.seriesRef(s)}
		}
		switch {
		case d.hasSeq:
			slot := formatSeq(d.seq)
			fd.Subclass = noSeriesAndPosition
			fd.Propose = Proposal{
				Op:     OpAddSeriesMember,
				Target: w.ID,
				Series: d.seriesID,
				Field:  "series",
				To:     slot,
			}
			if held := ix.positions[d.seriesID][slot]; held != "" && held != w.ID {
				fd.Propose.Op = OpReview
				fd.Propose.Advisory = true
				fd.Propose.Reason = "position already held by " + held +
					" - resolve as a duplicate (see W-DUP) before adding a membership"
			}
		default:
			fd.Subclass = noSeriesOnly
			fd.Propose = Proposal{
				Op:       OpAddSeriesMember,
				Target:   w.ID,
				Series:   d.seriesID,
				Field:    "series",
				Advisory: true,
				Reason:   "the title names the series but states no position, so the slot needs a human",
			}
		}
		f.add(fd)
	}
	return f
}
