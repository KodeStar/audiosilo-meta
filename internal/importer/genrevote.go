package importer

import (
	"slices"
	"sort"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

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
// bulk-mirror run (recordRunVote), and `metaimport libex --regenerate-genres`
// (regenerate.go). A set a user-library source contributed to is never trimmed
// (LICENSING.md, the trust tiers' rule 5).

// genreVoteQuorum is the number of genre-bearing recordings from which the vote
// stops being a union, and genreVoteMin the recordings a genre then needs.
const (
	genreVoteQuorum = 3
	genreVoteMin    = 2
)

// VoteGenres is the recording vote over a work's per-recording genre sets (each a
// recording's union over its rows; empty sets are not genre-bearing and are
// ignored). The result is sorted and duplicate-free. It is EMPTY when no
// recording states a genre, and also when n >= 3 recordings state genres and no
// genre reaches two of them - "no agreement", which every caller reads as "say
// nothing new" rather than as an empty set to store.
func VoteGenres(recordings [][]string) []string {
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
	var out []string
	for g, c := range votes {
		if c >= need {
			out = append(out, g)
		}
	}
	sort.Strings(out)
	return out
}

// unionOfSets is the plain union of per-recording sets, sorted.
func unionOfSets(recordings [][]string) []string {
	var out []string
	for _, set := range recordings {
		out = UnionGenres(out, set)
	}
	return out
}

// voteOrUnion is the create path's reading of the vote: the vote when it states
// anything, else the union. The fallback only matters for n >= 3 recordings that
// share no genre at all, and it is what keeps the outcome independent of the
// order a run meets the rows in - "keep whatever the work already says" would
// keep whichever two rows happened to come first.
func voteOrUnion(recordings [][]string) []string {
	if v := VoteGenres(recordings); len(v) > 0 {
		return v
	}
	return unionOfSets(recordings)
}

// recordRunVote is the create path's half of the vote: the row just planned onto
// recording rec of ws (a work THIS bulk-mirror create run created, which is what
// a non-nil runRecGenres marks) adds its mapped genres to that recording's set,
// and the work's stored set becomes the vote over every recording the run has
// given it. A row that changes nothing - the overwhelming majority - costs no
// store read; one that moves the vote is written with one read and one put and
// stamps its provenance, as the in-run credit merge does, because the set now
// reflects its evidence.
func (p *planner) recordRunVote(ws *workState, rec string, claims []genreClaim) {
	if ws == nil || ws.runRecGenres == nil || rec == "" {
		return
	}
	mapped := p.genres.mapGenres(claims, p.unmappedGenres)
	cur, known := ws.runRecGenres[rec]
	next := UnionGenres(cur, mapped)
	if known && len(next) == len(cur) {
		return
	}
	ws.runRecGenres[rec] = next
	sets := make([][]string, 0, len(ws.runRecGenres))
	for _, s := range ws.runRecGenres {
		sets = append(sets, s)
	}
	out := voteOrUnion(sets)
	if slices.Equal(out, ws.runGenres) {
		return
	}
	raw := p.workEntryRaw(ws.slug)
	if raw == nil {
		return
	}
	if len(out) == 0 {
		delete(raw, "genres")
	} else {
		raw["genres"] = out
	}
	ws.runGenres = out
	p.stampSource(raw)
	p.putWorkEntry(ws.slug, raw)
}

// votesGenres reports whether the works THIS run creates take the recording vote:
// a CREATE run of a bulk-mirror source. A user-library run keeps LICENSING.md's
// rule 5 (its genres are added, never trimmed), and the relocation pass moves
// recordings rather than planning rows onto them, so it keeps the union too.
func (p *planner) votesGenres() bool {
	return p.mode == ModeCreate && model.TierOfSource(p.sourceType) == model.TierBulkMirror
}
