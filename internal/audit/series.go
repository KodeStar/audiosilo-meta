package audit

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// S-INTEGRITY subclasses.
const (
	sDupPosition      = "duplicate-position"
	sGap              = "position-gap"
	sMalformed        = "malformed-position"
	sDanglingWork     = "dangling-work"
	sMinorityLanguage = "minority-language"
	sOmnibusSlot      = "omnibus-in-a-slot"
	sEmpty            = "empty-series"
)

// positionShape is the schema's own position grammar (series.schema.json): a
// number, or a range spanning several entries. Spelled here so a value the schema
// somehow admitted - or a hand edit landing before validation - is still named
// rather than silently mis-sorted.
var positionShape = regexp.MustCompile(`^\d+(\.\d+)?(-\d+(\.\d+)?)?$`)

// omnibusTitle marks a title that announces itself as a collection of several
// volumes. A work like this sitting on a PLAIN integer position is the suspicious
// shape: the model spells an omnibus as a range ("1-3"), and a range beside its
// individual members is the concept working correctly, not a defect.
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
		fd.Action = "delete the series or add its members; a series with no works is unreachable"
		f.add(fd)
		return
	}

	// Duplicate positions. metacheck already refuses these, so a hit here means
	// the tree changed under the audit or the rule regressed - report either way.
	bySlot := map[string][]string{}
	var slots []string
	for _, sw := range s.Works {
		if _, seen := bySlot[sw.Position]; !seen {
			slots = append(slots, sw.Position)
		}
		bySlot[sw.Position] = append(bySlot[sw.Position], sw.Work)
	}
	sort.Strings(slots)
	for _, slot := range slots {
		if len(bySlot[slot]) < 2 {
			continue
		}
		fd := base(sDupPosition)
		fd.Key = s.ID + "@" + slot
		fd.Series = []SeriesRef{ref}
		fd.Field = "position"
		fd.Have = slot
		fd.Works = ix.worksBrief(bySlot[slot])
		fd.Action = "give each work its own position; metacheck refuses a shared one"
		f.add(fd)
	}

	// Malformed positions and dangling work references.
	for _, sw := range sortedMembers(s.Works) {
		if !positionShape.MatchString(sw.Position) {
			fd := base(sMalformed)
			fd.Key = s.ID + "@" + sw.Work
			fd.Field = "position"
			fd.Have = sw.Position
			fd.Works = ix.worksBrief([]string{sw.Work})
			fd.Action = `restate the position as a number ("2", "2.5") or a range ("1-3.5")`
			f.add(fd)
			continue
		}
		if lo, hi, ok := rangeBounds(sw.Position); ok && lo >= hi {
			fd := base(sMalformed)
			fd.Key = s.ID + "@" + sw.Work
			fd.Field = "position"
			fd.Have = sw.Position
			fd.Works = ix.worksBrief([]string{sw.Work})
			if lo == hi {
				// The shape a season/episode numbering takes when it is written as
				// a decimal: "2.1-2.10" reads as 2.1 to 2.1, because .10 and .1 are
				// the same number. The range spans nothing, so it orders nothing.
				fd.Action = "restate the position: its two bounds are the same NUMBER, so the range spans one slot and orders nothing " +
					`(a decimal is not a two-part numbering - "2.10" is "2.1")`
			} else {
				fd.Action = "restate the range low-to-high"
			}
			f.add(fd)
		}
		if ix.workByID[sw.Work] == nil {
			fd := base(sDanglingWork)
			fd.Key = s.ID + "@" + sw.Work
			fd.Field = "work"
			fd.Have = sw.Work
			fd.Action = "remove the membership or restore the work it names"
			f.add(fd)
		}
	}

	// Integer-sequence gaps, advisory: a real series is often missing volumes
	// simply because nobody has added them yet.
	if missing, top := integerGaps(s); len(missing) > 0 {
		fd := base(sGap)
		fd.Action = "advisory only: check whether the missing volumes exist and are unmodeled, or simply are not in the catalogue"
		fd.Notes = []string{fmt.Sprintf("highest integer position %d; missing %s: %s",
			top, joinCount(len(missing), "position"), truncateList(missing, 25))}
		f.add(fd)
	}

	// A member whose work is in another language than the series' majority.
	if maj := ref.Language; maj != "" {
		var minority []string
		for _, sw := range sortedMembers(s.Works) {
			w := ix.workByID[sw.Work]
			if w == nil || w.Language == "" || w.Language == maj {
				continue
			}
			minority = append(minority, sw.Work)
		}
		if len(minority) > 0 {
			fd := base(sMinorityLanguage)
			fd.Field = "language"
			fd.Have = strings.Join(languagesOf(ix, minority), ",")
			fd.Want = maj
			fd.Works = ix.worksBrief(minority)
			fd.Action = "move the translated volumes to their own series, or correct the work language"
			f.add(fd)
		}
	}

	// A work announcing itself as an omnibus while sitting on a single slot.
	for _, sw := range sortedMembers(s.Works) {
		w := ix.workByID[sw.Work]
		if w == nil || !omnibusTitle.MatchString(w.Title) {
			continue
		}
		if _, _, isRange := rangeBounds(sw.Position); isRange {
			continue // a range IS how the model spells an omnibus
		}
		fd := base(sOmnibusSlot)
		fd.Key = s.ID + "@" + sw.Work
		fd.Field = "position"
		fd.Have = sw.Position
		fd.Works = ix.worksBrief([]string{sw.Work})
		fd.Action = `restate the position as the range the collection spans ("1-3"), which frees the single slot for the individual volume`
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

// rangeBounds parses an omnibus range position. isRange is false for a plain
// number, which is not a range rather than a malformed one.
func rangeBounds(pos string) (lo, hi float64, isRange bool) {
	i := strings.Index(pos, "-")
	if i <= 0 {
		return 0, 0, false
	}
	l, errLo := strconv.ParseFloat(pos[:i], 64)
	h, errHi := strconv.ParseFloat(pos[i+1:], 64)
	if errLo != nil || errHi != nil {
		return 0, 0, false
	}
	return l, h, true
}

// integerGaps returns the missing integer positions of a series, and the highest
// one it holds. Only PLAIN integers count on either side: a novella at 2.5 does
// not fill slot 3, and an omnibus range is deliberately not read as filling the
// slots it spans - a gap report is advisory and over-reporting a covered slot
// would be the noisier error.
func integerGaps(s *model.Series) (missing []string, top int) {
	have := map[int]bool{}
	for _, sw := range s.Works {
		n, err := strconv.Atoi(sw.Position)
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

// worksBrief cites a list of work ids, silently skipping the ones the catalogue
// does not hold (a dangling reference is its own finding).
func (ix *index) worksBrief(ids []string) []WorkRef {
	var out []WorkRef
	for _, id := range sortedUnique(ids) {
		if w := ix.workByID[id]; w != nil {
			out = append(out, ix.workBrief(w))
		}
	}
	return out
}
