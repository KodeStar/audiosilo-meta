package audit

import (
	"fmt"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/titlerule"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// F-HYGIENE subclasses.
const (
	hygWorkNoLanguage    = "work-missing-language"
	hygWorkNoAuthors     = "work-missing-authors"
	hygWorkNoRecordings  = "work-without-recordings"
	hygRecNoNarrators    = "recording-missing-narrators"
	hygRecNoIdentifier   = "recording-without-identifier"
	hygSlugLeadArticle   = "slug-leading-article"
	hygSlugDoubledToken  = "slug-doubled-token"
	hygSlugArticleSeries = "slug-article-series-prefix"
)

// addedAtCounts is one entity kind's added_at spelling split. It is a COUNT-only
// advisory: the split is documented and expected (a plain date is what everything
// stamped at creation carries; a full RFC 3339 timestamp is what the storage
// migration's one-time git-history backfill wrote), so listing 280k works
// individually would be noise - the numbers are the whole finding.
type addedAtCounts struct {
	Date  int
	Stamp int
	None  int
}

// observe classifies one added_at value by its SHAPE alone. The schema has already
// accepted it (date_or_datetime), so the only question is which accepted spelling it
// is: a plain YYYY-MM-DD, or anything longer (a timestamp).
func (c *addedAtCounts) observe(v string) {
	switch {
	case v == "":
		c.None++
	case len(v) == len("2026-08-14"):
		c.Date++
	default:
		c.Stamp++
	}
}

// hygieneStats holds the count-only advisories.
type hygieneStats struct {
	Works      addedAtCounts
	Recordings addedAtCounts
	// RecordingCount and Chapters are the catalogue sizes the report's other
	// counts are read against.
	RecordingCount  int
	Chapters        int
	RecordingsNoRun int
}

// detectHygiene walks every work and recording for the field-level defects a bulk
// import leaves, and accumulates the count-only advisories alongside.
func detectHygiene(ix *index) (*findings, hygieneStats) {
	f := &findings{class: ClassHygiene}
	var st hygieneStats

	for _, w := range ix.cat.Works {
		// The citation is built LAZILY, inside add: it costs a workBrief (several
		// sorted slices and a membership render) and the overwhelming majority of
		// works have nothing wrong with them. Building it up front cost roughly
		// 1.5M allocations over the real tree to describe 2,854 findings.
		add := func(sub, field, have string, p Proposal) {
			p.Target, p.Field, p.From = w.ID, field, have
			f.add(Finding{
				Subclass: sub, Key: w.ID, Works: []WorkRef{ix.workBrief(w)}, Propose: p,
			})
		}
		if strings.TrimSpace(w.Language) == "" {
			add(hygWorkNoLanguage, "language", "", Proposal{Op: OpFillField,
				Reason: "state the work language; the schema requires it and search ranks on it"})
		}
		if len(w.Authors) == 0 {
			add(hygWorkNoAuthors, "authors", "", Proposal{Op: OpFillField,
				Reason: "credit the work's author(s); an authorless work is unreachable by author search and cannot be deduplicated by identity"})
		}
		if len(w.Recordings) == 0 {
			add(hygWorkNoRecordings, "recordings", "", Proposal{Op: OpReview, Advisory: true,
				Reason: "add the recording this work was imported from, or delete the work: a work with no recording has nothing to play"})
		}
		st.Works.observe(w.AddedAt)

		checkWorkSlug(ix, f, w)

		for _, r := range w.Recordings {
			st.RecordingCount++
			st.Chapters += len(r.Chapters)
			if r.RuntimeMin <= 0 {
				st.RecordingsNoRun++
			}
			st.Recordings.observe(r.AddedAt)

			addRec := func(sub, field, reason string) {
				f.add(Finding{
					Subclass: sub, Key: w.ID + "/" + r.ID, Recording: r.ID,
					Works: []WorkRef{ix.workBrief(w)},
					Propose: Proposal{
						Op: OpFillField, Target: r.ID, Field: field, Reason: reason,
					},
				})
			}
			if len(r.Narrators) == 0 {
				addRec(hygRecNoNarrators, "narrators",
					"credit the narrator(s); an uncredited narration cannot be told from an alternate one, which is how a "+
						"second production ends up merged into the first")
			}
			if len(r.ASIN) == 0 && len(r.ISBN) == 0 {
				addRec(hygRecNoIdentifier, "asin",
					"add the ASIN or ISBN: with neither, the recording answers no lookup?asin=/isbn= query and no import can "+
						"dedupe against it")
			}
		}
	}
	return f, st
}

// checkWorkSlug reports the slug-convention oddities a bulk import leaves. A slug
// is IDENTITY, so none of these proposes a rename: a rename moves the record and
// rewrites every series membership and sidecar that names it. They mark the records
// a rename pass should consider, which is what OpRenameCandidate says.
func checkWorkSlug(ix *index, f *findings, w *model.Work) {
	add := func(sub, want, reason string, notes ...string) {
		f.add(Finding{
			Subclass: sub, Key: w.ID, Works: []WorkRef{ix.workBrief(w)},
			Propose: Proposal{
				Op: OpRenameCandidate, Target: w.ID, Field: "id", From: w.ID, To: want,
				Advisory: true, Reason: reason,
			},
			Notes: notes,
		})
	}
	// A dangling leading article: the slug leads with one the title does not.
	if art, ok := titlerule.LeadingSlugArticle(w.ID); ok && !titlerule.TitleStartsWith(w.Title, art) {
		add(hygSlugLeadArticle, strings.TrimPrefix(w.ID, art+"-"),
			"the slug leads with an article the title does not, so nothing probing by title will find it")
	}
	// The retailer shape the calibration record carries: "A <series name>: <real
	// title>", where the leading article belongs to nothing and rides into the slug
	// at the front of it. The tuple is on the derivation, computed once per work.
	if d := ix.derived(w); d.artOK {
		add(hygSlugArticleSeries, model.Slugify(d.artRest),
			"the title prefixes the work with an article and its SERIES name, so the slug leads with a dangling article and "+
				"repeats the series",
			fmt.Sprintf("title reads %q + series %q + %q", d.artArticle, d.artSeries, d.artRest),
			"see W-TITLE for the title proposal and W-NOSERIES for the membership")
	}
	if frag, ok := titlerule.DoubledSlugToken(w.ID); ok {
		add(hygSlugDoubledToken, "",
			"a token run repeats immediately, the shape a title that restates its own series or subtitle produces",
			"repeated run: "+frag)
	}
}
