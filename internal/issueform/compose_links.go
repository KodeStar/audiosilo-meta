package issueform

import (
	"fmt"
	"slices"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// compose_links.go is the correct-data form's door onto the Languages Phase 2 LINKS:
// a work's or a series' translation_of (the original(s) it translates) and a series'
// ordering / ordering_of (which reading order its positions state, and the
// franchise's primary ordering when it is a variant).
//
// Every rule pkg/check holds these links to is asked HERE, at compose time, with a
// verdict naming the fix - a self-link, two sides in one language, a two-hop chain,
// a second series in one ordering of a family - rather than written and then bounced
// by the post-write validation as a raw metacheck line. The references are resolved
// through the tree's own tombstone table exactly as the sidecar path resolves a
// work key: a retired slug composes under its survivor and says so, and a slug the
// catalogue does not hold at all is the submitter's to fix (invalid), since a link
// must name a record that is already here.

// linkNoun is the family's noun for a verdict, and the tombstone namespace its
// references resolve through.
func linkNoun(f pack.Family) (string, model.RedirectKind) {
	if f == pack.FamilySeries {
		return "series", model.RedirectSeries
	}
	return "work", model.RedirectWorks
}

// resolveLinkRef reads a reference to a record of the family - a page URL, a legacy
// ?id= URL, a data path or a bare slug - into the slug it names.
func resolveLinkRef(f pack.Family, ref string) (string, bool) {
	if f == pack.FamilySeries {
		return resolveSeriesRef(ref)
	}
	return resolveWorkRef(ref)
}

// resolveSeriesRef is resolveWorkRef's twin for a SERIES reference: whatever
// resolveRecordRef reads as a series (the /series/<slug> page, series?id=, the
// data path), else a bare slug. A reference resolveRecordRef reads as another kind
// is not a series reference at all.
func resolveSeriesRef(ref string) (string, bool) {
	if rr, ok := resolveRecordRef(ref); ok {
		if rr.kind != model.KindSeries {
			return "", false
		}
		return rr.slug, true
	}
	ref = strings.TrimSpace(ref)
	if strings.ContainsAny(ref, "/?#") {
		return "", false
	}
	return sanitizeSlug(ref)
}

// liveLinkSlug is slug itself when the catalogue holds it, its survivor when the
// tree's tombstone table retired it onto a live record, else "" - liveWorkSlug for
// either family.
func (c *composer) liveLinkSlug(f pack.Family, slug string) string {
	if f == pack.FamilyWorks {
		return c.liveWorkSlug(slug)
	}
	if _, ok := c.series[slug]; ok {
		return slug
	}
	if to, retired := c.redirects.Survivor(model.RedirectSeries, slug); retired {
		if _, live := c.series[to]; live {
			return to
		}
	}
	return ""
}

// resolveLinkTarget turns a submitted reference into the LIVE slug a link may name,
// failing the run (invalid) when it names nothing here. A retired slug resolves to
// its survivor with a note, exactly as every other door treats one.
func (c *composer) resolveLinkTarget(f pack.Family, field, raw string) (string, bool) {
	noun, kind := linkNoun(f)
	ref, ok := resolveLinkRef(f, raw)
	if !ok {
		c.fail(StatusInvalid, "%q is not a %s reference - %s takes the %s's page URL or its slug", raw, noun, field, noun)
		return "", false
	}
	live := c.liveLinkSlug(f, ref)
	if live == "" {
		c.fail(StatusInvalid, "%s %q is not in the catalogue - %s must name a %s that is already here, so add it first",
			noun, ref, field, noun)
		return "", false
	}
	if live != ref {
		c.noteRetired(kind, ref, live)
	}
	return live, true
}

// linkLanguage is the primary language subtag a translation link is judged by: a
// work's own language (the record being corrected is read as it stands, queued
// writes included), and a series' DERIVED language - model.SeriesLanguage, the
// strict majority of its members, "" on a tie or when nothing is known.
func (c *composer) linkLanguage(f pack.Family, slug string, record map[string]any) string {
	if f == pack.FamilyWorks {
		if record != nil {
			lang, _ := record["language"].(string)
			return model.PrimarySubtag(lang)
		}
		if w := c.works[slug]; w != nil {
			return model.PrimarySubtag(w.Language)
		}
		return ""
	}
	s := c.series[slug]
	if s == nil {
		return ""
	}
	return model.SeriesLanguage(s, func(id string) string {
		if w := c.works[id]; w != nil {
			return w.Language
		}
		return ""
	})
}

// translationTargets is what a catalogued record's translation_of names.
func (c *composer) translationTargets(f pack.Family, slug string) []string {
	if f == pack.FamilyWorks {
		if w := c.works[slug]; w != nil {
			return w.TranslationOf
		}
		return nil
	}
	if s := c.series[slug]; s != nil {
		return s.TranslationOf
	}
	return nil
}

// translationSources is every catalogued record of the family whose translation_of
// names slug, sorted. A scan, because one correction asks it once and an index
// over the whole catalogue would cost more than the question.
func (c *composer) translationSources(f pack.Family, slug string) []string {
	var out []string
	if f == pack.FamilyWorks {
		for id, w := range c.works {
			if slices.Contains(w.TranslationOf, slug) {
				out = append(out, id)
			}
		}
	} else {
		for id, s := range c.series {
			if slices.Contains(s.TranslationOf, slug) {
				out = append(out, id)
			}
		}
	}
	slices.Sort(out)
	return out
}

// correctTranslationOf contributes ONE original to a work's or a series'
// translation_of - the ADD op, since the field is a set. The target is resolved
// through the tombstone table and then held to pkg/check's rules before anything is
// written: no self-link, the two sides not in one language (a series side whose
// language is a tie, or unknown, cannot be judged and is not), and no chain in
// either direction - the target may not itself be a translation, and the record
// being corrected may not be an original another record already translates. An
// original that is already listed is the no-op duplicate. The set is re-sorted.
func (c *composer) correctTranslationOf(addr entryAddr, record map[string]any, corrected string) (string, bool) {
	const field = "translation_of"
	noun, _ := linkNoun(addr.family)
	target, ok := c.resolveLinkTarget(addr.family, field, corrected)
	if !ok {
		return "", false
	}
	if target == addr.slug {
		c.fail(StatusInvalid, "%s cannot be a translation of itself - translation_of names the ORIGINAL %s it translates", addr.label(c), noun)
		return "", false
	}
	have, shaped := stringsOf(record[field])
	if !shaped {
		c.fail(StatusNeedsHuman, "the translation_of already on %s is not in the expected shape - a maintainer will apply this", addr.label(c))
		return "", false
	}
	if slices.Contains(have, target) {
		c.failNoop("%s already names %q in translation_of", addr.label(c), target)
		return "", false
	}
	own, theirs := c.linkLanguage(addr.family, addr.slug, record), c.linkLanguage(addr.family, target, nil)
	if own != "" && own == theirs {
		c.fail(StatusInvalid, "%s and %s %q are both in %q - a translation is in a different language from its original; "+
			"two records of one book in one language are a duplicate to report, not a translation", addr.label(c), noun, target, own)
		return "", false
	}
	if originals := c.translationTargets(addr.family, target); len(originals) > 0 {
		c.fail(StatusInvalid, "%s %q is itself a translation (of %s) - translation_of names the ORIGINAL, so name %s instead",
			noun, target, strings.Join(originals, ", "), strings.Join(originals, " or "))
		return "", false
	}
	if sources := c.translationSources(addr.family, addr.slug); len(sources) > 0 {
		c.fail(StatusInvalid, "%s is the original that %s translate(s) - a %s cannot be both an original and a translation; "+
			"if %s is really a translation, those records have to name %q instead, which a maintainer will sort out",
			addr.label(c), strings.Join(sources, ", "), noun, addr.slug, target)
		return "", false
	}
	next := append(slices.Clone(have), target)
	slices.Sort(next)
	record[field] = next
	return fmt.Sprintf("added translation_of %q", target), true
}

// stringsOf reads a raw string array, reporting false for a value that is present
// but not one: that is escalated, never overwritten (correctISBN's rule).
func stringsOf(v any) ([]string, bool) {
	if v == nil {
		return nil, true
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		s, ok := x.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

// checkOrdering judges a series' `ordering` correction against the family it sits
// in (its primary and every variant of that primary): no two members may state the
// same ordering - a second series in one order is a duplicate to fold, not a view.
// The vocabulary itself is the schema's (enumViolation has already spoken).
func (c *composer) checkOrdering(addr entryAddr, record map[string]any, value any) (any, bool) {
	ordering, _ := value.(string)
	primary := addr.slug
	if of, _ := record["ordering_of"].(string); of != "" {
		primary = of
	}
	if other := c.familyMemberWith(primary, ordering, addr.slug); other != "" {
		c.fail(StatusInvalid, "series %q already states the %q ordering of %s's family - two series in one order are one list "+
			"to report as a duplicate, not two views of it", other, ordering, primary)
		return nil, false
	}
	return ordering, true
}

// resolveOrderingOf judges a series' `ordering_of` correction and resolves the
// reference to the live primary it names: the series must state its own ordering
// first (the schema's dependentRequired), may not name itself, and the link is one
// hop - the target may not itself be a variant, and a series other variants already
// name as their primary may not become a variant. The family it joins must not
// already hold a series in its ordering.
func (c *composer) resolveOrderingOf(addr entryAddr, record map[string]any, value any) (any, bool) {
	const field = "ordering_of"
	ordering, _ := record["ordering"].(string)
	if ordering == "" {
		c.fail(StatusInvalid, "%s states no ordering - state this series' ordering first (a correction of \"ordering\": %s), "+
			"then file this one: a variant has to say what kind of order it is", addr.label(c), allowedValues(model.SeriesOrderings()))
		return nil, false
	}
	raw, _ := value.(string)
	target, ok := c.resolveLinkTarget(pack.FamilySeries, field, raw)
	if !ok {
		return nil, false
	}
	if target == addr.slug {
		c.fail(StatusInvalid, "%s cannot be a variant ordering of itself - ordering_of names the franchise's PRIMARY ordering", addr.label(c))
		return nil, false
	}
	if s := c.series[target]; s != nil && s.OrderingOf != "" {
		c.fail(StatusInvalid, "series %q is itself a variant ordering of %q - ordering_of names the franchise's PRIMARY ordering, so name %q instead",
			target, s.OrderingOf, s.OrderingOf)
		return nil, false
	}
	if variants := c.variantsOf(addr.slug); len(variants) > 0 {
		c.fail(StatusInvalid, "%s is the primary ordering that %s name(s) - a series cannot be both a primary and a variant, "+
			"which a maintainer will sort out", addr.label(c), strings.Join(variants, ", "))
		return nil, false
	}
	if other := c.familyMemberWith(target, ordering, addr.slug); other != "" {
		c.fail(StatusInvalid, "series %q already states the %q ordering of %s's family - two series in one order are one list "+
			"to report as a duplicate, not two views of it", other, ordering, target)
		return nil, false
	}
	return target, true
}

// variantsOf is every catalogued series whose ordering_of names slug, sorted.
func (c *composer) variantsOf(slug string) []string {
	var out []string
	for id, s := range c.series {
		if s.OrderingOf == slug {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

// familyMemberWith returns a member of primary's ordering family - the primary and
// its variants - other than except that states ordering, or "". The primary is
// asked first and the variants in slug order, so a verdict names the same series on
// every run.
func (c *composer) familyMemberWith(primary, ordering, except string) string {
	if ordering == "" {
		return ""
	}
	for _, id := range append([]string{primary}, c.variantsOf(primary)...) {
		if id == except {
			continue
		}
		if s := c.series[id]; s != nil && s.Ordering == ordering {
			return id
		}
	}
	return ""
}
