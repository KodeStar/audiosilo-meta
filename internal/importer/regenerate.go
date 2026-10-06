package importer

import (
	"fmt"
	"slices"
	"sort"

	"github.com/kodestar/audiosilo-meta/pkg/model"
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

// Genre-change modes.
const (
	GenreChangeTrim    = "trim"
	GenreChangeAddOnly = "add-only"
)

// regenWork is what the regeneration needs to know about one catalogued work,
// collected once at load (loadExisting).
type regenWork struct {
	genres []string
	// userSourced says a source on the work is user-library tier.
	userSourced bool
	// asinRecs are the work's recordings that carry at least one ASIN - the
	// ones whose rows the evidence must cover for a trim.
	asinRecs []string
}

// regenState is the pass's run state: the catalogue index and, per work, the
// mapped genres of each recording a row reached.
type regenState struct {
	works map[string]*regenWork
	seen  map[string]map[string][]string // work -> recording -> union of its rows' genres
}

// indexForRegen records w for the regeneration (loadExisting).
func (p *planner) indexForRegen(w *model.Work) {
	if p.regen == nil {
		p.regen = &regenState{works: map[string]*regenWork{}, seen: map[string]map[string][]string{}}
	}
	rw := &regenWork{genres: slices.Clone(w.Genres)}
	for _, s := range w.Sources {
		if model.TierOfSource(s.Type) == model.TierUserLibrary {
			rw.userSourced = true
		}
	}
	for _, r := range w.Recordings {
		if len(r.ASIN) > 0 {
			rw.asinRecs = append(rw.asinRecs, r.ID)
		}
	}
	p.regen.works[w.ID] = rw
}

// planRegenerateGenres is the pass itself: every row is matched to the
// recording its ASIN sits on (the enrichment pass's ASIN index, so a catalogue
// ASIN on two recordings matches the first and the second's evidence is simply
// incomplete), then every work a row reached is decided.
func (p *planner) planRegenerateGenres(books []sourceBook) {
	if p.regen == nil {
		return // an empty catalogue: nothing to regenerate
	}
	// The run reads every row of the catalogue, whose umbrella nodes ("Literature
	// & Fiction") are unmapped by design and were reported by the runs that
	// created the works; listing them again is noise, not news.
	unmapped := map[string]bool{}
	for _, b := range books {
		asin := NormalizeASIN(b.str("asin"))
		ref, matched := p.asinLoc[asin]
		if !matched {
			p.summary.NotInCatalog++
			continue
		}
		p.summary.Matched++
		recs := p.regen.seen[ref.Work]
		if recs == nil {
			recs = map[string][]string{}
			p.regen.seen[ref.Work] = recs
		}
		recs[ref.Rec] = UnionGenres(recs[ref.Rec], p.genres.mapGenres(b.genres, unmapped))
	}

	slugs := make([]string, 0, len(p.regen.works))
	for slug := range p.regen.works {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	var userSourced, incomplete, silent int
	for _, slug := range slugs {
		recs, reached := p.regen.seen[slug]
		if !reached {
			p.summary.GenreWorksNoRow++
			continue
		}
		rw := p.regen.works[slug]
		sets := make([][]string, 0, len(recs))
		for _, rec := range sortedKeys(recs) {
			sets = append(sets, recs[rec])
		}
		vote := VoteGenres(sets)
		complete := true
		for _, rec := range rw.asinRecs {
			if _, ok := recs[rec]; !ok {
				complete = false
				break
			}
		}
		next, mode := UnionGenres(rw.genres, vote), GenreChangeAddOnly
		switch {
		case rw.userSourced:
			userSourced++
		case !complete:
			incomplete++
		case len(vote) == 0:
			silent++
		default:
			next, mode = vote, GenreChangeTrim
		}
		if slices.Equal(next, rw.genres) {
			p.summary.GenreWorksUnchanged++
			continue
		}
		change := GenreChange{Work: slug, Removed: minus(rw.genres, next), Added: minus(next, rw.genres), Mode: mode}
		if !p.writeRegeneratedGenres(slug, next) {
			return
		}
		rw.genres = next
		if mode == GenreChangeTrim {
			p.summary.GenreWorksSet++
		} else {
			p.summary.GenreWorksAddedTo++
		}
		p.summary.GenresAdded += len(change.Added)
		p.summary.GenresRemoved += len(change.Removed)
		p.summary.GenreChanges = append(p.summary.GenreChanges, change)
	}
	if n := userSourced + incomplete; n > 0 {
		p.summary.Notes = append(p.summary.Notes, fmt.Sprintf(
			"genres of %d %s were only added to, never trimmed: %d carry a user-library source, %d have a recording with an ASIN no input row matched",
			n, plural(n, "work"), userSourced, incomplete))
	}
	if silent > 0 {
		p.summary.Notes = append(p.summary.Notes, fmt.Sprintf(
			"%d trim-eligible %s kept the recorded set because the recording vote stated no genre",
			silent, plural(silent, "work")))
	}
}

// writeRegeneratedGenres replaces the work entry's genres and queues it - the
// one write the pass makes, with no provenance stamp (see the file comment).
func (p *planner) writeRegeneratedGenres(slug string, genres []string) bool {
	raw := p.workEntryRaw(slug)
	if raw == nil {
		return p.fatal == nil
	}
	raw["genres"] = genres
	p.putWorkEntry(slug, raw)
	return true
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

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
