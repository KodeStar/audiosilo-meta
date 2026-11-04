package importer

import (
	"maps"
	"slices"

	"github.com/kodestar/audiosilo-meta/internal/unionfind"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// PeopleApart is the people half of the reviewed "not one record" decisions
// (Options.Distinct): every pair of person slugs a merge-people rejection in
// internal/audit/reviewed.json recorded as two people, resolved through the
// tombstone table with check.ResolveDistinct - the census's own resolution, so
// the importer, the intake form and metacheck agree on which pairs a decision
// names.
//
// It is read wherever a rule would otherwise make the two ONE person: the
// honorific and credential folds (creditCensus.keptApart), the LibriVox rule 1
// paths, and the series same-person rule (personForm.same), which is what the
// series-author fit and internal/issueform's work-slug and takeover gates
// decide by. "Dr. Alex George" the TV doctor and Alex George the novelist strip
// to one name, and without the review the doctor's book joins the novelist's
// series - the series-hijack pattern, arriving through a rule meant to keep one
// author's spellings together.
//
// The series grouping asks it through personGroups, which is what keeps a spelling
// of both people from joining them by way of itself.
//
// A nil *PeopleApart keeps nothing apart, so a caller with no decisions (a
// test, a probe) is unchanged.
type PeopleApart struct {
	pairs check.DistinctPairs
	reds  model.Redirects
	// named is every live slug some pair names, the cheap first question the
	// grouping asks of every credit (names).
	named map[string]bool
}

// NewPeopleApart resolves ds's people decisions through reds, or returns nil
// when none names a pair.
func NewPeopleApart(ds []check.ReviewedDistinct, reds model.Redirects) *PeopleApart {
	pairs := check.ResolveDistinct(ds, reds)
	if len(pairs[model.RedirectPeople]) == 0 {
		return nil
	}
	named := map[string]bool{}
	for pair := range pairs[model.RedirectPeople] {
		named[pair[0]], named[pair[1]] = true, true
	}
	return &PeopleApart{pairs: pairs, reds: reds, named: named}
}

// live is the slug a decision names for person slug s: its survivor when the
// tombstone table retires it, as the decisions were resolved.
func (pa *PeopleApart) live(s string) string { return pa.reds.Live(model.RedirectPeople, s) }

// names reports whether a review names person slug s in any pair.
func (pa *PeopleApart) names(s string) bool {
	return pa != nil && s != "" && pa.named[pa.live(s)]
}

// Has reports whether a review recorded the person slugs a and b as two
// people. A retired slug is asked as its survivor, as the decisions were.
func (pa *PeopleApart) Has(a, b string) bool {
	if pa == nil || a == "" || b == "" {
		return false
	}
	return pa.pairs.Has(model.RedirectPeople, pa.live(a), pa.live(b))
}

// SamePerson is the series same-person rule (personForm.same) over two credited
// names, each with the slug it is recorded or would be minted under: whether
// the two may be one person. A pair a review kept apart never is. It is
// exported for internal/issueform's work-slug and takeover gates, which must
// decide as the importer does whether a credit is the recorded author - one
// rule, so the two doors never split one author's books or join two authors'.
// A nil receiver applies no review.
func (pa *PeopleApart) SamePerson(slugA, nameA, slugB, nameB string) bool {
	return pa.form(slugA, nameA).same(pa.form(slugB, nameB))
}

// NearPerson reports whether two names that are NOT one person by SamePerson
// are still close enough that a reader cannot tell a misspelling from a
// different author (nearPerson) - never a pair a review kept apart, which a
// maintainer has already told apart ("Dr. Alex George" is not Alex George). It
// is SamePerson's other door: internal/issueform's work-slug gate asks it to
// decide when a taken title slug is safe to step past rather than a question
// for a maintainer. A nil receiver applies no review.
func (pa *PeopleApart) NearPerson(slugA, nameA, slugB, nameB string) bool {
	return !pa.Has(slugA, slugB) && nearPerson(nameA, nameB)
}

// form is formOf carrying this review.
func (pa *PeopleApart) form(slug, name string) personForm {
	f := formOf(slug, name)
	f.apart = pa
	return f
}

// personGroups groups indexed items - a series' credited spellings, a batch's
// claims - into people with a union-find (internal/unionfind), under the review's
// NEVER-JOIN constraints: a union that would put a pair a review kept apart in
// one group is refused. The pairwise veto (personForm.same) cannot do this alone,
// because the grouping is transitive: "A. George" is a spelling of Alex George
// and of Dr. Alex George alike, no review names it, and without the constraint
// it makes the two reviewed people one group by way of itself. With it, such a
// spelling in a series' evidence joins the first of the two it meets and stays out
// of the other (a batch's clusters do better, reviewedBridges).
//
// Only the items a review names are recorded (hold), so a grouping that meets no
// reviewed person pays one lookup per credit and holds nothing.
type personGroups struct {
	set unionfind.Set
	// held is the reviewed person slugs each component holds, by its root.
	held map[int][]string
	// apart is the review the held forms came under: one run, one review.
	apart *PeopleApart
}

// newPersonGroups is n singleton items.
func newPersonGroups(n int) *personGroups { return &personGroups{set: unionfind.New(n)} }

// add appends a new singleton item and returns its index.
func (g *personGroups) add() int {
	i := len(g.set)
	g.set = append(g.set, i)
	return i
}

// find is the root of i's group: its lowest index.
func (g *personGroups) find(i int) int { return g.set.Find(i) }

// hold records that item i credits person f. Call it before any union of i that
// must see f.
func (g *personGroups) hold(i int, f personForm) {
	if !f.apart.names(f.slug) {
		return
	}
	g.apart = f.apart
	if g.held == nil {
		g.held = map[int][]string{}
	}
	r := g.find(i)
	if !slices.Contains(g.held[r], f.slug) {
		g.held[r] = append(slices.Clip(g.held[r]), f.slug) // Clip: a clone shares the backing array
	}
}

// union joins a's and b's groups, unless a review keeps a person of one apart
// from a person of the other; it reports whether they are now one group.
func (g *personGroups) union(a, b int) bool {
	ra, rb := g.find(a), g.find(b)
	if ra == rb {
		return true
	}
	hb := g.held[rb]
	for _, x := range g.held[ra] {
		if slices.ContainsFunc(hb, func(y string) bool { return g.apart.Has(x, y) }) {
			return false
		}
	}
	joined := slices.Concat(g.held[ra], hb)
	g.set.Union(a, b)
	if len(joined) > 0 {
		delete(g.held, ra)
		delete(g.held, rb)
		g.held[g.find(a)] = joined
	}
	return true
}

// keptFrom reports whether a review keeps person f apart from anyone in i's group.
func (g *personGroups) keptFrom(i int, f personForm) bool {
	if len(g.held) == 0 || !f.apart.names(f.slug) {
		return false
	}
	return slices.ContainsFunc(g.held[g.find(i)], func(x string) bool { return f.apart.Has(x, f.slug) })
}

// clone is an independent copy.
func (g *personGroups) clone() *personGroups {
	return &personGroups{set: slices.Clone(g.set), held: maps.Clone(g.held), apart: g.apart}
}
