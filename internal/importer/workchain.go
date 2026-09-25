package importer

import "github.com/kodestar/audiosilo-meta/pkg/model"

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
func OnWorkSlugChain(w *model.Work) bool {
	if len(w.Authors) == 0 {
		return false
	}
	ts := slugOfTitle(cleanWorkTitle(w.Title))
	if ts.fellBack {
		return false
	}
	authors := workAuthors{all: w.Authors, identity: diskIdentityAuthorList(w.Authors, w.Credits)}
	chain := newWorkChain(ts, "", authors, positionClaim{})
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
