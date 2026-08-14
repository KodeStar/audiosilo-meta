package issueform

import (
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
// The three additions, in the order addWork applies them, all over ONE derivation of
// the submitted title's context (titleContext, resolved once):
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
// VERDICT DISCIPLINE is the existing one, not a new one. The two DUPLICATE gates go
// through failDuplicateWork - failDuplicate's sibling for a work record - so a
// collision with a record that is still nothing but a bulk-mirror seed is
// StatusNeedsHuman (the submitter's data should REPLACE the seed and the bot only
// composes new records) exactly as it is at the three older gates. The strip is not a
// duplicate verdict at all: it either rewrites the title or refuses the submission on
// its own terms.
//
// Every gate fires only where an existing one has not already decided: they are
// reached after the identifier gate has returned, so no submission an old gate
// decided about gets a new verdict.
//
// THE VETOES ARE SHARED WITH THE BULK GUARD, deliberately: an intake verdict a
// re-import would contradict is worse than either answer on its own. Three of them
// are the same rule on both sides of the seam -
//
//   - AMBIGUITY: a key naming SEVERAL catalogued works supports no verdict naming one
//     of them (the bare-series-name serial shape);
//   - the POSITIVE VOLUME test: a title that STATES which volume it is is only a
//     duplicate of a work the catalogue places at that volume - silence is a veto, not
//     agreement (placesMatch here, seriesClaim.places there);
//   - COLLECTION: a boxed set is not the volume it collects (applied inside
//     check.WorkIdentity.matches, so both writers and the census inherit it).
//
// Two differences are deliberate rather than accidental. The evidence differs at the
// SOURCE - a bulk row carries its own series claim, while a submission's series is
// resolved from the form or from the title - and only the intake side has a title to
// COMPOSE, so it additionally keeps a decoration marker rather than stripping onto a
// work whose relationship to the submission nothing records. Where the bulk guard
// would simply decline to refuse, this side declines to REWRITE as well.

// titleContext is everything the three gates derive from the submitted title, and it
// is derived ONCE per submission.
//
// It exists because all three used to re-answer the same two questions ("which series
// is this title about" and "is it a volume the series does not hold"), which meant
// three linear scans of the catalogue's series names and a hand-threaded newVolume
// flag. Resolving it once makes the gates read as what they are - three tests over
// one derivation - and makes SeriesNameIn once-per-submission in practice as well as
// in principle.
type titleContext struct {
	// title is the title the work will be COMPOSED under: the submitted one, or the
	// cleaned one once the strip has run.
	title string
	// series is the series NAME the title is read against, or "": the form's, else a
	// catalogued series the title itself names. A form may state a series the
	// catalogue does not hold yet, and cleaning against that name is still right.
	series string
	// seriesRec is the catalogued series record, or nil - the positions half of the
	// series-volume gate, which only a record we hold can answer.
	seriesRec *model.Series
	// statesVolume: the title itself says which volume it is (titlerule.StatedVolume,
	// against series when one resolved and against nothing when none did). A title
	// that states a volume is making a claim, and the catalogue can only confirm it
	// through a series record - so the two gates below treat an UNCONFIRMED claim as a
	// veto rather than as agreement, and the strip keeps the marker that carries it.
	statesVolume bool
	// volume is that number, meaningful only when statesVolume.
	volume float64
	// memberAt is the work seriesRec records at that volume, or "" - the whole of
	// what the catalogue can say to confirm the claim.
	memberAt string
}

// placesMatch reports whether the catalogue positively places workID at the volume
// the title states - the POSITIVE test, the intake mirror of
// internal/importer's seriesClaim.places.
//
// A title stating no volume never needs it (nothing to confirm). A title that DOES
// state one needs a series record we hold, a position in it, and that position to be
// occupied by this very work: anything less is silence, and silence about a stated
// volume is a veto. Without it "Circus of the Dead, Book 2" was a duplicate of the
// plain "Circus of the Dead" whenever no series resolved - a book we do not hold,
// refused on the strength of a marker the key had thrown away.
func (ctx titleContext) placesMatch(workID string) bool {
	return !ctx.statesVolume || (ctx.memberAt != "" && ctx.memberAt == workID)
}

// sameBookAs reports whether this submission could be a second record of a
// catalogued work, on the two TITLE-side rules the context can answer: the positive
// volume test, and the collection veto (a boxed set is not the volume it collects -
// titlerule.IsCollection, the same rule check.WorkIdentity.matches applies at the
// identity gate).
//
// It is what the STRIP consults before rewriting a title onto an existing work's
// slug. Deliberately NOT the author rule: the slug gate that follows has been
// author-blind since long before this change, and the two would then disagree.
func (ctx titleContext) sameBookAs(w *model.Work) bool {
	return ctx.placesMatch(w.ID) && titlerule.IsCollection(ctx.title) == titlerule.IsCollection(w.Title)
}

// titleContextFor resolves the context: the series the title is about, the record we
// hold for it, and whether the title states a volume that record has no work at.
func (c *composer) titleContextFor(title, formSeries string) titleContext {
	ctx := titleContext{title: title}
	if formSeries != "" {
		if s := c.series[slugify(formSeries)]; s != nil && strings.EqualFold(s.Name, formSeries) {
			ctx.seriesRec = s
		}
		// A name the INDEX would not read a title against is not read against one
		// here either (WorkIdentity.Admits): a one-word series name strips its own
		// titles down to their volume number, and a fold-ambiguous one would make
		// what this submission COMPOSES depend on how some unrelated series spells
		// its name. The series RECORD above is still resolved - it is addressed by
		// slug, which is unambiguous - so the position gate keeps working while the
		// title is left alone.
		if c.identityIndex().Admits(formSeries) {
			ctx.series = formSeries
		}
	}
	if ctx.series == "" {
		// The second door: a series the submitter did not name but the title spells
		// out. It is what lets the strip see a series reference on a form whose series
		// fields are empty, which most retailer-decorated titles arrive as. Its
		// candidates are the admitted list, so both doors apply one rule.
		if name, id, ok := c.identityIndex().SeriesNameIn(title); ok {
			ctx.series = name
			if ctx.seriesRec == nil {
				ctx.seriesRec = c.series[id]
			}
		}
	}
	// The volume the title states - against the series it is read against, which may
	// be nothing at all: "Hammered, Book 7" states volume 7 whether or not a series
	// resolved, and that claim is exactly what the gates must not ignore.
	ctx.volume, ctx.statesVolume = titlerule.StatedVolume(title, ctx.series)
	if ctx.statesVolume && ctx.seriesRec != nil {
		ctx.memberAt, _ = workAtPosition(ctx.seriesRec, ctx.volume)
	}
	return ctx
}

// checkSeriesVolume applies gate 1: the submitted title states a known series and a
// volume number, and that series already holds a work at that position. It returns
// true when a terminal verdict has been set.
//
// The evidence is the CATALOGUE's, not the title's: the series must be one we hold
// (by name, matched at word boundaries with the audit's two-significant-word floor)
// and the position must be occupied by a work already recorded there. A title's
// number alone is famously unreliable - it is as often a part, an episode, a season
// or a collection index, which is why internal/audit files its own version of this
// shape as advisory - so the gate requires the number to name a slot the series
// actually fills, and reports rather than merges.
func (c *composer) checkSeriesVolume(ctx titleContext) bool {
	if !ctx.statesVolume || ctx.memberAt == "" {
		return false
	}
	member := c.works[ctx.memberAt]
	if member == nil {
		return false // a dangling membership; metacheck reports it
	}
	// A COLLECTION is not the volume it collects: "Bravelands: Books 1-3" claiming
	// position 1 is a boxed set beside book one, not a second record of it
	// (titlerule.IsCollection, the audit's multilingual rule, and the same veto
	// check.WorkIdentity.matches applies at the identity gate).
	if titlerule.IsCollection(ctx.title) != titlerule.IsCollection(member.Title) {
		return false
	}
	c.failDuplicateWork(ctx.memberAt, "the title states %q volume %s, and %s is already recorded at that position (%s) - "+
		"use the Add a recording form if this is another narration of it, or correct the title if it is a different book",
		ctx.seriesRec.Name, formatVolume(ctx.volume), ctx.memberAt,
		c.entryLocation(pack.FamilyWorks, ctx.memberAt, ""))
	return true
}

// decorationOutcome is what the intake gate does with one strip refusal.
type decorationOutcome struct {
	// reason is the contributor-facing phrase for a refusal that needs a MAINTAINER;
	// an empty reason means the submission proceeds with its title as submitted.
	reason string
}

// decorationRefusals maps EVERY titlerule strip-refusal code onto that decision, and
// the split is narrower than "refuse whatever cannot be stripped":
//
//   - a residual that names no book, or reads as a fragment, is needs-human. The
//     submitted title is decoration ("Book One", "Omnibus", "- Band 5"): we cannot
//     derive the book's name from it and neither can a mechanical pass, so a
//     maintainer titles it.
//   - every other refusal proceeds with the title as submitted. A title that IS its
//     series' name is perfectly ordinary (a one-book series is named after its book),
//     an omnibus whose clean form would BE the series name is a legitimate record,
//     and "there was nothing to strip" is not news. Refusing those would turn good
//     submissions away, which is the one failure mode a gate must not have.
//
// The table is exhaustive over titlerule.RefusalCodes() and
// TestEveryStripRefusalIsClassified pins that, so a refusal code added to the rule
// package cannot reach this gate with no decision recorded for it.
var decorationRefusals = map[string]decorationOutcome{
	titlerule.RefuseNothingToStrip:     {},
	titlerule.RefuseIsSeriesName:       {},
	titlerule.RefuseResultIsSeriesName: {},
	titlerule.RefuseNoIdentity:         {reason: "nothing that names a book"},
	titlerule.RefuseFragment:           {reason: "a fragment rather than a title"},
}

// checkDecoratedTitle applies gate 2. It returns the context with ctx.title set to
// the title the work should be composed under - the submitted one, or the cleaned
// one - and false when the submission cannot be composed at all (a terminal verdict
// has been set).
func (c *composer) checkDecoratedTitle(ctx titleContext) (titleContext, bool) {
	codes := titlerule.Decorations(titlerule.TitleFacts{
		Title:   ctx.title,
		Series:  ctx.series,
		Resolve: c.resolveSeries,
	})
	if len(codes) == 0 {
		return ctx, true
	}
	cleaned, refusal, ok := titlerule.StripDecoration(ctx.title, ctx.series)
	if !ok {
		if reason := decorationRefusals[refusal].reason; reason != "" {
			c.fail(StatusNeedsHuman, "Title %q is retailer decoration (%s) rather than the book's name, "+
				"and removing it leaves %s - a maintainer must decide what this work is called",
				ctx.title, strings.Join(codes, ", "), reason)
			return ctx, false
		}
		// Nothing safe to remove: the title stands as submitted. Silent on purpose -
		// "your title mentions its series" is not news, and the gates below still look
		// for the duplicate it might be.
		return ctx, true
	}
	if collides := c.works[slugify(cleaned)]; collides != nil && !ctx.sameBookAs(collides) {
		// The residual names a work we already hold that this submission is NOT a second
		// record of, so the title keeps the decoration that tells the two apart.
		// Stripping here would hand the submitter the slug gate's duplicate verdict for a
		// book we do not hold, which is the one failure a gate must not have - and that
		// gate is author-blind, so its message would not even be about their book.
		//
		// Two shapes reach it, and both were measured as wrong verdicts: a title stating
		// a volume nothing places ("Hammered, Book 7" on a form whose series fields are
		// blank cleans to "Hammered", and volume 7 of a serial we hold one volume of is
		// not that volume) and a COLLECTION ("Hammered: The Complete Boxed Set" cleans to
		// "Hammered", and a boxed set is not the book it collects).
		c.note("Title %q carries retailer decoration (%s), but %q is already a work this submission "+
			"is not a second record of - the title is kept as submitted so the two stay distinct",
			ctx.title, strings.Join(codes, ", "), cleaned)
		return ctx, true
	}
	c.note("Title %q carries retailer decoration (%s) - composed as %q instead, "+
		"so it cannot become a second record of a book already in the catalogue",
		ctx.title, strings.Join(codes, ", "), cleaned)
	ctx.title = cleaned
	return ctx, true
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
func (c *composer) checkNormalizedIdentity(ctx titleContext, lang string, authorSlugs []string) bool {
	if len(authorSlugs) == 0 {
		return false
	}
	authors := importer.ToSet(authorSlugs)
	matches := c.identityIndex().Match(ctx.title, ctx.series, lang, authors, authors)
	// The AMBIGUITY veto, the same one the bulk guard applies: a key naming several
	// catalogued works cannot say which of them a submission duplicates, so no verdict
	// may name one. The measured shape is a serial published under its bare series
	// name, where the tree holds two records that normalize alike and the submission
	// could be either or a third volume.
	if len(matches) != 1 {
		return false
	}
	m := matches[0]
	// The POSITION veto's POSITIVE half, the intake mirror of
	// internal/importer's seriesClaim.places: when the submitted title STATES which
	// volume it is, the catalogue must place the matched work at that very volume.
	// Gate 1 has already answered the confirmable case - a stated volume the series
	// really fills is a duplicate verdict naming the member - so what reaches here
	// stating a volume is a claim nothing confirms: a serial published under one title
	// ("Bravelands, Book 4"), or a marker on a form that resolved no series at all.
	// Both are books we may not hold, and silence is not agreement.
	if !ctx.placesMatch(m.Work.ID) {
		return false
	}
	c.failDuplicateWork(m.Work.ID, "%q normalizes to the same title and authors as the catalogued work %q (%q%s) at %s - "+
		"use the Add a recording form if this is another narration of it, or a correct-data form if its title needs fixing",
		ctx.title, m.Work.ID, m.Work.Title, againstSeries(m.Series), c.entryLocation(pack.FamilyWorks, m.Work.ID, ""))
	return true
}

// againstSeries renders the series a matched work's title was read against, or
// nothing at all when it was read against nothing. The distinction is evidence: a key
// derived against a MEMBERSHIP is a stronger claim than one derived against nothing.
//
// Message prose, in this package's verdict voice - internal/importer's guard renders
// the same clause for its aggregated warning. What both read is the one derivation
// (check.WorkIdentity.SeriesNameOf); only the sentence is local.
func againstSeries(series string) string {
	if series == "" {
		return ""
	}
	return ", cleaned against the series " + strconv.Quote(series)
}

// identityIndex is the submission's normalized-identity index: the one the catalogue
// LOAD already built (check.Result.Identity - its own duplicate census needs it), so
// no compose path pays for a second title-clean pass over the catalogue.
//
// It never returns nil: a load that produced no catalogue yields an empty index, so
// every gate reads as "nothing to collide with" rather than needing a nil check.
func (c *composer) identityIndex() *check.WorkIdentity {
	if c.identity == nil {
		c.identity = check.NewWorkIdentity(nil)
	}
	return c.identity
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
