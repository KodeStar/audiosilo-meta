package issueform

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// takeover.go is the intake side of the TRUST-TIER user-overwrite rule
// (LICENSING.md, "Trust tiers and the user-overwrite rule"; GOVERNANCE.md,
// "Overwriting an existing record").
//
// A record seeded from the libex mirror states real facts that nobody has
// attested. When an add-work or add-recording submission names one of its
// recordings BY ASIN, the submitter is the first person to say "this is my book
// and these are its details", so their stated facts replace the mirror's and
// their submission takes over the record's provenance. The compose paths only
// create records, so the takeover is not composed here at all: it is handed to
// the bulk importer's own attestation (importer.Attest), which plans the
// submission as a one-row user-library run through the hook every library
// import takes at its ASIN-dedup skip. One implementation of the rule, two doors.
//
// What that buys, all of it the importer's and none of it restated here:
//
//   - the stated facts (runtime, release date, publisher, cover, recording
//     ISBNs) replace the recorded ones; a fact the form leaves blank keeps the
//     mirror's value, because silence is not an assertion;
//   - genres are ADDED to the work's set, never replacing it (rule 5);
//   - the submission's `user` entry is appended to the recording and, when the
//     work is a mirror seed too, to the work: both are attested from then on,
//     and every later submission meets the ordinary first-writer-wins rules;
//   - a runtime more than 10% apart, or a release date that is not the same date
//     at another precision, refuses the submission whole and writes nothing - the
//     disagreement GOVERNANCE.md routes to a maintainer (data:needs-human);
//   - identity (title, authors, narrators, the identifier sets), abridged and
//     added_at are never rewritten.
//
// Only an ASIN match takes a record over, because that is what the rule says. A
// title, an ISBN or a narrator set can meet a record of another edition - or
// another book - and those collisions go to a maintainer with a message saying
// which it was (failMirrorSeed, failAnotherAuthorsSeed).

// takeover is a planned attestation: the ASIN gate found the submission naming a
// recording that is still bulk-mirror-only.
type takeover struct {
	ref   recRef
	asin  string
	isbns []model.ISBNRef
}

// planTakeover is what the ASIN gate does instead of a duplicate verdict when
// the recording an ASIN names is still bulk-mirror-only. It refuses the one
// shape the rule does not cover: a submission stating a SECOND ASIN the record
// does not carry, because an attestation never adds an identifier and dropping
// the submitter's ASIN silently would lose a fact.
func (c *composer) planTakeover(ref recRef, asin string, asins []outASIN, isbns []model.ISBNRef) {
	var extra []string
	for _, a := range asins {
		if a.ASIN == asin {
			continue
		}
		if other, ok := c.asinRec[a.ASIN]; ok && other == ref {
			continue
		}
		extra = append(extra, a.ASIN)
	}
	if len(extra) > 0 {
		c.fail(StatusNeedsHuman, "%s was seeded from the libex mirror and no user has attested it yet, so your "+
			"submission would replace what is recorded there - but it also states ASIN(s) %s, which that recording "+
			"does not carry. Taking a record over never adds an identifier to it, so a maintainer decides where those "+
			"belong; or edit the issue to state only %s and the bot will apply your data over the seed",
			c.recLocation(ref), strings.Join(extra, ", "), asin)
		return
	}
	c.takeover = &takeover{ref: ref, asin: asin, isbns: isbns}
}

// applyTakeover applies a planned takeover through importer.Attest and sets the
// verdict. It is a no-op unless the ASIN gate planned one, so a compose path
// calls it wherever that gate stopped. genres are the add-work form's
// (validated vocabulary values; nil on add-recording) and namedWork is the work
// an add-recording form names ("" on add-work).
func (c *composer) applyTakeover(s sections, genres []string, namedWork string) {
	t := c.takeover
	if t == nil || c.failed() {
		return
	}
	loc := c.recLocation(t.ref)
	// An add-recording form names its work; an ASIN recorded under a DIFFERENT
	// work means the submission and the catalogue disagree about what the book
	// is, which no attestation can settle.
	if namedWork != "" && namedWork != t.ref.Work {
		c.fail(StatusNeedsHuman, "ASIN %s is recorded at %s, a recording of %q - not of the work this submission "+
			"names (%q); a maintainer checks which is right before anything is applied", t.asin, loc, t.ref.Work, namedWork)
		return
	}
	runtimeMin, releaseDate, coverURL := c.recordingFacts(s)
	srcs := c.sources(s.get(fSources))
	isbns := make([]string, 0, len(t.isbns))
	for _, ref := range t.isbns {
		isbns = append(isbns, ref.ISBN)
	}
	unapplied, identity := c.outsideTakeover(s, t)
	title := s.get(fWorkTitle)
	if title == "" {
		title = t.ref.Work
	}
	// The composer's catalogue is dropped before the importer loads its own: the
	// two would otherwise both be live for the whole run.
	c.releaseCatalogue()

	sum, err := importer.Attest(importer.Attestation{
		ASIN: t.asin, Title: title,
		// The submission's `user` entry, which sources() always puts last; a
		// typed libex marker entry (provenance.go) names the ASIN, and the seed
		// already carries that one.
		Source:     srcs[len(srcs)-1],
		RuntimeMin: runtimeMin, ReleaseDate: releaseDate, CoverURL: coverURL,
		Publisher: s.get(fRecPublisher), ISBNs: isbns, Genres: genres,
	}, importer.Options{DataDir: c.dataDir, Profile: c.profile, ImportDate: c.date})
	if err != nil {
		// The submission is fine; the tree it produced is not, which is the bot's
		// problem rather than the submitter's.
		c.fail(StatusNeedsHuman, "applying your submission over %s (seeded from the libex mirror) failed: %v", loc, err)
		return
	}
	switch {
	case sum.Conflicts > 0:
		c.fail(StatusNeedsHuman, "%s was seeded from the libex mirror and no user has attested it yet, so your "+
			"submission would replace what is recorded there - but it disagrees with the recorded runtime or release "+
			"date (a runtime more than 10%% apart, or a date that is not the same date at another precision, can mean "+
			"a different production), and that is never overwritten mechanically: a maintainer adjudicates. Nothing "+
			"was applied", loc)
		c.noteAttestWarnings(sum)
	case sum.AttestedRecordings == 0:
		c.fail(StatusNeedsHuman, "your submission names %s by ASIN %s, but the takeover applied nothing there; "+
			"a maintainer checks why", loc, t.asin)
		c.noteAttestWarnings(sum)
	default:
		c.status = StatusOK
		c.note("%s was seeded from the libex mirror and no user had attested it: the facts your submission states "+
			"replaced the mirror's, facts it leaves blank keep the mirror's values, and the record is now attested by "+
			"this submission (LICENSING.md, \"Trust tiers and the user-overwrite rule\")", loc)
		if sum.AttestedWorks > 0 {
			c.note("its work %q was a mirror seed too, and is attested by this submission as well", t.ref.Work)
		}
		if len(genres) > 0 {
			c.note("your genres were added to the work's genres rather than replacing them")
		}
		if len(unapplied) > 0 {
			c.note("not applied - a takeover replaces a recording's facts, not the rest of the form: %s. "+
				"The Correct data form adds these", strings.Join(unapplied, ", "))
		}
		if len(identity) > 0 {
			c.note("your %s differ from the record's, and a takeover never rewrites identity; if the record is "+
				"wrong, the Correct data form fixes it", strings.Join(identity, ", "))
		}
		c.noteAttestWarnings(sum)
		c.wrote = sum.Files
	}
}

// noteAttestWarnings echoes the importer's per-row lines for the one row it
// planned - a conflict's two values, a refused ISBN. The run-level lines are
// about the catalogue as a whole (a pre-existing ASIN collision, say) and are
// not this submission's news.
func (c *composer) noteAttestWarnings(sum importer.Summary) {
	for _, w := range sum.RowWarnings() {
		c.note("import warning: %s", w)
	}
}

// takeoverUnapplied are the form fields a takeover does not carry, in form
// order. The importer's attestation reaches a recording's facts and a work's
// genres; everything else here is a work-level or regional fact nothing in it
// writes, so the verdict names what was left behind rather than dropping it
// silently.
var takeoverUnapplied = []string{
	fWorkSubtitle, fWorkFirstPublished, fWorkSeriesName, fWorkSeriesPosition,
	fWorkISBN, fWorkWikidata, fWorkOpenLibrary, fRecPublishers,
}

// outsideTakeover reports the stated fields the takeover leaves alone: unapplied
// are facts the form states that the attestation does not write, identity the
// identity fields that differ from the record's (never rewritten by a takeover).
// It reads the composer's catalogue, so it runs before releaseCatalogue.
func (c *composer) outsideTakeover(s sections, t *takeover) (unapplied, identity []string) {
	for _, label := range takeoverUnapplied {
		if s.get(label) != "" {
			unapplied = append(unapplied, label)
		}
	}
	for _, ref := range t.isbns {
		if ref.Region != "" {
			unapplied = append(unapplied, "the region of ISBN "+ref.ISBN)
		}
	}
	w := c.works[t.ref.Work]
	if w == nil {
		return unapplied, nil
	}
	var rec *model.Recording
	for _, r := range w.Recordings {
		if r.ID == t.ref.Rec {
			rec = r
		}
	}
	if stated := abridgedFromForm(s.get(fRecAbridged)); stated != nil && rec != nil && *stated != rec.Abridged {
		unapplied = append(unapplied, fRecAbridged)
	}
	if title := s.get(fWorkTitle); title != "" && slugify(title) != slugify(w.Title) {
		identity = append(identity, fWorkTitle)
	}
	if names := s.get(fWorkAuthors); names != "" && !sameSlugSet(c.personSlugs(splitNames(names)), w.Authors) {
		identity = append(identity, fWorkAuthors)
	}
	if rec != nil && !sameSlugSet(c.personSlugs(splitNarratorNames(s.get(fRecNarrators))), rec.Narrators) {
		identity = append(identity, fRecNarrators)
	}
	return unapplied, identity
}

// releaseCatalogue drops the composer's whole-catalogue state - the dedup maps,
// the loaded catalogue and the store that cached every pack it read - once
// nothing more will be asked of it.
func (c *composer) releaseCatalogue() {
	c.works, c.series, c.asinRec, c.isbnRec = nil, nil, nil, nil
	c.catalog, c.identity, c.seriesAuthors = nil, nil, nil
	c.store = nil
}

// failAnotherAuthorsSeed is the work-SLUG gate's answer for the one collision
// that is not a duplicate at all: the title's slug is held by a mirror-seeded
// work none of whose authors the submission names. That is another book with the
// same title, so "your submission should replace what is recorded there" would
// be exactly wrong - it would put one book's facts on another's record. The form
// composes a work only at a free slug, so a maintainer composes this one under a
// disambiguated slug. It reports whether it set the verdict.
//
// It asks only of a mirror-seed incumbent: an attested one keeps the gate's
// long-standing duplicate verdict, which this change does not reopen.
func (c *composer) failAnotherAuthorsSeed(live, slug string, authorNames []string) bool {
	w := c.works[live]
	if w == nil || !model.BulkMirrorOnly(w.Sources) {
		return false
	}
	submitted := c.personSlugs(authorNames)
	if len(submitted) == 0 {
		return false // nothing to compare: the ordinary verdict stands
	}
	for _, a := range w.Authors {
		if submitted[a] {
			return false
		}
	}
	c.fail(StatusNeedsHuman, "the work slug %q is held by a different book - %q by %s, at %s, seeded from the libex "+
		"mirror - and your submission names none of its authors, so it is neither a duplicate of that record nor a "+
		"takeover of it: nothing there is yours to replace. The intake bot composes a new work only at a free slug, "+
		"so a maintainer composes this book under a disambiguated one",
		slug, w.Title, c.personNames(w.Authors), c.entryLocation(pack.FamilyWorks, live, ""))
	return true
}

// personNames renders person ids as their recorded names, joined for a message;
// an id with no record is shown as itself.
func (c *composer) personNames(ids []string) string {
	want := make(map[string]string, len(ids))
	for _, id := range ids {
		want[id] = id
	}
	if c.catalog != nil {
		for _, p := range c.catalog.People {
			if _, ok := want[p.ID]; ok {
				want[p.ID] = p.Name
			}
		}
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, want[id])
	}
	return strings.Join(out, ", ")
}

// personSlugs is the READ-ONLY slug set of a list of person names - the slug each
// would resolve to, a retired one read as its survivor, without creating a
// record - for the gate and the notes that compare a submission's credits with a
// record's before anything is composed.
func (c *composer) personSlugs(names []string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, n := range names {
		slug, fellBack := model.PersonSlug(n)
		if fellBack {
			continue
		}
		if to, retired := c.redirects.Survivor(model.RedirectPeople, slug); retired {
			slug = to
		}
		out[slug] = true
	}
	return out
}

// sameSlugSet reports whether a slug set holds exactly ids.
func sameSlugSet(set map[string]bool, ids []string) bool {
	return importer.SameSet(set, importer.ToSet(ids))
}

// recordingASINs lists the ASINs of the catalogued recording at ref, each with
// its region, for a message a submitter acts on.
func (c *composer) recordingASINs(ref recRef) []string {
	w := c.works[ref.Work]
	if w == nil {
		return nil
	}
	for _, r := range w.Recordings {
		if r.ID == ref.Rec {
			return asinLabels(r)
		}
	}
	return nil
}

// maxNamedASINs bounds how many ASINs a message lists for a work: a work with a
// dozen regional recordings would otherwise bury the sentence.
const maxNamedASINs = 6

// workASINs lists the ASINs of every recording of w, bounded by maxNamedASINs.
func workASINs(w *model.Work) []string {
	var out []string
	for _, r := range w.Recordings {
		out = append(out, asinLabels(r)...)
	}
	sort.Strings(out)
	if len(out) > maxNamedASINs {
		out = append(out[:maxNamedASINs], fmt.Sprintf("and %d more", len(out)-maxNamedASINs))
	}
	return out
}

// asinLabels renders a recording's ASINs as "B0... (us)".
func asinLabels(r *model.Recording) []string {
	out := make([]string, 0, len(r.ASIN))
	for _, a := range r.ASIN {
		out = append(out, fmt.Sprintf("%s (%s)", a.ASIN, a.Region))
	}
	return out
}

// matchedByNarrators is the narrator-set gate's "how" for failMirrorSeed: the
// narrators alone, or the narrators when the submission's own ASINs are not on
// the record (a regional re-release, or a different edition).
func matchedByNarrators(asins []outASIN) string {
	if len(asins) == 0 {
		return "its narrators"
	}
	labels := make([]string, 0, len(asins))
	for _, a := range asins {
		labels = append(labels, a.ASIN)
	}
	return fmt.Sprintf("its narrators (the ASIN(s) you gave, %s, are not recorded on it)", strings.Join(labels, ", "))
}
