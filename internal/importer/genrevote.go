package importer

import "slices"

// genrevote.go is the RECORDING VOTE, the rule of record for what a MIRROR-derived
// genre set is.
//
// A work's genres come from Audible's category nodes, one row (one ASIN) at a
// time. A work with many recordings from different publishers is tagged by each
// publisher separately, and a union of every tagging accumulates each one's
// mistakes: one BBC ASIN filed Five Little Pigs under Westerns, and one Colonial
// Radio Theatre ASIN gave The Marvelous Land of Oz education, parenting and
// arts-entertainment. So a mirror-derived set is a VOTE over the work's
// recordings rather than a union of its rows:
//
//   - a recording's set is the union over its ASINs' rows - the regional ASINs of
//     one production are ONE vote, since they are one publisher's one tagging;
//   - a recording whose rows map nothing is not genre-bearing and does not vote;
//   - with n genre-bearing recordings, n >= 3 keeps a genre iff at least 2
//     recordings state it, and n < 3 keeps the union (two recordings cannot
//     outvote each other).
//
// It is applied where a set is the MIRROR's own account: the create path of a
// bulk-mirror run (mergeCreatedWorkFacts), `metaimport libex --enrich` filling a
// set (applyWorkGenres) - both through the one accretion rule, accrueRunGenres -
// and `metaimport libex --regenerate-genres` (regenerate.go). A set a user-library source contributed to is never trimmed
// (LICENSING.md, the trust tiers' rule 5).

// genreVoteQuorum is the number of genre-bearing recordings from which the vote
// stops being a union, and genreVoteMin the recordings a genre then needs.
const (
	genreVoteQuorum = 3
	genreVoteMin    = 2
)

// VoteGenres is the recording vote over a work's per-recording genre sets (each a
// recording's union over its rows; empty sets are not genre-bearing and are
// ignored), sorted and duplicate-free. stated is false when the vote says
// nothing - no recording states a genre, or n >= 3 recordings do and no genre
// reaches two of them - and genres is then the union of the sets: what a caller
// that must say something stores (the create path, where it keeps the outcome
// independent of row order), and what a caller that may stay silent ignores
// (the regeneration, which leaves the work as it is).
func VoteGenres(recordings [][]string) (genres []string, stated bool) {
	votes := map[string]int{}
	n := 0
	for _, set := range recordings {
		seen := map[string]bool{}
		for _, g := range set {
			if g != "" && !seen[g] {
				seen[g] = true
				votes[g]++
			}
		}
		if len(seen) > 0 {
			n++
		}
	}
	need := 1
	if n >= genreVoteQuorum {
		need = genreVoteMin
	}
	for g, c := range votes {
		if c >= need {
			genres = append(genres, g)
		}
	}
	if len(genres) == 0 {
		for g := range votes {
			genres = append(genres, g)
		}
	} else {
		stated = true
	}
	slices.Sort(genres)
	return genres, stated
}

// votesGenres reports whether the works THIS run creates take the recording vote:
// a CREATE run of a bulk-mirror source. A user-library run keeps LICENSING.md's
// rule 5 (its genres are added, never trimmed), and the relocation pass moves
// recordings rather than planning rows onto them, so it keeps the union too.
func (p *planner) votesGenres() bool {
	return p.mode == ModeCreate && p.mirrorTier
}
