package importer

import (
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
// Recording that slug as a tombstone onto w (the retitle ALIAS, internal/repair's
// retitleWork) makes the importer judge it as w, so the import merges instead of
// minting - exactly what it would have done had w been created under its new title.
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
// into that one volume. The series-scoped "<title>-<series>-book-<n>" shape is
// deliberately NOT matched: it is also exactly what a decorated title slugs to
// ("War: The Four Horsemen, Book 2" at war-the-four-horsemen-book-2, retitled
// "War"), where the title is the book's own and the alias is wanted.
func WorkTitleAlias(w *model.Work, live func(slug string) bool, reds model.Redirects) string {
	chain, ok := ownTitleChain(w)
	if !ok {
		return ""
	}
	if strings.HasPrefix(w.ID, chain.base+"-"+serialTailPrefix) {
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
