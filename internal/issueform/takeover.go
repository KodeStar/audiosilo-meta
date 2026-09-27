package issueform

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/internal/titlerule"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// takeover.go is the intake side of the TRUST-TIER user-overwrite rule
// (LICENSING.md, "Trust tiers and the user-overwrite rule"; GOVERNANCE.md,
// "Overwriting an existing record").
//
// When an add-work or add-recording submission names, BY ASIN, a recording
// that is still nothing but a libex mirror seed, the submitter is the first
// person to attest it, and their facts replace the mirror's. The compose paths
// only create records, so the takeover is not composed here: it is applied by
// importer.AttestAt, the bulk importer's own attestation, over the store this
// composer already opened - what that applies and refuses is documented there,
// once. This file only decides WHEN (an ASIN match on a mirror-only recording,
// and nothing weaker) and turns the importer's Summary into a verdict.

// takeover is a planned attestation: the ASIN gate found the submission naming a
// recording that is still bulk-mirror-only.
type takeover struct {
	ref   recRef
	asin  string
	isbns []model.ISBNRef
}

// planTakeover is what the ASIN gate returns instead of a duplicate verdict when
// the recording an ASIN names is still bulk-mirror-only. It refuses (nil, verdict
// set) the one shape the rule does not cover: a submission stating a SECOND ASIN
// the record does not carry, because an attestation never adds an identifier and
// dropping the submitter's ASIN silently would lose a fact.
func (c *composer) planTakeover(ref recRef, asin string, asins []outASIN, isbns []model.ISBNRef) *takeover {
	var extra []string
	for _, a := range asins {
		if c.asinRec[a.ASIN] != ref {
			extra = append(extra, a.ASIN)
		}
	}
	if len(extra) > 0 {
		c.fail(StatusNeedsHuman, "%s, so your submission would replace what is recorded there - but it also "+
			"states ASIN(s) %s, which that recording does not carry. Taking a record over never adds an identifier "+
			"to it, so a maintainer decides where those belong; or edit the issue to state only %s and the bot will "+
			"apply your data over the seed", mirrorSeedLead(c.recLocation(ref)), strings.Join(extra, ", "), asin)
		return nil
	}
	return &takeover{ref: ref, asin: asin, isbns: isbns}
}

// applyTakeover applies a planned takeover through importer.AttestAt and sets
// the verdict: ok is the importer having written the tree (c.wrote, which
// Process keys on), anything else is needs-human. genres are the add-work
// form's (validated vocabulary values; nil on add-recording) and namedWork is
// the work an add-recording form names ("" on add-work).
func (c *composer) applyTakeover(s sections, t *takeover, genres []string, namedWork string) {
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
	var isbns, taken []string
	for _, ref := range t.isbns {
		isbns = append(isbns, ref.ISBN)
		if _, recorded := c.isbnRec[isbnKey(ref.ISBN)]; recorded {
			taken = append(taken, ref.ISBN)
		}
	}
	unapplied, identity := c.outsideTakeover(s, t)
	// Nothing below reads the catalogue again, and the importer's post-write
	// validation loads its own.
	c.releaseCatalogue()

	sum, err := importer.AttestAt(c.store, t.ref, taken, importer.Attestation{
		ASIN: t.asin,
		// The submission's `user` entry, which sources() always puts last; a
		// typed libex marker entry (provenance.go) names the ASIN, and the seed
		// already carries that one.
		Source:     srcs[len(srcs)-1],
		RuntimeMin: runtimeMin, ReleaseDate: releaseDate, CoverURL: coverURL,
		Publisher: s.get(fRecPublisher), ISBNs: isbns, Genres: genres,
	}, importer.Options{DataDir: c.dataDir, Profile: c.profile, ImportDate: c.date})
	switch {
	case err != nil:
		// The submission is fine; the tree it produced is not, which is the bot's
		// problem rather than the submitter's.
		c.fail(StatusNeedsHuman, "applying your submission over %s (seeded from the libex mirror) failed: %v", loc, err)
		return
	case sum.Conflicts > 0:
		c.fail(StatusNeedsHuman, "%s, so your submission would replace what is recorded there - but it disagrees "+
			"with the recorded runtime or release date (a runtime more than 10%% apart, or a date that is not the "+
			"same date at another precision, can mean a different production), and that is never overwritten "+
			"mechanically: a maintainer adjudicates. Nothing was applied", mirrorSeedLead(loc))
		c.noteAttestWarnings(sum)
		return
	case sum.AttestedRecordings == 0:
		c.fail(StatusNeedsHuman, "your submission names %s by ASIN %s, but the takeover applied nothing there; "+
			"a maintainer checks why", loc, t.asin)
		c.noteAttestWarnings(sum)
		return
	}
	c.note("%s: the facts your submission states replaced the mirror's, facts it leaves blank keep the mirror's "+
		"values, and the record is now attested by this submission (LICENSING.md, \"Trust tiers and the "+
		"user-overwrite rule\")", mirrorSeedLead(loc))
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

// noteAttestWarnings echoes the importer's per-row lines for the one row it
// planned - a conflict's two values, a refused ISBN.
func (c *composer) noteAttestWarnings(sum importer.Summary) {
	for _, w := range sum.RowWarnings() {
		c.note("import warning: %s", w)
	}
}

// takeoverUnapplied are the form fields a takeover does not carry, in form
// order: work-level and regional facts the attestation never writes, named in
// the verdict rather than dropped silently.
var takeoverUnapplied = []string{
	fWorkSubtitle, fWorkFirstPublished, fWorkSeriesName, fWorkSeriesPosition,
	fWorkISBN, fWorkWikidata, fWorkOpenLibrary, fRecPublishers,
}

// outsideTakeover reports the stated fields the takeover leaves alone: unapplied
// are facts the form states that the attestation does not write, identity the
// identity fields that differ from the record's (never rewritten by a takeover),
// judged by the catalogue's own identity rules - the author-nesting rule
// (check.IdentityAuthorsMatch) and the title comparison key with retailer
// decoration stripped. It reads the composer's catalogue, so it runs before
// releaseCatalogue.
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
	rec := c.recordingAt(t.ref)
	if rec == nil {
		return unapplied, nil
	}
	if stated := abridgedFromForm(s.get(fRecAbridged)); stated != nil && *stated != rec.Abridged {
		unapplied = append(unapplied, fRecAbridged)
	}
	w := c.works[t.ref.Work]
	if title := s.get(fWorkTitle); title != "" && titleKey(title, s.get(fWorkSeriesName)) != titleKey(w.Title, "") {
		identity = append(identity, fWorkTitle)
	}
	if names := s.get(fWorkAuthors); names != "" {
		if authors := c.personSlugs(splitNames(names)); !check.IdentityAuthorsMatch(w, authors, authors) {
			identity = append(identity, fWorkAuthors)
		}
	}
	if !importer.SameSet(c.personSlugs(splitNarratorNames(s.get(fRecNarrators))), importer.ToSet(rec.Narrators)) {
		identity = append(identity, fRecNarrators)
	}
	return unapplied, identity
}

// titleKey is a title's comparison identity: retailer decoration stripped where
// that is safe (titlerule.StripDecoration, the gate the add-work form applies),
// then titlerule.CompareKeyWhole.
func titleKey(title, series string) string {
	if cleaned, _, ok := titlerule.StripDecoration(title, series); ok {
		title = cleaned
	}
	return titlerule.CompareKeyWhole(title)
}

// releaseCatalogue drops the composer's whole-catalogue state - the dedup maps
// and the loaded catalogue - once nothing more will be asked of it. The store
// stays: the takeover writes through it.
func (c *composer) releaseCatalogue() {
	c.works, c.series, c.asinRec, c.isbnRec = nil, nil, nil, nil
	c.catalog, c.identity, c.seriesAuthors, c.personName = nil, nil, nil, nil
}

// personSlugs is the read-only slug set of a list of person names
// (personSlugOf), for comparing a submission's credits with a record's before
// anything is composed.
func (c *composer) personSlugs(names []string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, n := range names {
		if slug, ok := c.personSlugOf(n); ok {
			out[slug] = true
		}
	}
	return out
}

// recordingASINs lists the ASINs of the catalogued recording at ref, each with
// its region, for a message a submitter acts on.
func (c *composer) recordingASINs(ref recRef) string {
	r := c.recordingAt(ref)
	if r == nil {
		return ""
	}
	return strings.Join(asinLabels(r), ", ")
}

// workASINs lists the ASINs of every recording of w, capped so a work with a
// dozen regional recordings still gets a readable sentence.
func workASINs(w *model.Work) string {
	var out []string
	for _, r := range w.Recordings {
		out = append(out, asinLabels(r)...)
	}
	sort.Strings(out)
	return joinCapped(out, 6)
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
