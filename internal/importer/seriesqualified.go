package importer

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/titlerule"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// seriesqualified.go is the QUALIFIER reach of series resolution: the catalogued
// series a claim reaches by what the two names state (titlerule.ReadSeriesQualifiers)
// rather than by its own name's slug chain. The invariants:
//
//   - every catalogued series is indexed once per snapshot, under its base name
//     (compared as SameSeriesName compares a name), its LANGUAGE facet - the edition
//     decoration's language, else the derived language - and its ORDERING facet. A
//     tied series has no language facet and is not indexed, so it is reached by its
//     name alone and its ordering facet is unreachable too;
//   - a VARIANT (ordering_of) is indexed under its stated ordering only, so no
//     unqualified claim reaches a reading order; a non-variant is indexed under its
//     own (the field, else its name's qualifier) and, when that is publication
//     order or none, under no ordering too. A chronological or recommended
//     non-variant ("Vorkosigan Saga (chronological)", stating no ordering_of) is a
//     reading order whatever its fields say, so it too is reached only by a claim
//     stating that order - an unqualified claim's position is not one of its slots;
//   - a claim asks with its name's base and ordering, and with its decoration's
//     language, else its row's (an unknown language asks nothing);
//   - what it reaches only ADDS candidates, after the chain's (resolveSeriesUnit's
//     tiers), judged by the same language closure and author fit;
//   - the importer never writes translation_of, ordering or ordering_of, and a
//     founded series keeps the source's name verbatim.

// qualifierKey is one index key under a base slug: the base's name key
// (titlerule.SeriesName.Key) and the two facets.
type qualifierKey struct{ name, language, ordering string }

// qualifiedIndex is base slug -> key -> the catalogued series slugs indexed there,
// sorted.
type qualifiedIndex map[string]map[qualifierKey][]string

// qualifiedSource is what the index reads of one catalogued series - copied out
// of the record, so a SeriesAuthorIndex that never builds the index does not keep
// the catalogue's series (and their membership lists) alive for its lifetime.
type qualifiedSource struct{ id, name, ordering, orderingOf string }

// qualifiedSources is the index's input over a catalogue's series, nil for none.
func qualifiedSources(series []*model.Series) []qualifiedSource {
	if len(series) == 0 {
		return nil
	}
	out := make([]qualifiedSource, len(series))
	for i, s := range series {
		out[i] = qualifiedSource{s.ID, s.Name, s.Ordering, s.OrderingOf}
	}
	return out
}

// buildQualified indexes series; language is each series' derived language.
// Every series it reads is live, so a lookup needs no liveness check.
func buildQualified(series []qualifiedSource, language map[string]string) qualifiedIndex {
	out := qualifiedIndex{}
	for _, s := range series {
		q := titlerule.ReadSeriesQualifiers(s.name)
		base := titlerule.NewSeriesName(q.Base)
		lang := q.Language
		if lang == "" {
			lang = model.PrimarySubtag(language[s.id])
		}
		if base.Slug() == "" || lang == "" {
			continue
		}
		ordering := cmp.Or(s.ordering, q.Ordering)
		orderings := []string{ordering}
		if s.orderingOf == "" && ordering == model.OrderingPublication {
			orderings = append(orderings, "")
		}
		keys := out[base.Slug()]
		if keys == nil {
			keys = map[qualifierKey][]string{}
			out[base.Slug()] = keys
		}
		for _, o := range orderings {
			k := qualifierKey{base.Key(), lang, o}
			keys[k] = append(keys[k], s.id)
		}
	}
	for _, keys := range out {
		for _, slugs := range keys {
			if len(slugs) > 1 {
				sort.Strings(slugs)
			}
		}
	}
	return out
}

// qualifiedIndex is the index, built on first use: every name group's reach asks
// it (reachOf), so a resolution with any claim builds it once per snapshot, and an
// index nothing resolves through never does.
func (ix *SeriesAuthorIndex) qualifiedIndex() qualifiedIndex {
	if ix.qualifiedFrom != nil {
		ix.qualified = buildQualified(ix.qualifiedFrom, ix.language)
		ix.qualifiedFrom = nil
	}
	return ix.qualified
}

// holdsQualifiedBase reports whether any indexed series has the base slug a claim
// to name would ask under - libex-select's cheap pre-filter, a necessary condition
// only (it asks no facet, and compares slugs rather than name keys). slug is the
// name's own slug, which is its base slug when it carries no bracketed group.
func (ix *SeriesAuthorIndex) holdsQualifiedBase(name, slug string) bool {
	if ix == nil {
		return false
	}
	if strings.ContainsAny(name, ")]") {
		slug = Slugify(titlerule.ReadSeriesQualifiers(name).Base)
	}
	return len(ix.qualifiedIndex()[slug]) > 0
}

// qualifiedCandidates is what a group of claims to name (whose slug is base)
// reaches through the index beyond the candidates it already holds (have). A
// decorated name reaches under its own decoration's language (tierDecorated,
// every claim of the group); an undecorated one under each row language of the
// claims at idx (tierLanguage, the facet naming the claims it serves), in facet
// then slug order. A name carrying no qualifier asks the index by the slug it
// already has, so a group whose base the index does not hold costs one map lookup.
func qualifiedCandidates(cat seriesCatalogue, name, base string, claims []nameClaim, idx []int, have []seriesCandidate) []seriesCandidate {
	if cat.qualified == nil {
		return nil
	}
	q := titlerule.ReadSeriesQualifiers(name)
	if q.Base != name {
		base = Slugify(q.Base)
	}
	keys := cat.qualified()[base]
	if len(keys) == 0 {
		return nil
	}
	tier, facets := tierDecorated, []string{q.Language}
	if q.Language == "" {
		tier, facets = tierLanguage, nil
		for _, ci := range idx {
			if l := claims[ci].row.language; l != "" && !slices.Contains(facets, l) {
				facets = append(facets, l)
			}
		}
		if len(facets) > 1 {
			sort.Strings(facets)
		}
	}
	seen := make(map[string]bool, len(have))
	for _, c := range have {
		seen[c.slug] = true
	}
	key := titlerule.SeriesNameGroupKey(q.Base)
	var out []seriesCandidate
	for _, f := range facets {
		for _, slug := range keys[qualifierKey{key, f, q.Ordering}] {
			if seen[slug] {
				continue
			}
			seen[slug] = true
			c := seriesCandidate{slug: slug, lang: cat.language[slug], tier: tier}
			if tier == tierLanguage {
				c.facet = f
			}
			out = append(out, c)
		}
	}
	return out
}

// SeriesNameJoin is a claim that joined a catalogued series stored under another
// name than the one it gave: a join the name alone does not show (a respelled
// qualifier, a renamed edition series, a plain name reaching its language's
// edition).
type SeriesNameJoin struct{ Given, Slug, Stored string }

func (j SeriesNameJoin) String() string {
	return fmt.Sprintf("%q joined %s %q", j.Given, j.Slug, j.Stored)
}

// SeriesNameJoinsNote is the one note line naming such joins - the importer's
// run-level note and the intake form's verdict line alike: a count, then a bounded
// list of examples (withExamples), sorted so two runs over one input read the same.
func SeriesNameJoinsNote(joins []SeriesNameJoin) string {
	examples := make([]string, len(joins))
	for i, j := range joins {
		examples[i] = j.String()
	}
	slices.Sort(examples)
	return withExamples(fmt.Sprintf("%d series claim(s) joined a catalogued series stored under another name", len(joins)), examples)
}

// noteSeriesNameJoin records a join under another stored name, once; the run's
// Notes name them (reportSeriesNameJoins) - a note, not a warning, as a tombstone
// ride is.
func (p *planner) noteSeriesNameJoin(j SeriesNameJoin) {
	if p.seriesNameJoins == nil {
		p.seriesNameJoins = map[SeriesNameJoin]bool{}
	}
	p.seriesNameJoins[j] = true
}

// reportSeriesNameJoins appends the run's one note naming the joins.
func (p *planner) reportSeriesNameJoins() {
	if len(p.seriesNameJoins) == 0 {
		return
	}
	p.summary.Notes = append(p.summary.Notes, SeriesNameJoinsNote(slices.Collect(maps.Keys(p.seriesNameJoins))))
}
