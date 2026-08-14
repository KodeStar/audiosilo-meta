package audit

import (
	"fmt"
	"strings"

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

// hygieneStats holds the subclasses that are COUNTS rather than records. The
// added_at split is the documented, expected one (a plain date for everything
// stamped at creation, an RFC 3339 timestamp for the records the storage
// migration's one-time git-history backfill dated), so listing 280k works
// individually would be noise - the numbers are the whole finding.
type hygieneStats struct {
	WorkAddedDate   int
	WorkAddedStamp  int
	WorkAddedNone   int
	RecAddedDate    int
	RecAddedStamp   int
	RecAddedNone    int
	Recordings      int
	Chapters        int
	RecordingsNoRun int
}

// detectHygiene walks every work and recording for the field-level defects a bulk
// import leaves, and accumulates the count-only advisories alongside.
func detectHygiene(ix *index) (*findings, hygieneStats) {
	f := &findings{class: ClassHygiene}
	var st hygieneStats

	for _, w := range ix.cat.Works {
		brief := ix.workBrief(w)
		add := func(sub, field, have, action string, notes ...string) {
			f.add(Finding{
				Subclass: sub, Key: w.ID, Works: []WorkRef{brief},
				Field: field, Have: have, Action: action, Notes: notes,
			})
		}
		if strings.TrimSpace(w.Language) == "" {
			add(hygWorkNoLanguage, "language", "", "state the work language; the schema requires it and search ranks on it")
		}
		if len(w.Authors) == 0 {
			add(hygWorkNoAuthors, "authors", "", "credit the work's author(s); an authorless work is unreachable by author search and cannot be deduplicated by identity")
		}
		if len(w.Recordings) == 0 {
			add(hygWorkNoRecordings, "recordings", "", "add the recording this work was imported from, or delete the work: a work with no recording has nothing to play")
		}
		switch addedAtShape(w.AddedAt) {
		case addedDate:
			st.WorkAddedDate++
		case addedStamp:
			st.WorkAddedStamp++
		default:
			st.WorkAddedNone++
		}

		checkWorkSlug(ix, f, w, brief)

		for _, r := range w.Recordings {
			st.Recordings++
			st.Chapters += len(r.Chapters)
			if r.RuntimeMin <= 0 {
				st.RecordingsNoRun++
			}
			switch addedAtShape(r.AddedAt) {
			case addedDate:
				st.RecAddedDate++
			case addedStamp:
				st.RecAddedStamp++
			default:
				st.RecAddedNone++
			}
			if len(r.Narrators) == 0 {
				f.add(Finding{
					Subclass: hygRecNoNarrators, Key: w.ID + "/" + r.ID, Recording: r.ID,
					Works: []WorkRef{brief}, Field: "narrators", Have: "",
					Action: "credit the narrator(s); an uncredited narration cannot be told from an alternate one, which is how a second production ends up merged into the first",
				})
			}
			if len(r.ASIN) == 0 && len(r.ISBN) == 0 {
				f.add(Finding{
					Subclass: hygRecNoIdentifier, Key: w.ID + "/" + r.ID, Recording: r.ID,
					Works: []WorkRef{brief}, Field: "asin", Have: "",
					Action: "add the ASIN or ISBN: with neither, the recording answers no lookup?asin=/isbn= query and no import can dedupe against it",
				})
			}
		}
	}
	return f, st
}

// checkWorkSlug reports the slug-convention oddities a bulk import leaves. A slug
// is IDENTITY, so none of these proposes a rename on its own - a rename moves the
// record and rewrites every series membership and sidecar that names it. They mark
// the records a rename pass should consider.
func checkWorkSlug(ix *index, f *findings, w *model.Work, brief WorkRef) {
	// A dangling leading article: the slug leads with one the title does not.
	if art, ok := leadingSlugArticle(w.ID); ok && !titleStartsWith(w.Title, art) {
		f.add(Finding{
			Subclass: hygSlugLeadArticle, Key: w.ID, Works: []WorkRef{brief},
			Field: "id", Have: w.ID, Want: strings.TrimPrefix(w.ID, art+"-"),
			Action: "candidate for a rename pass: the slug leads with an article the title does not, so nothing probing by title will find it",
		})
	}
	// The retailer shape the calibration record carries: "A <series name>: <real
	// title>", where the leading article belongs to nothing and rides into the
	// slug at the front of it.
	if art, series, rest, ok := articleSeriesPrefix(w.Title, ix.seriesFormIn); ok {
		f.add(Finding{
			Subclass: hygSlugArticleSeries, Key: w.ID, Works: []WorkRef{brief},
			Field: "id", Have: w.ID, Want: model.Slugify(rest),
			Action: "candidate for a rename pass: the title prefixes the work with an article and its SERIES name, so the slug leads with a dangling article and repeats the series",
			Notes: []string{
				fmt.Sprintf("title reads %q + series %q + %q", art, series, rest),
				"see W-TITLE for the title proposal and W-NOSERIES for the membership",
			},
		})
	}
	if frag, ok := doubledSlugToken(w.ID); ok {
		f.add(Finding{
			Subclass: hygSlugDoubledToken, Key: w.ID, Works: []WorkRef{brief},
			Field: "id", Have: w.ID,
			Action: "candidate for a rename pass: a token run repeats immediately, the shape a title that restates its own series or subtitle produces",
			Notes:  []string{"repeated run: " + frag},
		})
	}
}

// leadingSlugArticle reports the article a slug leads with, if any.
func leadingSlugArticle(slug string) (string, bool) {
	for _, art := range []string{"the", "an", "a"} {
		if strings.HasPrefix(slug, art+"-") {
			return art, true
		}
	}
	return "", false
}

// titleStartsWith reports whether a title's first alphanumeric WORD is art.
//
// The word, not the whitespace token: "A.A. Milne's Winnie-the-Pooh" slugs to
// `a-a-milnes-...`, and its first token "A.A." folds to "a-a", which is not the
// article "a" - so a token comparison called the slug's leading `a-` dangling when
// it is an initial. Reading the same words the slug is built from is what makes the
// two agree.
func titleStartsWith(title, art string) bool {
	ws := lowerWords(strings.ToLower(model.Slugify(title)))
	if len(ws) == 0 {
		return false
	}
	return ws[0].word == art
}

// doubledSlugToken reports an immediately repeated token run in a slug - one token
// ("italian-italian") or a pair ("iron-druid-iron-druid"). Returns the repeated run.
//
// A run must carry a LETTER and, for the single-token case, be at least three
// characters. Both bounds are there because a slug's numbers repeat innocently: a
// title with a grouped number slugs to `10-000-000-marriage-proposal`, and an
// initials pair to `a-a-milnes-...`, neither of which is a title restating itself.
func doubledSlugToken(slug string) (string, bool) {
	toks := strings.Split(slug, "-")
	for i := 0; i+1 < len(toks); i++ {
		if toks[i] == toks[i+1] && len(toks[i]) >= 3 && hasLetter(toks[i]) {
			return toks[i], true
		}
	}
	for i := 0; i+3 < len(toks); i++ {
		if toks[i] == toks[i+2] && toks[i+1] == toks[i+3] &&
			hasLetter(toks[i]) && hasLetter(toks[i+1]) {
			return toks[i] + "-" + toks[i+1], true
		}
	}
	return "", false
}

// hasLetter reports whether s holds an ASCII letter. Slug tokens are ASCII by
// construction (model.Slugify), so this is the whole test.
func hasLetter(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 'a' && s[i] <= 'z' {
			return true
		}
	}
	return false
}

// addedAt shapes.
const (
	addedNone  = 0
	addedDate  = 1 // YYYY-MM-DD, what everything stamped at creation carries
	addedStamp = 2 // a full RFC 3339 timestamp, what the one-time backfill wrote
)

// addedAtShape classifies an added_at value by its SHAPE alone. The schema has
// already accepted it (date_or_datetime), so the only question here is which of
// the two accepted spellings it is.
func addedAtShape(v string) int {
	switch {
	case v == "":
		return addedNone
	case len(v) == len("2026-08-14"):
		return addedDate
	default:
		return addedStamp
	}
}
