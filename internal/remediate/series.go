package remediate

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

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
func planSeries(idx *index, rewrites map[string]rewrite, swaps map[string]string) (plans []seriesPlan, refusals []Refusal, blocked map[*group]bool) {
	blocked = map[*group]bool{}
	for _, slug := range sortedKeys(idx.series) {
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
	changed  bool
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
			ep.work, ep.fromPart, ep.changed = rw.To, rw.FromPart, true
			touched = true
		}
		// A plain series' numeric slot names the plain text edition. The swap
		// is applied to the entry's CURRENT target, so it reaches both the slot
		// a part just vacated and the slot the complete-set work already held.
		if plain {
			if twin, ok := swaps[ep.work]; ok && twin != ep.work {
				ep.work = twin
				ep.changed = true
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
		group := byWork[work]
		var kept []entryPlan
		var parts []entryPlan
		for _, ep := range group {
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
			sort.Slice(kept, func(i, j int) bool { return positionLess(kept[i].position, kept[j].position) })
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
					Reason:  fmt.Sprintf("the positions %s the parts of %s hold are not plain numbers", joinComma(positions), work),
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
	sort.Slice(out, func(i, j int) bool { return positionLess(out[i].Position, out[j].Position) })
	return out, changes, Refusal{}, true
}

// collapsedPosition is the one position a book's parts fold onto: the lowest
// integer any of them sits at. A decimal group ("1.1".."1.5") is the book's
// position with the part number after the point, and a plain series that gave
// consecutive integers to consecutive parts ("Brush Country" at 1 and 2) still
// names one book - so the floor of the lowest is the answer in both shapes.
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
	return strconv.Itoa(best), true
}

// floorPosition reads a plain numeric position and returns its integer part.
// ok is false for the position forms that are not one number - an omnibus range
// ("1-3.5") names several books at once and has no single floor.
func floorPosition(p string) (int, bool) {
	whole := p
	if i := strings.IndexByte(p, '.'); i >= 0 {
		whole = p[:i]
		if !allDigits(p[i+1:]) || p[i+1:] == "" {
			return 0, false
		}
	}
	if !allDigits(whole) || whole == "" {
		return 0, false
	}
	n, err := strconv.Atoi(whole)
	if err != nil {
		return 0, false
	}
	return n, true
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
}

// positionLess orders two positions the way a reader expects: numerically when
// both are numbers, lexicographically otherwise (an omnibus range sorts by its
// text, which is stable and is all the ordering it needs).
func positionLess(a, b string) bool {
	na, oka := parsePositionValue(a)
	nb, okb := parsePositionValue(b)
	switch {
	case oka && okb:
		if na != nb {
			return na < nb
		}
		return a < b
	case oka:
		return true
	case okb:
		return false
	default:
		return a < b
	}
}

// parsePositionValue reads a plain numeric position as a float for ordering.
func parsePositionValue(p string) (float64, bool) {
	if _, ok := floorPosition(p); !ok {
		return 0, false
	}
	v, err := strconv.ParseFloat(p, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
