package remediate

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// group.go turns the part works into part GROUPS - one per book - and refuses
// the ones it cannot form an unambiguous answer for.

// part is one part product inside a group.
type part struct {
	Slug   string
	Ref    partRef
	Work   obj
	RecKey string
	Rec    obj
}

// group is one book's part set, plus everything the merge needs to decide.
type group struct {
	Base    string   // the base title, the parts' shared title minus the markers
	Authors []string // the author set, in the first part's order
	Parts   []part   // sorted by (part number, work slug)

	// Target is the complete-set work the catalogue already holds for this
	// book, empty when there is none and one has to be minted.
	Target string
	// Set is the complete-set dump row, nil when none was supplied or matched.
	Set *completeSet

	refused bool
}

// key identifies the group: the base title's article-tolerant slug and the
// author set. It is a slug rather than the title text because Audible spells
// one product's title several ways - "Against The Odds" and "Against the Odds",
// "Stone of Farewell" and "The Stone of Farewell" - and two spellings of one
// book must not become two books.
func (g *group) key() string { return groupKey(g.Base, g.Authors) }

// groupKey builds a group key from a base title and an author set.
func groupKey(base string, authors []string) string {
	return titleKey(base) + "\x01" + authorsKey(authors)
}

// total is the part count the titles state. Every part agrees on it - a group
// whose parts do not is refused before anything reads this.
func (g *group) total() int { return g.Parts[0].Ref.Total }

// partSlugs returns the parts' work slugs in part order.
func (g *group) partSlugs() []string {
	out := make([]string, 0, len(g.Parts))
	for _, p := range g.Parts {
		out = append(out, p.Slug)
	}
	return out
}

// Complete reports whether the parts cover 1..total exactly once, which is what
// licenses summing their runtimes into a whole-book one.
func (g *group) Complete() bool {
	seen := map[int]bool{}
	for _, p := range g.Parts {
		if seen[p.Ref.Num] {
			return false
		}
		seen[p.Ref.Num] = true
	}
	if len(seen) != g.total() {
		return false
	}
	for n := 1; n <= g.total(); n++ {
		if !seen[n] {
			return false
		}
	}
	return true
}

// Refusal is one thing the run declined to do, and why. Every refusal names the
// entries it covers, so a maintainer can act on it without re-deriving it.
type Refusal struct {
	Category string
	Subject  string
	Reason   string
	Entries  []string
}

// String renders a refusal as one line.
func (r Refusal) String() string {
	s := fmt.Sprintf("%s: %s: %s", r.Category, r.Subject, r.Reason)
	if len(r.Entries) > 0 {
		s += " [" + strings.Join(r.Entries, ", ") + "]"
	}
	return s
}

// The refusal categories: the vocabulary the report groups by. Declared
// together so adding one is a visible decision.
const (
	catAuthorSplit      = "author-split"
	catMultiRecording   = "multi-recording"
	catAmbiguousTarget  = "ambiguous-target"
	catAmbiguousSet     = "ambiguous-complete-set"
	catNarrators        = "narrator-disagreement"
	catLanguage         = "language-disagreement"
	catSlugCollision    = "slug-collision"
	catSeriesCollision  = "series-collision"
	catSeriesPosition   = "series-position-unreadable"
	catAmbiguousTwin    = "ambiguous-plain-twin"
	catTotalMismatch    = "part-count-disagreement"
	catTwinDisagreement = "twin-disagreement"
	// catInternal is a record this package could not compose: a bug here rather
	// than a judgement about the data, which is why it does not share a
	// category with one.
	catInternal = "internal-error"
)

// buildGroups partitions the GraphicAudio part works into groups and records
// the refusals that stop a group being formed at all.
func buildGroups(idx *index) (groups []*group, refusals []Refusal) {
	byKey := map[string]*group{}
	byBase := map[string]map[string]bool{}

	for _, slug := range sortedKeys(idx.candidates) {
		c := idx.candidates[slug]
		ref, ok := partOf(c.title())
		if !ok || !c.graphicAudio {
			continue
		}
		base := baseTitle(c.title())
		authors := c.authors()
		key := groupKey(base, authors)
		g := byKey[key]
		if g == nil {
			g = &group{Base: base, Authors: authors}
			byKey[key] = g
		}
		g.Parts = append(g.Parts, part{Slug: slug, Ref: ref, Work: c.obj})
		if byBase[titleKey(base)] == nil {
			byBase[titleKey(base)] = map[string]bool{}
		}
		byBase[titleKey(base)][authorsKey(authors)] = true
	}

	for _, key := range sortedKeys(byKey) {
		g := byKey[key]
		slices.SortFunc(g.Parts, func(a, b part) int {
			if a.Ref.Num != b.Ref.Num {
				return a.Ref.Num - b.Ref.Num
			}
			return strings.Compare(a.Slug, b.Slug)
		})
		groups = append(groups, g)
	}
	slices.SortFunc(groups, func(a, b *group) int { return strings.Compare(a.key(), b.key()) })

	for _, g := range groups {
		if len(byBase[titleKey(g.Base)]) > 1 {
			// One book's parts recorded under two author sets: one of them is
			// wrong (a narrator credited as an author, a stray co-author), and
			// picking a side would be inventing the answer. Both groups stay
			// as they are.
			g.refused = true
			refusals = append(refusals, Refusal{
				Category: catAuthorSplit,
				Subject:  g.Base,
				Reason:   "the parts of this book are recorded under more than one author set",
				Entries:  g.partSlugs(),
			})
			continue
		}
		if r, ok := checkParts(idx, g); !ok {
			g.refused = true
			refusals = append(refusals, r)
		}
	}
	return groups, refusals
}

// checkParts applies the per-group refusals that need only the parts.
func checkParts(idx *index, g *group) (Refusal, bool) {
	totals := map[int]bool{}
	for i := range g.Parts {
		p := &g.Parts[i]
		key, rec, ok := idx.candidates[p.Slug].soleRecording()
		if !ok {
			return Refusal{
				Category: catMultiRecording,
				Subject:  g.Base,
				Reason:   "a part does not carry exactly one recording, so which production it belongs to is not stated",
				Entries:  []string{p.Slug},
			}, false
		}
		p.RecKey, p.Rec = key, rec
		totals[p.Ref.Total] = true
	}
	if len(totals) > 1 {
		return Refusal{
			Category: catTotalMismatch,
			Subject:  g.Base,
			Reason:   "the parts disagree about how many parts the set has",
			Entries:  g.partSlugs(),
		}, false
	}
	if lang, ok := agreedLanguage(g); !ok {
		return Refusal{
			Category: catLanguage,
			Subject:  g.Base,
			Reason:   fmt.Sprintf("the parts disagree about the language (%s)", lang),
			Entries:  g.partSlugs(),
		}, false
	}
	if a, b, ok := disjointNarrators(g); !ok {
		return Refusal{
			Category: catNarrators,
			Subject:  g.Base,
			Reason:   "two parts credit no narrator in common, so they are not one production",
			Entries:  []string{a, b},
		}, false
	}
	return Refusal{}, true
}

// agreedLanguage returns the language every part states. ok is false when they
// disagree; the returned string then names the values seen, for the report.
func agreedLanguage(g *group) (string, bool) {
	seen := map[string]bool{}
	for _, p := range g.Parts {
		if l := p.Work.str("language"); l != "" {
			seen[l] = true
		}
	}
	switch len(seen) {
	case 0:
		return "", true
	case 1:
		for l := range seen {
			return l, true
		}
	}
	return strings.Join(sortedKeys(seen), ", "), false
}

// namelessCredits are the person ids that stand for something other than a
// named performer: the collective records the credit fold produces (full-cast,
// various, anonymous, uncredited, unknown) plus the shared catch-all a name
// that slugs away to nothing lands on.
//
// It is DERIVED from internal/importer rather than restated, so a sixth
// collective bucket added there is a sixth id here. Restating it was the defect:
// a bucket added later would silently have turned narrator-disagreement
// refusals into merges, because a credit naming nobody would have started
// reading as a credit naming somebody.
var namelessCredits = sync.OnceValue(func() map[string]bool {
	ids := importer.CollectiveIDs()
	ids[model.UnslugPersonID] = true
	return ids
})

// disjointNarrators looks for two parts whose IDENTIFYING narrator sets share
// nobody. A dramatization's parts legitimately credit different slices of one
// cast (36 of the live groups do), so overlap - not equality - is the test, and
// two parts that share not one performer are two productions.
func disjointNarrators(g *group) (string, string, bool) {
	nameless := namelessCredits()
	type named struct {
		slug string
		set  map[string]bool
	}
	var sets []named
	for _, p := range g.Parts {
		s := map[string]bool{}
		for _, n := range p.Rec.strs("narrators") {
			if !nameless[n] {
				s[n] = true
			}
		}
		if len(s) == 0 {
			continue
		}
		sets = append(sets, named{slug: p.Slug, set: s})
	}
	for i := 0; i < len(sets); i++ {
		for j := i + 1; j < len(sets); j++ {
			shared := false
			for n := range sets[i].set {
				if sets[j].set[n] {
					shared = true
					break
				}
			}
			if !shared {
				return sets[i].slug, sets[j].slug, false
			}
		}
	}
	return "", "", true
}

// matchTargets finds the complete-set work the catalogue already holds for each
// group: a GraphicAudio work with no part marker, the same base title and the
// same author set.
func matchTargets(idx *index, groups []*group) []Refusal {
	byKey := map[string][]string{}
	for _, slug := range sortedKeys(idx.candidates) {
		c := idx.candidates[slug]
		if _, isPart := partOf(c.title()); isPart || !c.graphicAudio {
			continue
		}
		key := groupKey(baseTitle(c.title()), c.authors())
		byKey[key] = append(byKey[key], slug)
	}
	var refusals []Refusal
	for _, g := range groups {
		if g.refused {
			continue
		}
		found := byKey[g.key()]
		switch len(found) {
		case 0:
		case 1:
			target := found[0]
			if _, _, ok := idx.candidates[target].soleRecording(); !ok {
				g.refused = true
				refusals = append(refusals, Refusal{
					Category: catMultiRecording,
					Subject:  g.Base,
					Reason:   "the complete-set work already in the catalogue does not carry exactly one recording",
					Entries:  []string{target},
				})
				continue
			}
			g.Target = target
		default:
			g.refused = true
			refusals = append(refusals, Refusal{
				Category: catAmbiguousTarget,
				Subject:  g.Base,
				Reason:   "more than one complete-set work in the catalogue matches this book",
				Entries:  found,
			})
		}
	}
	return refusals
}
