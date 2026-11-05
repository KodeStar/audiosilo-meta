package query

import (
	"context"
	"errors"
	"slices"
	"strings"
)

// works/match?expand=work: one response carrying what a bulk match otherwise
// fetches on its own.
//
// WHY. A client matching a whole library pays a network round trip for each wait:
// the match, then the work it found (GET works/{id}), then what that work's series
// records say (GET series/{id}). Over the public service those waits are nearly all
// of the time (RTT ~80 ms against a 20 ms median match), so inlining them turns
// three waits per book into one.
//
// WHAT IS INLINED, AND FOR WHICH RESULT. Only the BEST result (bestResult) carries
// anything: a run reads the best candidate's whole document and of the next one only
// the score and reasons every result has. It gets the work exactly as GET works/{id}
// serves it, and series_facts, the two facts of its series that the client's series
// rules read off the series record. Not the record itself: GET series/{id} lists
// every member, and a series can have hundreds. So the addition to a response is one
// work and at most matchExpandSeriesMax small objects, whatever the limit asked for.
//
// THE BUDGET. It runs after the match has its answer, inside the same slot and the
// same budget (matchInSlot), and every read takes that context. It is a saving,
// never a requirement: out of budget, or on a read error, the match goes out as it
// would have without expand, because a client that finds nothing inlined fetches as
// it always did. An older metaserve ignores the parameter, so clients handle that
// anyway. It is all or nothing, so `work` present means series_facts was read too,
// and a series missing from it is one nothing is known about.

// matchExpandWork is the expand value that asks for all of this.
const matchExpandWork = "work"

// matchExpandSeriesMax bounds the series one expansion reads the facts of, like
// audiosilo-server's maxSeriesFacts (internal/meta/seriesrules.go), which it must
// not fall below: past either, a series' facts are not known, which the rules read
// as nothing against it. The catalogue's most series on one work is 5 (2026-11).
const matchExpandSeriesMax = 16

// seriesFacts is what a series' own record says that a work's membership does not:
// its derived language, omitted where its members tie (or the artifact predates
// languagesSchemaVersion), and the ids of the series it translates. Each is the same
// value GET series/{id} serves as language and translation_of.
type seriesFacts struct {
	Language      string   `json:"language,omitempty"`
	TranslationOf []string `json:"translation_of,omitempty"`
}

// wantsWorkExpansion reads the expand parameter: comma-separated values, repeatable,
// and a value this server does not know is ignored rather than refused, so a client
// can ask for a later addition without learning the server's version first.
func wantsWorkExpansion(values []string) bool {
	for part := range strings.SplitSeq(strings.Join(values, ","), ",") {
		if strings.TrimSpace(part) == matchExpandWork {
			return true
		}
	}
	return false
}

// bestResult is the index of the result a client takes as the match: the one the
// identifier found, which is the book whatever else scores as well, else the first
// (they are in rank order). An identifier hit always scores 100, so it is never below
// the top score, but the ranking breaks a tie on the title, so it can rank second.
func bestResult(results []matchResult) int {
	return max(slices.IndexFunc(results, func(r matchResult) bool { return r.Reasons.Identifier != "" }), 0)
}

// expandBest inlines the expansion on the best result, in place. Past the budget or
// on a read error it leaves every result as it was: see the file header.
func (s *DB) expandBest(ctx context.Context, results []matchResult) {
	if len(results) == 0 {
		return
	}
	best := &results[bestResult(results)]
	work, facts, err := s.matchExpansion(ctx, best.ID)
	if err != nil {
		if ctx.Err() == nil {
			s.logf("query: works/match expand of %s failed, serving the match alone: %v", best.ID, err)
		}
		return
	}
	best.Work, best.SeriesFacts = work, facts
}

// matchExpansion reads one work's document and its series' facts. A work that isn't
// there is an error like any read that fails, as a match only names works it read.
// Its last step asks whether the context is still alive, so a read the budget cut
// short can never pass for a whole one.
func (s *DB) matchExpansion(ctx context.Context, workID string) (*WorkDetail, map[string]seriesFacts, error) {
	work, err := s.workDetail(ctx, workID)
	if err != nil {
		return nil, nil, err
	}
	if work == nil {
		return nil, nil, errors.New("not in the artifact")
	}
	facts, err := s.seriesFactsOf(ctx, work.Series)
	if err != nil {
		return nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	return work, facts, nil
}

// seriesFactsOf reads the facts of the series a work belongs to, in one batch: the
// memberships in order, each once, up to matchExpandSeriesMax. A variant reading
// order (ordering_of) is left out, as it is never a series a book of the work is in,
// so the rules ask nothing of it. A series the catalogue doesn't hold has no entry.
func (s *DB) seriesFactsOf(ctx context.Context, refs []SeriesRef) (map[string]seriesFacts, error) {
	var ids []string
	for _, ref := range refs {
		if ref.OrderingOf == "" && !slices.Contains(ids, ref.ID) && len(ids) < matchExpandSeriesMax {
			ids = append(ids, ref.ID)
		}
	}
	rows, err := s.seriesResultsByID(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[string]seriesFacts, len(rows))
	for id, r := range rows {
		f := seriesFacts{Language: r.Language}
		for _, t := range r.TranslationOf {
			f.TranslationOf = append(f.TranslationOf, t.ID)
		}
		out[id] = f
	}
	return out, nil
}
