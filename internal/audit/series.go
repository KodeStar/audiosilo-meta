package audit

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// S-INTEGRITY subclasses.
const (
	sDupPosition      = "duplicate-position"
	sGap              = "position-gap"
	sMalformed        = "malformed-position"
	sNonCanonical     = "non-canonical-position"
	sDanglingWork     = "dangling-work"
	sMinorityLanguage = "minority-language"
	sOmnibusSlot      = "omnibus-in-a-slot"
	sEmpty            = "empty-series"
)

// omnibusTitle marks a title that announces itself as a collection of several
// volumes. A work like this sitting on a PLAIN position is the suspicious shape:
// the model spells an omnibus as a range ("1-3"), and a range beside its individual
// members is the concept working correctly, not a defect.
var omnibusTitle = regexp.MustCompile(`(?i)\b(?:omnibus|box\s*set|boxed\s*set|complete\s+(?:series|collection|trilogy)|books?\s+\d+\s*-\s*\d+)\b`)

// maxGapCeiling bounds the gap report: a series whose highest integer position is
// absurd (a retailer's catalogue number landing in a position column) would
// otherwise generate thousands of "missing" volumes.
const maxGapCeiling = 300

// detectSeriesIntegrity checks each series' own consistency.
func detectSeriesIntegrity(ix *index) *findings {
	f := &findings{class: ClassSeriesInteg}
	for _, s := range ix.cat.Series {
		checkOneSeries(ix, f, s)
	}
	return f
}

func checkOneSeries(ix *index, f *findings, s *model.Series) {
	ref := ix.seriesRef(s)
	base := func(sub string) Finding {
		return Finding{Subclass: sub, Key: s.ID, Series: []SeriesRef{ref}}
	}
	if len(s.Works) == 0 {
		fd := base(sEmpty)
		fd.Propose = Proposal{
			Op: OpReview, Target: s.ID, Advisory: true,
			Reason: "a series with no works is unreachable: delete it, or add its members",
		}
		f.add(fd)
		return
	}

	// Duplicate positions, over the RAW position string. metacheck already refuses
	// these, so a hit here means the tree changed under the audit or the rule
	// regressed - report either way.
	bySlot, slots := groupBy(s.Works, func(sw model.SeriesWork) string { return sw.Position })
	for _, slot := range slots {
		if len(bySlot[slot]) < 2 {
			continue
		}
		ids := make([]string, 0, len(bySlot[slot]))
		for _, sw := range bySlot[slot] {
			ids = append(ids, sw.Work)
		}
		fd := base(sDupPosition)
		fd.Key = s.ID + "@" + slot
		fd.Works = ix.worksBrief(ids)
		fd.Propose = Proposal{
			Op: OpRestatePosition, Series: s.ID, Field: "position", From: slot,
			Others: sortedUnique(ids),
			Reason: "metacheck refuses a shared position: give each work its own",
		}
		f.add(fd)
	}

	// ONE pass over the members for the three per-member checks. They were three
	// loops over three sortedMembers calls, which is three sorts of the same slice.
	majority := ref.Language
	var minority []string
	for _, sw := range sortedMembers(s.Works) {
		member := func(sub string) Finding {
			fd := base(sub)
			fd.Key = s.ID + "@" + sw.Work
			fd.Works = ix.worksBrief([]string{sw.Work})
			return fd
		}

		// Position grammar: acceptance and canonical spelling through the rule of
		// record (importer.NormalizeSequence), which tolerates the "1 - 3" range
		// spellings the schema pattern rejects - 104 of them are real.
		if norm, grammatical := importer.NormalizeSequence(sw.Position); !grammatical {
			fd := member(sMalformed)
			fd.Propose = Proposal{
				Op: OpRestatePosition, Target: sw.Work, Series: s.ID, Field: "position", From: sw.Position,
				Advisory: true,
				Reason:   `restate it as a number ("2", "2.5") or a range ("1-3.5")`,
			}
			f.add(fd)
		} else {
			// Grammatical but not the canonical spelling: a position is a STRING,
			// so two spellings of one number are two different positions to every
			// rule that compares them.
			//
			// On a RANGE the canonical spelling is not safe to propose. The
			// normalization trims fractional zeros, which is right for a scalar
			// ("0.50" is 0.5) and DESTRUCTIVE across a range: "2.1-2.10" becomes
			// "2.1-2.1", collapsing a ten-episode span to one slot. Such a position
			// is the known season/episode modeling ambiguity - S-INTEGRITY already
			// reports it as malformed-position below - so here it is a review with
			// no rewrite attached.
			if norm != sw.Position {
				fd := member(sNonCanonical)
				fd.Propose = Proposal{
					Op: OpRestatePosition, Target: sw.Work, Series: s.ID, Field: "position",
					From: sw.Position, To: norm,
					Reason: "a position is a string, so a non-canonical spelling is a different position to every rule that compares them",
				}
				if strings.Contains(norm, "-") {
					fd.Propose.Op = OpReview
					fd.Propose.To = ""
					fd.Propose.Advisory = true
					fd.Propose.Reason = "a RANGE cannot be recanonicalized mechanically: trimming fractional zeros would turn " +
						`"2.1-2.10" into "2.1-2.1" and collapse the span. Decide what the two bounds mean first`
				}
				f.add(fd)
			}
			if lo, hi, isRange := importer.PositionRange(sw.Position); isRange && lo >= hi {
				fd := member(sMalformed)
				fd.Propose = Proposal{
					Op: OpRestatePosition, Target: sw.Work, Series: s.ID, Field: "position", From: sw.Position,
					Advisory: true,
				}
				if lo == hi {
					// The shape a season/episode numbering takes when it is written
					// as a decimal: "2.1-2.10" reads as 2.1 to 2.1, because .10 and
					// .1 are the same number. The range spans nothing.
					fd.Propose.Reason = "its two bounds are the same NUMBER, so the range spans one slot and orders nothing " +
						`(a decimal is not a two-part numbering - "2.10" is "2.1")`
				} else {
					fd.Propose.Reason = "restate the range low-to-high"
				}
				f.add(fd)
			}
		}

		w := ix.workByID[sw.Work]
		if w == nil {
			fd := member(sDanglingWork)
			fd.Propose = Proposal{
				Op: OpDropMembership, Series: s.ID, Field: "work", From: sw.Work,
				Advisory: true,
				Reason:   "remove the membership, or restore the work it names",
			}
			f.add(fd)
			continue
		}

		if majority != "" && w.Language != "" && w.Language != majority {
			minority = append(minority, sw.Work)
		}

		// A work announcing itself as an omnibus while sitting on a single slot.
		if omnibusTitle.MatchString(w.Title) {
			if _, _, isRange := importer.PositionRange(sw.Position); !isRange {
				fd := member(sOmnibusSlot)
				fd.Propose = Proposal{
					Op: OpRestatePosition, Target: sw.Work, Series: s.ID, Field: "position", From: sw.Position,
					Advisory: true,
					Reason:   `restate it as the range the collection spans ("1-3"), which frees the single slot for the individual volume`,
				}
				f.add(fd)
			}
		}
	}

	if len(minority) > 0 {
		fd := base(sMinorityLanguage)
		fd.Works = ix.worksBrief(minority)
		fd.Propose = Proposal{
			Op: OpReview, Series: s.ID, Field: "language",
			From: strings.Join(languagesOf(ix, minority), ","), To: majority,
			Others:   sortedUnique(minority),
			Advisory: true,
			Reason:   "move the translated volumes to their own series, or correct the work language",
		}
		f.add(fd)
	}

	// Integer-sequence gaps, advisory: a real series is often missing volumes
	// simply because nobody has added them yet.
	if missing, top := integerGaps(s); len(missing) > 0 {
		fd := base(sGap)
		fd.Propose = Proposal{
			Op: OpReview, Series: s.ID, Advisory: true,
			Reason: "advisory only: check whether the missing volumes exist and are unmodeled, or simply are not in the catalogue",
		}
		fd.Notes = []string{fmt.Sprintf("highest integer position %d; missing %s: %s",
			top, joinCount(len(missing), "position"), truncateList(missing, 25))}
		f.add(fd)
	}
}

// sortedMembers returns a series' memberships in a deterministic order,
// independent of the file's own ordering.
func sortedMembers(ws []model.SeriesWork) []model.SeriesWork {
	out := make([]model.SeriesWork, len(ws))
	copy(out, ws)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Work != out[j].Work {
			return out[i].Work < out[j].Work
		}
		return out[i].Position < out[j].Position
	})
	return out
}

// integerGaps returns the missing integer positions of a series, and the highest
// one it holds. Only PLAIN integers count on either side: a novella at 2.5 does
// not fill slot 3, and an omnibus range is deliberately not read as filling the
// slots it spans - a gap report is advisory and over-reporting a covered slot
// would be the noisier error.
func integerGaps(s *model.Series) (missing []string, top int) {
	have := map[int]bool{}
	for _, sw := range s.Works {
		// The canonical spelling first, so "01" and "1 " count as 1.
		norm, ok := importer.NormalizeSequence(sw.Position)
		if !ok {
			continue
		}
		n, err := strconv.Atoi(norm)
		if err != nil || n < 1 {
			continue
		}
		have[n] = true
		if n > top {
			top = n
		}
	}
	if len(have) < 2 || top > maxGapCeiling {
		return nil, top
	}
	for n := 1; n < top; n++ {
		if !have[n] {
			missing = append(missing, strconv.Itoa(n))
		}
	}
	return missing, top
}

func languagesOf(ix *index, workIDs []string) []string {
	var out []string
	for _, id := range workIDs {
		if w := ix.workByID[id]; w != nil {
			out = append(out, w.Language)
		}
	}
	return sortedUnique(out)
}
