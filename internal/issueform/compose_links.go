package issueform

import (
	"fmt"
	"slices"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// compose_links.go is the correct-data form's door onto the Languages Phase 2 LINKS:
// a work's or a series' translation_of (the original(s) it translates) and a series'
// ordering / ordering_of (which reading order its positions state, and the
// franchise's primary ordering when it is a variant) - plus the one scalar that can
// break a link from a distance, a work's language.
//
// The RULES are pkg/check's (check.LinkFaults, the one rule of record); this file
// only builds the state they are asked over and words the answer as a verdict. The
// state is the catalogue with the ONE corrected record overlaid, and the question is
// which faults the correction would INTRODUCE: a fault the catalogue already carries
// (an advisory a correction elsewhere on the record does not touch) is not this
// submission's to answer for. Every introduced fault is refused - including the
// series same-language link, which a load only advises on, since writing a link the
// derived languages already contradict is a claim a submitter can check - except an
// ordering variant listing works its primary does not, which is legitimate data.
//
// The references are resolved through the tree's own tombstone table exactly as the
// sidecar path resolves a work key: a retired slug composes under its survivor and
// says so, and a slug the catalogue does not hold at all is the submitter's to fix
// (invalid), since a link must name a record that is already here.

// linkFamily is everything that differs between the two families a link can live
// in, picked once per correction.
type linkFamily struct {
	noun       string
	kind       model.RedirectKind
	resolveRef func(string) (string, bool)
	holds      func(*composer, string) bool
}

var linkFamilies = map[pack.Family]linkFamily{
	pack.FamilyWorks:  {noun: "work", kind: model.RedirectWorks, resolveRef: resolveWorkRef, holds: (*composer).holdsWork},
	pack.FamilySeries: {noun: "series", kind: model.RedirectSeries, resolveRef: resolveSeriesRef, holds: (*composer).holdsSeries},
}

func (c *composer) holdsWork(slug string) bool   { return c.works[slug] != nil }
func (c *composer) holdsSeries(slug string) bool { return c.series[slug] != nil }

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

// resolveLinkTarget turns a submitted reference into the LIVE slug a link may name,
// failing the run (invalid) when it names nothing here. A retired slug resolves to
// its survivor with a note, exactly as every other door treats one.
func (c *composer) resolveLinkTarget(fam linkFamily, field, raw string) (string, bool) {
	ref, ok := fam.resolveRef(raw)
	if !ok {
		c.fail(StatusInvalid, "%q is not a %s reference - %s takes the %s's page URL or its slug", raw, fam.noun, field, fam.noun)
		return "", false
	}
	live := c.liveSlug(fam.kind, func(s string) bool { return fam.holds(c, s) }, ref)
	if live == "" {
		c.fail(StatusInvalid, "%s %q is not in the catalogue - %s must name a %s that is already here, so add it first",
			fam.noun, ref, field, fam.noun)
		return "", false
	}
	if live != ref {
		// Not noteRetired: its "the submission is recorded against" names the record a
		// submission is filed under, and here that is the corrected record - the
		// survivor is only what the link names.
		c.note("%s slug %q was retired by a merge onto %q; %s names %q", fam.kind, ref, live, field, live)
	}
	return live, true
}

// linkView is the catalogue as the link rules read it, built once per run.
func (c *composer) linkView() check.LinkView {
	if c.links == nil {
		c.links = check.NewLinkView(c.works, c.series, c.redirects)
	}
	return c.links
}

// overlayOf reads the corrected record's current link members into an overlay of the
// catalogue (check.LinkOverlay, the state a correction is judged in), for the caller
// to change the one it corrects. ok is false (and the run failed) when the stored
// translation_of is not a string array: that is escalated, never overwritten
// (correctISBN's rule).
func (c *composer) overlayOf(fam linkFamily, addr entryAddr, record map[string]any) (*check.LinkOverlay, bool) {
	have, shaped := stringsOf(record[check.FieldTranslationOf])
	if !shaped {
		c.fail(StatusNeedsHuman, "the translation_of already on %s is not in the expected shape - a maintainer will apply this", addr.label(c))
		return nil, false
	}
	rec := check.LinkRecord{TranslationOf: have}
	rec.Language, _ = record["language"].(string)
	rec.Ordering, _ = record[check.FieldOrdering].(string)
	rec.OrderingOf, _ = record[check.FieldOrderingOf].(string)
	return check.NewLinkOverlay(c.linkView(), fam.kind, addr.slug, rec), true
}

// introducedFaults is every fault the correction would introduce
// (check.LinkOverlay.IntroducedFaults), minus the one the form never refuses (a
// variant listing works its primary does not).
func introducedFaults(o *check.LinkOverlay) []check.LinkFault {
	return slices.DeleteFunc(o.IntroducedFaults(), func(f check.LinkFault) bool { return f.Code == check.LinkNotSubset })
}

// refuseLinkFaults fails the run on the first introduced fault, in the order the
// form has always judged them - a self-link, a language clash, a chain from this
// end and then from the other, a variant's hop from this end and then the other,
// a duplicate ordering - and reports whether it did. target is the record the
// corrected link names, "" for a correction that names none (a language).
func (c *composer) refuseLinkFaults(fam linkFamily, addr entryAddr, field, target string, faults []check.LinkFault) bool {
	if len(faults) == 0 {
		return false
	}
	id := addr.slug
	rank := func(f check.LinkFault) int {
		own := f.From == id
		switch {
		case f.Code == check.LinkSelf:
			return 0
		case f.Code == check.LinkSameLanguage:
			return 1
		case f.Code == check.LinkChain && own:
			return 2
		case f.Code == check.LinkChain:
			return 3
		case f.Code == check.LinkOneHop && own:
			return 4
		case f.Code == check.LinkOneHop:
			return 5
		case f.Code == check.LinkDuplicateOrdering:
			return 6
		}
		return 7
	}
	slices.SortStableFunc(faults, func(a, b check.LinkFault) int { return rank(a) - rank(b) })
	f := faults[0]
	// The records stating the same kind of fault from the OTHER end, for the two
	// verdicts that name them all.
	var from []string
	for _, g := range faults {
		if g.Code == f.Code && g.From != id {
			from = append(from, g.From)
		}
	}
	label := addr.label(c)
	switch {
	case f.Code == check.LinkSelf && f.Field == check.FieldOrderingOf:
		c.fail(StatusInvalid, "%s cannot be a variant ordering of itself - ordering_of names the franchise's PRIMARY ordering", label)
	case f.Code == check.LinkSelf:
		c.fail(StatusInvalid, "%s cannot be a translation of itself - translation_of names the ORIGINAL %s it translates", label, fam.noun)
	case f.Code == check.LinkSameLanguage && field == check.FieldTranslationOf:
		c.fail(StatusInvalid, "%s and %s %q are both in %q - a translation is in a different language from its original; "+
			"two records of one book in one language are a duplicate to report, not a translation", label, fam.noun, f.To, f.Language)
	case f.Code == check.LinkSameLanguage && f.From == id:
		c.fail(StatusNeedsHuman, "%s would then be in %q, the same language as %s %q, which it names in translation_of - a translation "+
			"is in a different language from its original, so the language or the link is wrong; a maintainer will decide which",
			label, f.Language, fam.noun, f.To)
	case f.Code == check.LinkSameLanguage:
		c.fail(StatusNeedsHuman, "%s would then be in %q, the same language as %s %q, which names it as its original in translation_of - "+
			"a translation is in a different language from its original, so the language or the link is wrong; a maintainer will decide which",
			label, f.Language, fam.noun, f.From)
	case f.Code == check.LinkChain && f.From == id && slices.Contains(f.Others, id):
		// The target translates THIS record: "name its original instead" would name
		// the record itself, so the verdict says the two links contradict each other.
		c.fail(StatusNeedsHuman, "%s %q already names %s as its original - the two records cannot each be a translation of the other, "+
			"so if %s is really the translation, %q's translation_of is what is wrong, which a maintainer will sort out",
			fam.noun, f.To, id, id, f.To)
	case f.Code == check.LinkChain && f.From == id:
		c.fail(StatusInvalid, "%s %q is itself a translation (of %s) - translation_of names the ORIGINAL, so name %s instead",
			fam.noun, f.To, strings.Join(f.Others, ", "), strings.Join(f.Others, " or "))
	case f.Code == check.LinkChain:
		c.fail(StatusNeedsHuman, "%s is the original that %s translate(s) - a %s cannot be both an original and a translation; "+
			"if %s is really a translation, those records have to name %q instead, which a maintainer will sort out",
			label, strings.Join(from, ", "), fam.noun, id, target)
	case f.Code == check.LinkOneHop && f.From == id:
		c.fail(StatusInvalid, "series %q is itself a variant ordering of %q - ordering_of names the franchise's PRIMARY ordering, so name %q instead",
			f.To, f.Others[0], f.Others[0])
	case f.Code == check.LinkOneHop:
		c.fail(StatusNeedsHuman, "%s is the primary ordering that %s name(s) - a series cannot be both a primary and a variant, "+
			"which a maintainer will sort out", label, strings.Join(from, ", "))
	case f.Code == check.LinkDuplicateOrdering:
		other := f.From
		if other == id {
			other = f.Others[0]
		}
		c.fail(StatusInvalid, "series %q already states the %q ordering of %s's family - two series in one order are one list "+
			"to report as a duplicate, not two views of it", other, f.Ordering, f.To)
	default:
		c.fail(StatusNeedsHuman, "the %s correction on %s would leave %s %q breaking the %s link rule - a maintainer will apply it",
			field, label, f.Field, f.To, f.Code)
	}
	return true
}

// correctTranslationOf contributes ONE original to a work's or a series'
// translation_of - the ADD op, since the field is a set. The target is resolved
// through the tombstone table, an original already listed is the no-op duplicate,
// and the set it would write is then held to the link rules before anything is: no
// self-link, the two sides not in one language (a series side whose derived
// language is a tie, or unknown, cannot be judged and is not), and no chain in
// either direction - the target may not itself be a translation, and the record
// being corrected may not be an original another record already translates. The
// set is re-sorted.
func (c *composer) correctTranslationOf(addr entryAddr, record map[string]any, corrected string) (string, bool) {
	fam := linkFamilies[addr.family]
	target, ok := c.resolveLinkTarget(fam, check.FieldTranslationOf, corrected)
	if !ok {
		return "", false
	}
	o, ok := c.overlayOf(fam, addr, record)
	if !ok {
		return "", false
	}
	if slices.Contains(o.Record.TranslationOf, target) {
		c.failNoop("%s already names %q in translation_of", addr.label(c), target)
		return "", false
	}
	next := append(slices.Clone(o.Record.TranslationOf), target)
	slices.Sort(next)
	o.Record.TranslationOf = next
	if c.refuseLinkFaults(fam, addr, check.FieldTranslationOf, target, introducedFaults(o)) {
		return "", false
	}
	record[check.FieldTranslationOf] = next
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

// checkWorkLanguage is a work `language` correction's link judgement: a language
// that lands the work on the primary language of a work it translates, or of a
// work translating it, would break the translation rule from a distance - and
// which of the two statements is wrong is a maintainer's call (needs-human).
//
// Only the WORK-level rule is asked. A work's language also moves the DERIVED
// language of every series it belongs to, but a series clash is only an advisory,
// and correcting a misfiled member's language is exactly how one is resolved - so
// refusing the correction for it would block the fix for the defect it names.
func (c *composer) checkWorkLanguage(addr entryAddr, record map[string]any, value any) (any, bool) {
	fam := linkFamilies[pack.FamilyWorks]
	o, ok := c.overlayOf(fam, addr, record)
	if !ok {
		return nil, false
	}
	o.Record.Language, _ = value.(string)
	if c.refuseLinkFaults(fam, addr, "language", "", introducedFaults(o)) {
		return nil, false
	}
	return value, true
}

// checkOrdering judges a series' `ordering` correction against the family it sits
// in (its primary and every variant of that primary): no two members may state the
// same ordering - a second series in one order is a duplicate to fold, not a view.
// The vocabulary itself is the schema's (enumViolation has already spoken).
func (c *composer) checkOrdering(addr entryAddr, record map[string]any, value any) (any, bool) {
	fam := linkFamilies[pack.FamilySeries]
	o, ok := c.overlayOf(fam, addr, record)
	if !ok {
		return nil, false
	}
	o.Record.Ordering, _ = value.(string)
	if c.refuseLinkFaults(fam, addr, check.FieldOrdering, "", introducedFaults(o)) {
		return nil, false
	}
	return value, true
}

// resolveOrderingOf judges a series' `ordering_of` correction and resolves the
// reference to the live primary it names: the series must state its own ordering
// first (the schema's dependentRequired), may not name itself, and the link is one
// hop - the target may not itself be a variant, and a series other variants already
// name as their primary may not become a variant. The family it joins must not
// already hold a series in its ordering.
func (c *composer) resolveOrderingOf(addr entryAddr, record map[string]any, value any) (any, bool) {
	fam := linkFamilies[pack.FamilySeries]
	if ordering, _ := record[check.FieldOrdering].(string); ordering == "" {
		c.fail(StatusInvalid, "%s states no ordering - state this series' ordering first (a correction of \"ordering\": %s), "+
			"then file this one: a variant has to say what kind of order it is", addr.label(c), allowedValues(model.SeriesOrderings()))
		return nil, false
	}
	raw, _ := value.(string)
	target, ok := c.resolveLinkTarget(fam, check.FieldOrderingOf, raw)
	if !ok {
		return nil, false
	}
	o, ok := c.overlayOf(fam, addr, record)
	if !ok {
		return nil, false
	}
	o.Record.OrderingOf = target
	if c.refuseLinkFaults(fam, addr, check.FieldOrderingOf, target, introducedFaults(o)) {
		return nil, false
	}
	return target, true
}
