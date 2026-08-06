package remediate

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// series.go repairs the series entries the part works sit in.
//
// Two kinds of series reference them. A DRAMATIZED series (its name carries the
// edition marker) parks the parts at decimal positions - "1.1".."1.5" is one
// book in five parts - so the repair is to collapse them onto the book's
// integer position. A PLAIN series has had its numeric slots taken by the
// parts, so the repair is to give the slot back to the plain text edition when
// the catalogue holds one, and to leave the merged dramatization in it when it
// does not.
//
// Both are the same operation over one rewrite map, which is why they are one
// function: rewrite the work reference, collapse what the parts occupied onto
// one position, and refuse anything that would put two works on one position.
//
// The position GRAMMAR is not restated here. Reading and canonicalizing a
// position is internal/importer.NormalizeSequence's job, and this file builds
// only the floor-and-order POLICY on top of it. (internal/serve carries its own
// parser for the search-side volume boost; unifying all three into pkg/model is
// a separate change and deliberately not attempted here.)

// rewrite is the change one series entry undergoes.
type rewrite struct {
	// To is the work slug the entry now names.
	To string
	// FromPart reports that the entry named a PART work: its position is the
	// hijacked one, so it collapses rather than being kept.
	FromPart bool
	// Group names the part group the entry's work belonged to, so a series this
	// run has to refuse can refuse that group with it.
	Group *group
}

// seriesPlan is one series' repaired membership list.
type seriesPlan struct {
	Slug    string
	Works   []model.SeriesWork
	Changes []string
}

// planSeries repairs every series that references a rewritten work. A series it
// cannot repair is left exactly as it is and the part groups it references are
// returned, so the caller can refuse them and re-plan - a book whose series
// cannot be fixed is a book that must not be merged, or its parts would dangle.
//
// It visits only the series a rewrite actually reaches (idx.seriesOf), not all
// 30,799: the retry loop can run several rounds, and every one of them would
// otherwise re-read the whole family to touch a few dozen entries.
func planSeries(idx *index, rewrites map[string]rewrite, swaps map[string]string) (plans []seriesPlan, refusals []Refusal, blocked map[*group]bool) {
	blocked = map[*group]bool{}
	affected := map[string]bool{}
	for work := range rewrites {
		for _, s := range idx.seriesOf[work] {
			affected[s] = true
		}
	}
	for work := range swaps {
		for _, s := range idx.seriesOf[work] {
			affected[s] = true
		}
	}
	for _, slug := range sortedKeys(affected) {
		plan, r, ok := planOneSeries(slug, idx.series[slug], rewrites, swaps)
		if !ok {
			refusals = append(refusals, r)
			for _, g := range groupsIn(idx.series[slug], rewrites) {
				blocked[g] = true
			}
			continue
		}
		if plan != nil {
			plans = append(plans, *plan)
		}
	}
	return plans, refusals, blocked
}

// groupsIn names the part groups a series references.
func groupsIn(s obj, rewrites map[string]rewrite) []*group {
	var out []*group
	seen := map[*group]bool{}
	entries, err := seriesWorks(s)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if rw, ok := rewrites[e.Work]; ok && rw.Group != nil && !seen[rw.Group] {
			seen[rw.Group] = true
			out = append(out, rw.Group)
		}
	}
	return out
}

// seriesWorks decodes a series entry's membership list.
func seriesWorks(s obj) ([]model.SeriesWork, error) {
	var out []model.SeriesWork
	if err := json.Unmarshal(s["works"], &out); err != nil {
		return nil, err
	}
	return out, nil
}

// entryPlan is one membership entry while the series is being replanned.
type entryPlan struct {
	work     string
	position string
	fromPart bool
	original string
}

// planOneSeries repairs a single series. A nil plan with ok true means the
// series needs no change.
func planOneSeries(slug string, s obj, rewrites map[string]rewrite, swaps map[string]string) (*seriesPlan, Refusal, bool) {
	entries, err := seriesWorks(s)
	if err != nil {
		return nil, Refusal{Category: catSeriesPosition, Subject: slug,
			Reason: "the series' works list could not be read: " + err.Error()}, false
	}
	plain := !dramatizedSeries(s.str("name"))

	planned := make([]entryPlan, 0, len(entries))
	touched := false
	for _, e := range entries {
		ep := entryPlan{work: e.Work, position: e.Position, original: e.Work}
		if rw, ok := rewrites[e.Work]; ok {
			ep.work, ep.fromPart = rw.To, rw.FromPart
			touched = true
		}
		// A plain series' numeric slot names the plain text edition. The swap
		// is applied to the entry's CURRENT target, so it reaches both the slot
		// a part just vacated and the slot the complete-set work already held.
		if plain {
			if twin, ok := swaps[ep.work]; ok && twin != ep.work {
				ep.work = twin
				touched = true
			}
		}
		planned = append(planned, ep)
	}
	if !touched {
		return nil, Refusal{}, true
	}

	final, changes, r, ok := collapse(slug, planned)
	if !ok {
		return nil, r, false
	}
	if len(final) == 0 {
		return nil, Refusal{Category: catSeriesCollision, Subject: slug,
			Reason: "the repair would empty the series"}, false
	}
	return &seriesPlan{Slug: slug, Works: final, Changes: changes}, Refusal{}, true
}

// collapse folds the entries that now name one work onto a single position and
// refuses anything that would leave two works sharing one.
func collapse(seriesSlug string, planned []entryPlan) ([]model.SeriesWork, []string, Refusal, bool) {
	byWork := map[string][]entryPlan{}
	order := []string{}
	for _, ep := range planned {
		if _, ok := byWork[ep.work]; !ok {
			order = append(order, ep.work)
		}
		byWork[ep.work] = append(byWork[ep.work], ep)
	}

	var out []model.SeriesWork
	var changes []string
	for _, work := range order {
		entries := byWork[work]
		var kept, parts []entryPlan
		for _, ep := range entries {
			if ep.fromPart {
				parts = append(parts, ep)
			} else {
				kept = append(kept, ep)
			}
		}
		switch {
		case len(kept) > 0:
			// A slot this run did not derive from a part records where the book
			// actually sits, so it wins; the hijacked slots go away.
			slices.SortFunc(kept, func(a, b entryPlan) int { return comparePositions(a.position, b.position) })
			out = append(out, model.SeriesWork{Work: work, Position: kept[0].position})
			for _, ep := range kept[1:] {
				changes = append(changes, fmt.Sprintf("dropped duplicate %s at position %s", work, ep.position))
			}
			for _, ep := range parts {
				changes = append(changes, fmt.Sprintf("dropped %s at position %s (%s already sits at %s)",
					ep.original, ep.position, work, kept[0].position))
			}
		default:
			pos, ok := collapsedPosition(parts)
			if !ok {
				positions := make([]string, 0, len(parts))
				for _, ep := range parts {
					positions = append(positions, ep.position)
				}
				return nil, nil, Refusal{Category: catSeriesPosition, Subject: seriesSlug,
					Reason:  fmt.Sprintf("the positions %s the parts of %s hold are not plain numbers", strings.Join(positions, ", "), work),
					Entries: []string{work}}, false
			}
			out = append(out, model.SeriesWork{Work: work, Position: pos})
			for _, ep := range parts {
				if ep.original != work || ep.position != pos {
					changes = append(changes, fmt.Sprintf("%s at %s -> %s at %s", ep.original, ep.position, work, pos))
				}
			}
		}
	}

	seen := map[string]string{}
	for _, sw := range out {
		if prev, ok := seen[sw.Position]; ok {
			return nil, nil, Refusal{Category: catSeriesCollision, Subject: seriesSlug,
				Reason:  fmt.Sprintf("position %s would be held by both %s and %s", sw.Position, prev, sw.Work),
				Entries: []string{prev, sw.Work}}, false
		}
		seen[sw.Position] = sw.Work
	}
	slices.SortFunc(out, func(a, b model.SeriesWork) int { return comparePositions(a.Position, b.Position) })
	return out, changes, Refusal{}, true
}

// collapsedPosition is the one position a book's parts fold onto: the lowest
// integer any of them sits at. A decimal group ("1.1".."1.5") is the book's
// position with the part number after the point, and a plain series that gave
// consecutive integers to consecutive parts ("Brush Country" at 1 and 2) still
// names one book - so the floor of the lowest is the answer in both shapes.
//
// The result goes back through the canonicalizer that read the inputs, so what
// is written is a position by the same definition as what was read.
func collapsedPosition(parts []entryPlan) (string, bool) {
	best := -1
	for _, ep := range parts {
		n, ok := floorPosition(ep.position)
		if !ok {
			return "", false
		}
		if best < 0 || n < best {
			best = n
		}
	}
	if best < 0 {
		return "", false
	}
	return importer.NormalizeSequence(strconv.Itoa(best))
}

// floorPosition reads a plain numeric position and returns its integer part.
// ok is false for the position forms that are not one number - an omnibus range
// ("1-3.5") names several books at once and has no single floor - and for
// anything NormalizeSequence does not recognize as a position at all.
func floorPosition(p string) (int, bool) {
	pos, ok := importer.NormalizeSequence(p)
	if !ok || strings.Contains(pos, "-") {
		return 0, false
	}
	whole, _, _ := strings.Cut(pos, ".")
	n, err := strconv.Atoi(whole)
	if err != nil {
		return 0, false
	}
	return n, true
}

// comparePositions orders two positions the way a reader expects: numerically
// when both are numbers, lexicographically otherwise (an omnibus range sorts by
// its text, which is stable and is all the ordering it needs). Ties break on the
// CANONICAL spelling, so two spellings of one position ("2.5" and "2.50") come
// out equal rather than in whichever order their raw bytes fall.
func comparePositions(a, b string) int {
	na, oka := positionValue(a)
	nb, okb := positionValue(b)
	switch {
	case oka && okb:
		if na != nb {
			if na < nb {
				return -1
			}
			return 1
		}
		return strings.Compare(canonicalPosition(a), canonicalPosition(b))
	case oka:
		return -1
	case okb:
		return 1
	default:
		return strings.Compare(a, b)
	}
}

// canonicalPosition is a position in the one spelling the canonicalizer writes,
// or the raw value when it is not a position at all.
func canonicalPosition(p string) string {
	if pos, ok := importer.NormalizeSequence(p); ok {
		return pos
	}
	return p
}

// positionValue reads a plain numeric position as a float for ordering.
func positionValue(p string) (float64, bool) {
	pos, ok := importer.NormalizeSequence(p)
	if !ok || strings.Contains(pos, "-") {
		return 0, false
	}
	v, err := strconv.ParseFloat(pos, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
