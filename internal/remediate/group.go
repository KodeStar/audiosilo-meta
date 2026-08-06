package remediate

import (
	"fmt"
	"sort"
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
	Total   int      // the part count the titles state

	// Target is the complete-set work the catalogue already holds for this
	// book, empty when there is none and one has to be minted.
	Target string
	// Set is the complete-set dump row, nil when none was supplied or matched.
	Set *completeSet

	// Slug is the merged work's slug: Target when merging into one, else the
	// minted slug.
	Slug string
	// Title is the merged work's title.
	Title string

	// Complete reports whether the parts cover 1..Total exactly once, which is
	// what licenses summing their runtimes.
	Complete bool

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

// partSlugs returns the parts' work slugs in part order.
func (g *group) partSlugs() []string {
	out := make([]string, 0, len(g.Parts))
	for _, p := range g.Parts {
		out = append(out, p.Slug)
	}
	return out
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
		s += " [" + joinComma(r.Entries) + "]"
	}
	return s
}

// The refusal categories. Declared together so the report can list them in a
// fixed order and so adding one is a visible decision.
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
)

// buildGroups partitions the GraphicAudio part works into groups and records
// the refusals that stop a group being formed at all.
func buildGroups(idx *index) (groups []*group, refusals []Refusal) {
	byKey := map[string]*group{}
	byBase := map[string]map[string]bool{}

	for _, slug := range sortedKeys(idx.candidates) {
		c := idx.candidates[slug]
		ref, ok := partOf(c.title())
		if !ok || !isGraphicAudio(c.obj) {
			continue
		}
		base := baseTitle(c.title())
		authors := c.authors()
		key := groupKey(base, authors)
		g := byKey[key]
		if g == nil {
			g = &group{Base: base, Authors: authors, Total: ref.Total}
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
		sort.Slice(g.Parts, func(i, j int) bool {
			if g.Parts[i].Ref.Num != g.Parts[j].Ref.Num {
				return g.Parts[i].Ref.Num < g.Parts[j].Ref.Num
			}
			return g.Parts[i].Slug < g.Parts[j].Slug
		})
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].key() < groups[j].key() })

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
		if r, ok := checkParts(g); !ok {
			g.refused = true
			refusals = append(refusals, r)
			continue
		}
		g.Complete = partsComplete(g)
	}
	return groups, refusals
}

// checkParts applies the per-group refusals that need only the parts.
func checkParts(g *group) (Refusal, bool) {
	totals := map[int]bool{}
	for i := range g.Parts {
		p := &g.Parts[i]
		key, rec, ok := soleRecording(p.Work)
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

// partsComplete reports whether the parts cover 1..Total exactly once. Only a
// complete set licenses summing the parts' runtimes into a whole-book one.
func partsComplete(g *group) bool {
	seen := map[int]bool{}
	for _, p := range g.Parts {
		if seen[p.Ref.Num] {
			return false
		}
		seen[p.Ref.Num] = true
	}
	if len(seen) != g.Total {
		return false
	}
	for n := 1; n <= g.Total; n++ {
		if !seen[n] {
			return false
		}
	}
	return true
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
	return joinComma(sortedKeys(seen)), false
}

// collectiveNarrators are the canonical records that stand for a cast rather
// than for a named performer (internal/importer/collective.go folds every
// spelling onto these). A part credited only to one of them states no
// identifying narrator at all, so it can neither agree nor disagree with a
// sibling part - which matters, because GraphicAudio's parts really do credit
// different subsets of one cast, and several parts carry the bare "full-cast"
// credit alone.
var collectiveNarrators = map[string]bool{
	"full-cast":  true,
	"various":    true,
	"anonymous":  true,
	"uncredited": true,
	"unknown":    true,
	"person":     true,
}

// disjointNarrators looks for two parts whose IDENTIFYING narrator sets share
// nobody. A dramatization's parts legitimately credit different slices of one
// cast (36 of the live groups do), so overlap - not equality - is the test, and
// two parts that share not one performer are two productions.
func disjointNarrators(g *group) (string, string, bool) {
	type named struct {
		slug string
		set  map[string]bool
	}
	var sets []named
	for _, p := range g.Parts {
		s := map[string]bool{}
		for _, n := range p.Rec.strs("narrators") {
			if !collectiveNarrators[n] {
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
		if _, isPart := partOf(c.title()); isPart {
			continue
		}
		if !isGraphicAudio(c.obj) {
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
			if _, _, ok := soleRecording(idx.candidates[target].obj); !ok {
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

// joinComma renders a list for a one-line report.
func joinComma(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
