package importer

import (
	"regexp"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// OnWorkSlugChain reports whether a catalogued work sits at a slug the CREATE path's
// candidate chain (newWorkChain) would probe or mint for its own title and credits:
// the cleaned title's slug, "<title>-<author>" for its first identity author (or, probed
// only, any other credited person), and "<title>-<author>-<n>" - each composed by
// workSlugAt, so a chain slug the importer had to SHORTEN to fit MaxSlugLen is
// recognized exactly as it was minted. The claim-dependent "book-<position>" candidates
// are left out: a work that states no position cannot be addressed by them.
//
// It is exported for internal/audit, which crowns the clean-titled member of a
// duplicate cluster as the survivor only when a re-import of that title would land on
// it - a slug the chain never mints ("emma-1996", "emma-1") is a hand-made address,
// and asking the chain itself rather than a restatement of its formula is what keeps
// the two from disagreeing about which is which.
//
// It reads the CLEANED title's chain (cleanWorkTitle, title qualifiers stripped), not
// resolveWork's qualified-title merge fallback; its one caller (internal/audit's
// cleanTwins) only asks it of an undecorated title, which the strip leaves unchanged.
func OnWorkSlugChain(w *model.Work) bool {
	chain, ok := ownTitleChain(w)
	if !ok {
		return false
	}
	for i := 0; ; i++ {
		c, ok := chain.at(i)
		if !ok {
			return false
		}
		if c.slug == w.ID {
			return true
		}
	}
}

// WorkTitleAlias answers the question a RETITLE raises: would a re-import of the
// work's own (new) title and credits still find it? A work's slug never moves (it is
// public identity), so a retitle can leave the record OFF the chain its title now
// composes, and the next import of that title would walk the chain, miss the record
// and mint a duplicate beside it.
//
// The answer is "" when the chain reaches w - a candidate IS its id, or a candidate is
// retired in reds onto it (a retired work candidate is judged as its survivor,
// tombstone.go) - and also when nothing could be minted (no authors, a title that
// slugs to nothing, a chain with no free candidate). Otherwise it is the slug the
// import would MINT at: the first create candidate that is neither held (live) nor
// retired, found by the same walk walkWorkChain makes, stopping where it stops.
// Recording that slug as a tombstone onto w (the retitle ALIAS: internal/repair's
// retitleWork and internal/issueform's title correction) makes the importer judge
// it as w, so the import merges instead of minting - exactly what it would have
// done had w been created under its new title.
//
// It judges no live candidate: whether a live record would absorb the row is the
// planner's identity question (authors, language, claim), so a same-author
// duplicate already on the chain can make the alias one no import reaches first.
// That costs nothing - the import merges into that duplicate, which is no new one.
//
// Like OnWorkSlugChain it walks the claim-FREE chain, and so it leaves a SERIAL
// VOLUME alone: a work at "<title>-book-<n>" (the serial pre-pass's slug,
// workidentity.go) sits there because its title cannot tell it from its siblings,
// it is reached by the rows stating its position through the position probes, and
// aliasing the bare title onto it would pull every claim-free row of that title
// into that one volume. A volume slug the pre-pass SHORTENED to fit its tail is one
// too (serialVolumeSlug). The series-scoped "<title>-<series>-book-<n>" shape is
// deliberately NOT matched: it is also exactly what a decorated title slugs to
// ("War: The Four Horsemen, Book 2" at war-the-four-horsemen-book-2, retitled
// "War"), where the title is the book's own and the alias is wanted.
func WorkTitleAlias(w *model.Work, live func(slug string) bool, reds model.Redirects) string {
	chain, ok := ownTitleChain(w)
	if !ok {
		return ""
	}
	if serialVolumeSlug(chain.base, w.ID) {
		return ""
	}
	free := ""
	for i := 0; ; i++ {
		c, ok := chain.at(i)
		if !ok {
			break
		}
		if c.slug == w.ID {
			return ""
		}
		to, retired := reds.Survivor(model.RedirectWorks, c.slug)
		if retired {
			if to == w.ID {
				return ""
			}
			continue
		}
		if live(c.slug) {
			continue
		}
		if chain.takeFree(i, c, &free) {
			break
		}
	}
	return free
}

// serialVolumeSlug reports whether id is the serial pre-pass's slug for a volume of
// the title whose slug is base: base followed by a "book-" tail, or - for a long
// title the pre-pass had to SHORTEN to fit the tail - exactly what posSuffixSlugs
// composes (model.BoundedSlugTail over "-book-<position>"), so "my-daughter-left-
// the-nest-and-book-11" is that title's volume 11 although it does not begin with
// the whole base.
//
// The shortened arm demands a POSITION tail (serialPositionTail) and nothing after
// it. Without that, any long slug carrying the word "book" read as a shortened
// volume: BoundedSlugTail reproduces a slug's own head whenever the "tail" is all
// the rest of it, so "read-people-like-a-book-how-to-analyze-..." retitled "Read
// People like a Book" (or "the-book-of-five-rings-by-miyamoto-musashi-..." retitled
// "The Book of Five Rings") lost the alias the retitle exists to record.
func serialVolumeSlug(base, id string) bool {
	// The whole base, then a position - possibly followed by the collision
	// suffix a later candidate carries ("the-saga-book-3-marty-book-2"). The
	// rest must OPEN with the position's number, or a title that merely
	// continues with the word "book" ("The Saga Book of Shadows" retitled "The
	// Saga") would be read as a volume and lose its alias.
	if rest, ok := strings.CutPrefix(id, base+"-"+serialTailPrefix); ok {
		return serialPositionHead.MatchString(rest)
	}
	i := strings.LastIndex(id, "-"+serialTailPrefix)
	if i < 0 || !serialPositionTail.MatchString(id[i+1+len(serialTailPrefix):]) {
		return false
	}
	return model.BoundedSlugTail(base, id[i:]) == id
}

// serialPositionTail matches what serialPositionSuffix puts after "book-" for a
// position the schema admits (series.schema.json, model.NormalizeSequence): a
// number, its decimal point slugged to a hyphen ("2.5" -> "2-5"), and an omnibus
// range spelled out with "-to-" ("1-3.5" -> "1-to-3-5").
var serialPositionTail = regexp.MustCompile(`^\d+(-\d+)?(-to-\d+(-\d+)?)?$`)

// serialPositionHead matches the start of a position tail that may be followed by
// more slug: a number, then the end or a hyphen.
var serialPositionHead = regexp.MustCompile(`^\d+(-|$)`)

// ownTitleChain is the create path's candidate chain for a catalogued work's own
// cleaned title and credits, with no position claim; ok is false when the work
// names no author or its title slugs to nothing (nothing is composed for either).
func ownTitleChain(w *model.Work) (workChain, bool) {
	if len(w.Authors) == 0 {
		return workChain{}, false
	}
	ts := slugOfTitle(cleanWorkTitle(w.Title))
	if ts.fellBack {
		return workChain{}, false
	}
	authors := workAuthors{all: w.Authors, identity: diskIdentityAuthorList(w.Authors, w.Credits)}
	return newWorkChain(ts, "", authors, positionClaim{}), true
}
