package importer

import (
	"fmt"
	"maps"
	"slices"
)

// regenerate.go is `metaimport libex --regenerate-genres` (ModeRegenerateGenres):
// the pass that re-derives catalogued works' genre sets from the libex rows of
// their own recordings, under today's mapping table and the recording vote
// (genrevote.go).
//
// It exists because nothing else repairs a stored set. Every writer only ever
// UNIONS genres, the create path stores the set the creating run's rows mapped
// to at the time, and enrichment fills only a work with NO genres - so a table
// fix (Romance > Contemporary, issue #2337), the format rule (audiblegenres.go)
// and the vote reach new works only, and a set the mirror got wrong stays wrong.
//
// What it may do to a work is the TRUST TIER's decision (LICENSING.md, rule 5):
//
//   - TRIM-ELIGIBLE: no source on the work is user-library tier, AND every
//     recording of the work that carries an ASIN met at least one input row
//     (the evidence is complete - a recording whose rows are missing could hold
//     the very genre the vote would drop). Its set BECOMES the vote, sorted. A
//     vote that states nothing (no recording maps a genre, or n >= 3 recordings
//     agree on none) leaves the work unchanged: silence is not an assertion.
//   - otherwise ADD-ONLY: the set becomes existing UNION vote. A set a
//     user-library source contributed to is only ever added to, and an
//     incomplete account cannot remove what an unseen recording might state.
//
// The reference tier does not block a trim: a `community` source (a Goodreads
// reference) states no genre, so it contributed nothing a trim could take away.
//
// It never creates anything and touches no field but `genres`. It does NOT
// append a sources[] entry: the genres are a DERIVATION of rows the work
// already cites (its recordings' ASINs are libex rows, and the work's own
// libex-import source is the import that read them), so a stamp would add no
// provenance - and would make a re-run that only re-derives the same set look
// like a new import. A second identical run is a byte-level no-op.

// GenreChange is one work the regeneration changed, a line of the
// `--genre-changes` worklist.
type GenreChange struct {
	Work    string   `json:"work"`
	Removed []string `json:"removed"`
	Added   []string `json:"added"`
	// Mode is "trim" (the set became the vote) or "add-only" (the vote was
	// unioned in), see regenerate.go.
	Mode string `json:"mode"`
}

// GenreTally counts a genre regeneration's changes: the works set to the vote
// (trim) and the works only added to, and the genre instances added and removed.
func (s Summary) GenreTally() (set, addedTo, added, removed int) {
	for _, c := range s.GenreChanges {
		if c.Mode == GenreChangeTrim {
			set++
		} else {
			addedTo++
		}
		added += len(c.Added)
		removed += len(c.Removed)
	}
	return set, addedTo, added, removed
}

// Genre-change modes.
const (
	GenreChangeTrim    = "trim"
	GenreChangeAddOnly = "add-only"
)

// planRegenerateGenres is the pass itself: every row is matched to the
// recording its ASIN sits on (forEachMatchedRow, the enrichment pass's ASIN
// index, so a catalogue ASIN on two recordings matches the first and the
// second's evidence is simply incomplete), then every catalogued work a row
// reached is decided from what the load put on its workState.
func (p *planner) planRegenerateGenres(books []sourceBook) {
	// The run reads every row of the catalogue, whose umbrella nodes ("Literature
	// & Fiction") are unmapped by design and were reported by the runs that
	// created the works; listing them again is noise, not news.
	unmapped := map[string]bool{}
	reached := map[string]bool{}
	p.forEachMatchedRow(books, func(b sourceBook, ref RecRef) {
		reached[ref.Work] = true
		p.regenRecs[ref] = UnionGenres(p.regenRecs[ref], p.genres.mapGenres(b.genres, unmapped))
	})

	var userSourced, incomplete, silent int
	for _, slug := range slices.Sorted(maps.Keys(p.works)) {
		if !reached[slug] {
			p.summary.GenreWorksNoRow++
			continue
		}
		ws := p.works[slug]
		var sets [][]string
		complete := true
		for rec, ri := range ws.recs {
			set, seen := p.regenRecs[RecRef{Work: slug, Rec: rec}]
			if seen {
				sets = append(sets, set)
			} else if len(ri.asins) > 0 {
				complete = false
			}
		}
		vote, stated := VoteGenres(sets)
		if !stated {
			vote = nil
		}
		next, mode := UnionGenres(ws.genres, vote), GenreChangeAddOnly
		switch {
		case ws.userSourced:
			userSourced++
		case !complete:
			incomplete++
		case !stated:
			silent++
		default:
			next, mode = vote, GenreChangeTrim
		}
		if slices.Equal(next, ws.genres) {
			p.summary.GenreWorksUnchanged++
			continue
		}
		raw := p.workEntryRaw(slug)
		if raw == nil {
			if p.fatal != nil {
				return
			}
			continue
		}
		raw["genres"] = next
		p.putWorkEntry(slug, raw)
		p.summary.GenreChanges = append(p.summary.GenreChanges,
			GenreChange{Work: slug, Removed: minus(ws.genres, next), Added: minus(next, ws.genres), Mode: mode})
	}
	if n := userSourced + incomplete; n > 0 {
		p.summary.Notes = append(p.summary.Notes, fmt.Sprintf(
			"genres of %d %s could only be added to, never trimmed: %d carry a user-library source, %d have a recording with an ASIN no input row matched",
			n, plural(n, "work"), userSourced, incomplete))
	}
	if silent > 0 {
		p.summary.Notes = append(p.summary.Notes, fmt.Sprintf(
			"%d trim-eligible %s kept the recorded set because the recording vote stated no genre",
			silent, plural(silent, "work")))
	}
}

// minus is the sorted members of a that b lacks.
func minus(a, b []string) []string {
	out := []string{}
	for _, g := range a {
		if !slices.Contains(b, g) {
			out = append(out, g)
		}
	}
	return out
}
