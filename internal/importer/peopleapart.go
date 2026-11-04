package importer

import (
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
// The series grouping asks it through personGroups, which is what keeps a chain of
// spellings or a spelling of both people from joining them by way of itself.
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
// because the grouping is transitive: a chain of spellings, or a co-author of both,
// would make the two reviewed people one group. A spelling of BOTH takes no
// spelling union at all (reviewedBridges).
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
func newPersonGroups(n int) personGroups { return personGroups{set: unionfind.New(n)} }

// groupPeople groups forms, by their index, into people: every spelling of one
// person (personForm.same) is one group, a spelling matching two groups bridging
// them - never a pair a review kept apart, nor through a spelling of both.
func groupPeople(forms []personForm) personGroups {
	g := newPersonGroups(len(forms))
	for i, f := range forms {
		g.hold(i, f)
	}
	var bridges map[string]bool
	if len(g.held) > 0 {
		bridges = reviewedBridges(forms)
	}
	for i, f := range forms {
		if bridges[f.slug] {
			continue
		}
		for j := range i {
			if !bridges[forms[j].slug] && forms[j].same(f) {
				g.union(j, i)
			}
		}
	}
	return g
}

// reviewedBridges is the slugs of forms that reach BOTH people of a reviewed pair
// over the rungs ("A. George", with both Alex George and Dr. Alex George among the
// forms). Nothing says which of the two such a spelling is, and an order is no
// evidence (`dr-` sorts first, so slug order would hand "Robert E. Lee" to Mrs.
// Robert Lee), so it is a person of its own: its rows are judged on their own, and
// join whichever of the two a series already belongs to.
func reviewedBridges(forms []personForm) map[string]bool {
	out := map[string]bool{}
	for i, a := range forms {
		if !a.apart.names(a.slug) {
			continue
		}
		for _, b := range forms[i+1:] {
			if !a.apart.Has(a.slug, b.slug) {
				continue
			}
			for _, k := range forms {
				// The pair themselves reach each other over the rungs, so they are
				// asked out by slug.
				if k.slug != a.slug && k.slug != b.slug && k.rungs(a) && k.rungs(b) {
					out[k.slug] = true
				}
			}
		}
	}
	return out
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
		g.held[r] = append(g.held[r], f.slug)
	}
}

// union joins a's and b's groups, unless a review keeps a person of one apart
// from a person of the other.
func (g *personGroups) union(a, b int) {
	ra, rb := g.find(a), g.find(b)
	if ra == rb {
		return
	}
	for _, x := range g.held[ra] {
		if g.keptFrom(rb, x) {
			return
		}
	}
	joined := slices.Concat(g.held[ra], g.held[rb])
	g.set.Union(a, b)
	if len(joined) > 0 {
		slices.Sort(joined)
		delete(g.held, max(ra, rb))
		g.held[min(ra, rb)] = slices.Compact(joined) // rows of one person hold them each
	}
}

// keptFrom reports whether the review keeps person slug apart from anyone in i's
// group. It asks the group's review, so a row built without one keeps the veto.
func (g *personGroups) keptFrom(i int, slug string) bool {
	return slices.ContainsFunc(g.held[g.find(i)], func(x string) bool { return g.apart.Has(x, slug) })
}
