package importer

import (
	"slices"
	"sort"

	"github.com/kodestar/audiosilo-meta/internal/titlerule"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// seriesqualified.go is the QUALIFIER reach of series resolution: which held series
// a claim reaches by what the two NAMES state rather than by the exact spelling of
// their qualifiers. The chain walk (seriesCandidates) finds a held series only at
// its own name's slug, so three shapes slip past it:
//
//   - a RESPELLED qualifier: "Throne of Glass (Deutsche Ausgabe)" beside the held
//     "Throne of Glass [German Edition]" has another slug, and founded a duplicate;
//   - a held edition series whose name was cleaned to its base ("Harry Potter" at
//     `harry-potter-german-edition`) no longer equals an incoming "Harry Potter
//     [German Edition]", so the walk stepped past it and minted `-2`;
//   - a German row naming the PLAIN "Harry Potter" meets the English series, which
//     language closes, and then founded `harry-potter-2` beside the German edition
//     series the catalogue already holds.
//
// So every catalogued series is indexed ONCE per snapshot (SeriesAuthorIndex) under
// the facets its name and its own fields state, read through the one qualifier
// reader (titlerule.ReadSeriesQualifiers):
//
//   - BASE: the name minus its edition and ordering qualifiers, compared as
//     SameSeriesName compares a name (seriesNameKeyOf);
//   - LANGUAGE: the edition decoration's language when the name carries one, else
//     the series' DERIVED language. A tie has no facet and is not indexed - it is
//     reached by its name alone, as before;
//   - ORDERING: a VARIANT (ordering_of set) is indexed under its stated ordering
//     ONLY, so an unqualified claim can never reach a reading order; a non-variant
//     under no ordering and, when it states one (the field, else its name's
//     qualifier), under that one too - so "MaddAddam (Published Order)" reaches the
//     primary that absorbed it rather than founding a copy beside it.
//
// A claim asks with its own facets: its name's base and ordering, and its name's
// edition language when decorated, else the row's language (an unknown language
// asks nothing). What it reaches is only ever ADDED to the candidates: it is
// tried in resolveSeriesGroup's CLUSTER step, after every chain candidate and
// before a series the batch founds, so a claim the chain anchors or admits never
// sees it, and it is judged by every rule a chain candidate is - language
// closure, the author fit (admits), the cluster resolution. A reached series the
// claim does not join is reported as stepped past only when the claim's OWN
// edition decoration reached it (that is the series the name spells, whatever
// its stored spelling); one reached only by the row's language was never the
// claim's name. So an outcome the index does not change reads exactly as the
// walk's did, and the decorated and the renamed spelling of one held series
// resolve - and report - alike.
//
// Nothing here WRITES anything about a series: the importer never states
// translation_of, ordering or ordering_of, and a series it founds keeps the
// source's name verbatim, decoration included (T-LINK reviews the link).

// qualifierKey is one index key: a base's name key and the two facets.
type qualifierKey struct{ base, language, ordering string }

// seriesNameKeyOf is a name's equality key for the index: its slug and its
// titlerule.SeriesNameKey, the grouping form of SameSeriesName (claimGroups keys
// claims by the same pair). "" when the name has no addressable slug.
func seriesNameKeyOf(name string) string {
	slug := Slugify(name)
	if slug == "" {
		return ""
	}
	return slug + "\x00" + titlerule.SeriesNameKey(name)
}

// indexQualified fills the qualifier index from the catalogue's series.
func (ix *SeriesAuthorIndex) indexQualified(series []*model.Series) {
	ix.qualified = map[qualifierKey][]string{}
	ix.bases = map[string]bool{}
	for _, s := range series {
		q := titlerule.ReadSeriesQualifiers(s.Name)
		base := seriesNameKeyOf(q.Base)
		if base == "" {
			continue
		}
		lang := q.Language
		if lang == "" {
			lang = model.PrimarySubtag(ix.language[s.ID])
		}
		if lang == "" {
			continue // a tie: reached by its name only
		}
		ordering := s.Ordering
		if ordering == "" {
			ordering = q.Ordering
		}
		add := func(o string) {
			k := qualifierKey{base, lang, o}
			ix.qualified[k] = append(ix.qualified[k], s.ID)
		}
		if s.OrderingOf != "" {
			if ordering == "" {
				continue // the schema forbids it; reach nothing rather than everything
			}
			add(ordering)
		} else {
			add("")
			if ordering != "" {
				add(ordering)
			}
		}
		ix.bases[base] = true
	}
	for k := range ix.qualified {
		sort.Strings(ix.qualified[k])
	}
}

// reachesQualified reports whether a claim to name could reach any indexed series
// by its base: libex-select's cheap pre-filter, the qualifier twin of
// namesACatalogueChain. It asks no facet, so it is a necessary condition only.
func (ix *SeriesAuthorIndex) reachesQualified(name string) bool {
	if ix == nil || len(ix.bases) == 0 {
		return false
	}
	return ix.bases[seriesNameKeyOf(titlerule.ReadSeriesQualifiers(name).Base)]
}

// qualifiedCandidates is what a group of claims to one name reaches through the
// index beyond the chain's own candidates (have), each marked with the claim
// language it serves: the name's edition language when decorated (every claim of
// the group), else one entry per row language of the group's claims. In facet,
// then slug order, each series once per facet.
func qualifiedCandidates(cat seriesCatalogue, name string, claims []nameClaim, idx []int, have []seriesCandidate) []seriesCandidate {
	if len(cat.qualified) == 0 {
		return nil
	}
	q := titlerule.ReadSeriesQualifiers(name)
	base := seriesNameKeyOf(q.Base)
	if base == "" {
		return nil
	}
	var facets []string
	if q.Language != "" {
		facets = []string{q.Language}
	} else {
		for _, ci := range idx {
			if l := claims[ci].row.language; l != "" && !slices.Contains(facets, l) {
				facets = append(facets, l)
			}
		}
		sort.Strings(facets)
	}
	var out []seriesCandidate
	for _, f := range facets {
		for _, slug := range cat.qualified[qualifierKey{base, f, q.Ordering}] {
			if slices.ContainsFunc(have, func(c seriesCandidate) bool { return c.slug == slug }) {
				continue
			}
			if _, live := cat.stored(slug); !live {
				continue
			}
			c := seriesCandidate{slug: slug, lang: cat.language[slug]}
			if cat.evidence != nil {
				c.ev = cat.evidence(slug)
			}
			if q.Language == "" {
				c.reach = f
			}
			out = append(out, c)
		}
	}
	return out
}
