package issueform

import (
	"sort"
	"strconv"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/internal/titlerule"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// dupidentity.go is the intake side of the duplicate-and-decoration prevention:
// the two gates and the one rewrite that keep an add-work submission from adding a
// second record of a book the catalogue already holds.
//
// The three existing duplicate gates (dedupIdentifiers' ASIN/ISBN, the
// add-recording narrator set, and the work-SLUG collision) all compare something
// EXACT. That is why a retailer-decorated title walks past all three: a submission
// titled "Hammered: The Iron Druid Chronicles, Book 3" carries a fresh ASIN, names a
// narrator nobody recorded, and slugs to `hammered-the-iron-druid-chronicles-book-3`,
// which no work occupies. The tree accumulated 4,596 near-duplicate work clusters
// this way, mostly from the bulk waves, and the audit that measured them is the
// reason these gates exist.
//
// The three additions, in the order addWork applies them:
//
//  1. the SERIES-VOLUME gate. A title that states a known series and a volume
//     number, where that series already holds a work at that position, is that
//     work - the "hammered-the-iron-druid-chronicles-book-3" shape. It is checked
//     against the catalogue's series and positions, not against the title alone,
//     and it runs FIRST because it is the most specific claim available: it can
//     name the volume, so its verdict tells a submitter which record theirs is.
//  2. the decoration STRIP. A submitted title carrying decoration
//     (titlerule.Decorations) is cleaned before its slug is derived, when cleaning
//     is safe under the audit's own boundary-anchored rules
//     (titlerule.StripDecoration). This is what lets the EXISTING slug gate do its
//     job: "Mageling (Unabridged)" cleaned to "Mageling" collides with the work
//     already stored there, where the decorated spelling did not.
//
//     ONE consequence is deliberate and worth stating: the slug gate is
//     author-blind (a taken slug is a duplicate verdict whoever wrote the book),
//     so a decorated title for a DIFFERENT book that cleans onto a taken slug now
//     reaches that verdict instead of being composed. It is the conservative
//     failure - nothing is written and the submitter is told which record we hold -
//     and making that gate author-aware would change a verdict that predates this
//     change, so it is left exactly as it is.
//  3. the NORMALIZED-IDENTITY gate. The submitted title's normalized identity
//     (check.WorkIdentity, the same index the bulk importer's create guard and
//     metacheck's census read) against the catalogue, with the language and
//     author-nesting rules applied.
//
// VERDICT DISCIPLINE is the existing one, not a new one: a duplicate is
// StatusDuplicate naming the record it duplicates, unless that record is still a
// bulk-mirror seed, in which case it is StatusNeedsHuman because the submitter's
// data should REPLACE the seed and the bot only composes new records
// (failDuplicateWork, and see LICENSING.md's trust tiers). Both gates route through
// that one function, so all five duplicate gates now answer the tier question the
// same way.
//
// Every gate fires only where an existing one has not already decided: they are
// reached after the identifier and slug gates have returned, so no fixture verdict
// that used to be `duplicate` becomes something else.

// checkDecoratedTitle applies gate 1. It returns the title the work should be
// composed under - the submitted one, or the cleaned one - and false when the
// submission cannot be composed at all (a terminal verdict has been set).
//
// The REFUSALS are split deliberately, and the split is narrower than "refuse
// whatever cannot be stripped":
//
//   - a residual that names no book, or reads as a fragment, is needs-human. The
//     submitted title is decoration ("Book One", "Omnibus", "- Band 5"): we cannot
//     derive the book's name from it and neither can a mechanical pass, so a
//     maintainer titles it.
//   - everything else proceeds with the title as submitted. In particular a title
//     that IS its series' name is perfectly ordinary - a one-book series is named
//     after its book - and an omnibus whose clean form would BE the series name is a
//     legitimate record. Refusing those would turn good submissions away, which is
//     the one failure mode a gate must not have.
// newVolume says the submission states a volume its series does not yet hold (see
// checkSeriesVolume). It is what stops the strip from collapsing a NEW volume of a
// serial published under one title onto its sibling: "Bravelands, Book 4" cleaned to
// "Bravelands" lands on the slug volume 1 occupies, and the marker is the only thing
// in the title that distinguishes the two.
func (c *composer) checkDecoratedTitle(title, seriesName string, newVolume bool) (string, bool) {
	// The series the title is read against: the one the form states if it states
	// one, else a catalogued series the title itself names. The second door is what
	// makes the strip see a series reference the submitter did not fill in.
	series := seriesName
	if series == "" {
		if name, _, ok := c.identityIndex().SeriesNameIn(title); ok {
			series = name
		}
	}
	codes := titlerule.Decorations(titlerule.TitleFacts{
		Title:   title,
		Series:  series,
		Resolve: c.resolveSeries,
	})
	if len(codes) == 0 {
		return title, true
	}
	cleaned, refusal, ok := titlerule.StripDecoration(title, series)
	if ok {
		if newVolume && c.works[slugify(cleaned)] != nil {
			// The residual names a work we already hold, and the submission is a volume
			// that series does not have: the collision is with a SIBLING, so the title
			// keeps the marker that tells them apart. Stripping here would hand the
			// submitter the slug gate's duplicate verdict for a book we do not hold,
			// which is the one failure a gate must not have.
			c.note("Title %q carries retailer decoration (%s), but %q is already a work and this is "+
				"volume the series does not hold yet - the title is kept as submitted so the volume stays distinct",
				title, strings.Join(codes, ", "), cleaned)
			return title, true
		}
		c.note("Title %q carries retailer decoration (%s) - composed as %q instead, "+
			"so it cannot become a second record of a book already in the catalogue",
			title, strings.Join(codes, ", "), cleaned)
		return cleaned, true
	}
	switch refusal {
	case titlerule.RefuseNoIdentity, titlerule.RefuseFragment:
		c.fail(StatusNeedsHuman, "Title %q is retailer decoration (%s) rather than the book's name, "+
			"and removing it leaves %s - a maintainer must decide what this work is called",
			title, strings.Join(codes, ", "), decorationRefusalReason(refusal))
		return title, false
	default:
		// Nothing safe to remove: the title stands as submitted. Silent on purpose -
		// "your title mentions its series" is not news, and the two gates below still
		// look for the duplicate it might be.
		return title, true
	}
}

// decorationRefusalReason renders a strip refusal for a contributor-facing verdict.
// It is a small table rather than the code itself so the message reads as a
// sentence, and it is exhaustive over the two codes its caller routes here.
func decorationRefusalReason(refusal string) string {
	switch refusal {
	case titlerule.RefuseNoIdentity:
		return "nothing that names a book"
	case titlerule.RefuseFragment:
		return "a fragment rather than a title"
	default:
		return refusal
	}
}

// checkSeriesVolume applies gate 1: the submitted title states a known series and a
// volume number, and that series already holds a work at that position.
//
// It answers TWO questions from one derivation, because they are the same lookup and
// the second is what keeps the strip safe: duplicate says a terminal verdict has been
// set, and newVolume says the submission states a volume the series does NOT hold -
// a genuinely new book, whose volume marker is load-bearing (see
// checkDecoratedTitle).
//
// The evidence is the CATALOGUE's, not the title's: the series must be one we hold
// (by name, matched at word boundaries with the audit's two-significant-word floor)
// and the position must be occupied by a work already recorded there. A title's
// number alone is famously unreliable - it is as often a part, an episode, a season
// or a collection index, which is why internal/audit files its own version of this
// shape as advisory - so the gate requires the number to name a slot the series
// actually fills, and reports rather than merges.
//
// It runs BEFORE the normalized-identity gate because it is the more specific
// claim: it names the volume, so its message can say which member the submission
// duplicates.
func (c *composer) checkSeriesVolume(title, seriesName string) (duplicate, newVolume bool) {
	series, ok := c.seriesForTitle(title, seriesName)
	if !ok {
		return false, false
	}
	vol, stated := titlerule.StatedVolume(title, series.Name)
	if !stated {
		return false, false
	}
	member, ok := workAtPosition(series, vol)
	if !ok {
		return false, true
	}
	if c.works[member] == nil {
		return false, false // a dangling membership; metacheck reports it
	}
	c.failDuplicateWork(member, "the title states %q volume %s, and %s is already recorded at that position (%s) - "+
		"use the Add a recording form if this is another narration of it, or correct the title if it is a different book",
		series.Name, formatVolume(vol), member, c.entryLocation(pack.FamilyWorks, member, ""))
	return true, false
}

// seriesForTitle resolves the series a title is about: the one the form named, else
// a catalogued series whose name the title spells out.
func (c *composer) seriesForTitle(title, seriesName string) (*model.Series, bool) {
	if seriesName != "" {
		if s := c.series[slugify(seriesName)]; s != nil && strings.EqualFold(s.Name, seriesName) {
			return s, true
		}
	}
	if _, id, ok := c.identityIndex().SeriesNameIn(title); ok {
		if s := c.series[id]; s != nil {
			return s, true
		}
	}
	return nil, false
}

// workAtPosition returns the work a series records at a single numeric position.
//
// A RANGE ("1-3.5") is not a position: it spans several, and an omnibus covering
// volume 3 is not the same book as volume 3. Acceptance goes through
// importer.NormalizeSequence - the rule of record for what a position may be - and
// the span through model.ParsePositionRange, in that order and for the reason
// internal/audit's positionKey states: the span reader is a ParseFloat and would
// otherwise admit values the data model rejects.
func workAtPosition(s *model.Series, vol float64) (string, bool) {
	for _, sw := range s.Works {
		norm, ok := importer.NormalizeSequence(sw.Position)
		if !ok || strings.Contains(norm, "-") {
			continue
		}
		lo, hi, ok := model.ParsePositionRange(norm)
		if !ok || lo != hi || lo != vol {
			continue
		}
		return sw.Work, true
	}
	return "", false
}

// formatVolume renders a volume number the way a position is spelled, so a verdict
// quoting one reads like the data ("2" and "2.5", never "2.000000").
func formatVolume(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// checkNormalizedIdentity applies gate 3: the submitted work's normalized identity
// against the catalogue. It returns true when a terminal verdict has been set.
//
// authorSlugs is the submission's author list. A form states no contributor ROLES,
// so its whole list is also its identity list - the nesting rule
// (check.IdentityAuthorsMatch) is what makes that meet a catalogued work whose
// author list carries a role-credited translator the form never mentions, which is
// the fork shape the audit's calibration set is full of.
func (c *composer) checkNormalizedIdentity(title, seriesName, lang string, authorSlugs []string, newVolume bool) bool {
	if len(authorSlugs) == 0 {
		return false
	}
	// The POSITION veto, in the intake bot's own terms: the submission states a volume
	// its series does not hold, so the catalogue itself says this is a book we do not
	// have - whatever its title reduces to once the marker comes off. It is the same
	// judgement internal/importer's seriesClaim.compatible makes on the bulk side, and
	// without it a serial published under one title ("Bravelands, Book 4") would be
	// refused as a duplicate of its own volume 1.
	if newVolume {
		return false
	}
	series := seriesName
	if series == "" {
		if name, _, ok := c.identityIndex().SeriesNameIn(title); ok {
			series = name
		}
	}
	authors := importer.ToSet(authorSlugs)
	matches := c.identityIndex().Match(title, series, lang, authors, authors)
	if len(matches) == 0 {
		return false
	}
	m := matches[0]
	c.failDuplicateWork(m.Work.ID, "%q normalizes to the same title and authors as the catalogued work %q (%q) at %s - "+
		"use the Add a recording form if this is another narration of it, or a correct-data form if its title needs fixing",
		title, m.Work.ID, m.Work.Title, c.entryLocation(pack.FamilyWorks, m.Work.ID, ""))
	return true
}

// identityIndex is the submission's normalized-identity index over the catalogue,
// built at most once per run and only for the paths that ask.
//
// LAZY on purpose: building it cleans every catalogued title, and the correction,
// sidecar and add-recording templates never ask - so a run that is not composing a
// new work pays nothing. It is the SAME index the bulk importer's create guard uses
// (check.NewWorkIdentity), which is what makes the intake bot and a library import
// agree about what one book is.
func (c *composer) identityIndex() *check.WorkIdentity {
	if c.identity == nil {
		c.identity = check.NewWorkIdentity(c.catalogView())
	}
	return c.identity
}

// catalogView reassembles the loaded catalogue from the composer's dedup maps.
//
// loadExisting keeps works and series by id rather than the *model.Catalog it read
// them from (every other gate is a map lookup), so this rebuilds the two slices the
// index needs, in ID ORDER - the index's own determinism depends on nothing else,
// but a caller that sorts is one less thing to reason about. People are not needed:
// the index compares author SLUGS, which the submission and the records both carry.
func (c *composer) catalogView() *model.Catalog {
	cat := &model.Catalog{}
	ids := make([]string, 0, len(c.works))
	for id := range c.works {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		cat.Works = append(cat.Works, c.works[id])
	}
	ids = ids[:0]
	for id := range c.series {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		cat.Series = append(cat.Series, c.series[id])
	}
	return cat
}

// resolveSeries is the composer's titlerule.SeriesResolver, for the decoration rule
// that needs a series' CANONICAL name as well as the spelling that matched (the
// article-plus-series prefix: it is what tells a retailer's spurious "A" from a
// series genuinely called "A Thousand Li").
func (c *composer) resolveSeries(text string) (form, name string, ok bool) {
	form, id, ok := c.identityIndex().SeriesNameIn(text)
	if !ok {
		return "", "", false
	}
	if s := c.series[id]; s != nil {
		name = s.Name
	}
	return form, name, true
}
