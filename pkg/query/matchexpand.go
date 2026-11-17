package query

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strconv"
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
// WHAT IS INLINED, AND FOR WHICH RESULT. Only the BEST result (the first: an
// identifier hit ranks first, scored.better) carries
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
// it always did. That degraded answer is sent `no-store` (handleMatch): a cache
// repeating it would send every client to the fetches for as long as it kept it.
// An older metaserve ignores the parameter, so clients handle that anyway. It is
// all or nothing, so `work` present means series_facts was read too, and a series
// missing from it is one nothing is known about.
//
// WHAT A CLIENT CAN LEAVE OUT. Two additions an older metaserve ignores, so a
// client sends them unconditionally: `lean` beside `work` leaves the work's CC BY-SA
// sidecars out (and unread), which a bulk match never reads and which are most of a
// long work's bytes; `expand_min` skips the expansion of a best the client will
// drop unread (no identifier, under its score), an answer that is still whole.

// matchExpandWork is the expand value that asks for all of this.
const matchExpandWork = "work"

// matchExpandLean, beside matchExpandWork, inlines the work without its CC BY-SA
// sidecars (characters, recaps, the recap summary and the position scale they
// are numbered on): most of a long work's bytes, and nothing a bulk match reads.
// A client sends both, so an older metaserve, which ignores the value it does not
// know, still inlines the whole work; alone it asks for nothing.
const matchExpandLean = "lean"

// matchExpandMinParam names the score under which the best is not expanded
// unless an identifier found it (1-100; anything else is ignored): a client that
// drops a weak best unread (audiosilo-server's review floor) says so, and its
// document is not read or sent. An older metaserve ignores it and expands.
const matchExpandMinParam = "expand_min"

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

// matchExpansionReq is what a request's expand parameters ask for.
type matchExpansionReq struct {
	work bool // matchExpandWork
	lean bool // matchExpandLean, beside work
	min  int  // matchExpandMinParam, 0 for none
}

// parseExpansion reads the expand parameter - comma-separated values, repeatable,
// a value this server does not know ignored rather than refused, so a client can
// ask for a later addition without learning the server's version first - and
// expand_min.
func parseExpansion(values []string, minScore string) matchExpansionReq {
	var x matchExpansionReq
	for part := range strings.SplitSeq(strings.Join(values, ","), ",") {
		switch strings.TrimSpace(part) {
		case matchExpandWork:
			x.work = true
		case matchExpandLean:
			x.lean = true
		}
	}
	x.lean = x.lean && x.work
	if n, err := strconv.Atoi(strings.TrimSpace(minScore)); err == nil && n >= 1 && n <= 100 {
		x.min = n
	}
	return x
}

// expandBest inlines the expansion on the best result, in place, and reports
// whether the answer is whole: false when the budget or a read stopped it, which
// leaves every result as it was (see the file header). No results is nothing to
// expand, so whole.
func (s *DB) expandBest(ctx context.Context, results []matchResult, x matchExpansionReq) bool {
	if len(results) == 0 {
		return true
	}
	best := &results[0]
	if best.Score < x.min && best.Reasons.Identifier == "" {
		return true // the client drops it unread: nothing it asked for is missing
	}
	work, facts, err := s.matchExpansion(ctx, best.ID, !x.lean)
	if err != nil {
		if ctx.Err() == nil {
			s.logf("query: works/match expand of %s failed, serving the match alone: %v", best.ID, err)
		}
		return false
	}
	best.Work, best.SeriesFacts = work, facts
	return true
}

// matchExpansion reads one work's document and its series' facts. A work that isn't
// there is an error like any read that fails, as a match only names works it read.
// Its last step asks whether the context is still alive, so a read the budget cut
// short can never pass for a whole one.
func (s *DB) matchExpansion(ctx context.Context, workID string, sidecars bool) (*WorkDetail, map[string]seriesFacts, error) {
	work, err := s.workDocument(ctx, workID, sidecars)
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
//
// It reads the two facts and nothing else: not a search hit's member count (a
// join over series_works) or ordering, which seriesResultsByID would read only for
// them to be dropped here. translation_of comes from the read the search page and
// GET series/{id} share (seriesTranslationOfByID), so the ids are theirs.
func (s *DB) seriesFactsOf(ctx context.Context, refs []SeriesRef) (map[string]seriesFacts, error) {
	var ids []string
	for _, ref := range refs {
		if ref.OrderingOf == "" && !slices.Contains(ids, ref.ID) && len(ids) < matchExpandSeriesMax {
			ids = append(ids, ref.ID)
		}
	}
	out := make(map[string]seriesFacts, len(ids))
	languages := s.schemaVersion >= languagesSchemaVersion
	err := eachChunk(ids, func(ph string, args []any) error {
		return queryEach(ctx, s.db, seriesLanguagesByIDSQL(ph, languages), args, func(rows *sql.Rows) error {
			var id string
			var lang sql.NullString
			if err := rows.Scan(&id, &lang); err != nil {
				return err
			}
			out[id] = seriesFacts{Language: lang.String}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	translated, err := s.seriesTranslationOfByID(ctx, ids)
	if err != nil {
		return nil, err
	}
	for id, of := range translated {
		if f, ok := out[id]; ok {
			for _, t := range of {
				f.TranslationOf = append(f.TranslationOf, t.ID)
			}
			out[id] = f
		}
	}
	return out, nil
}

// seriesLanguagesByIDSQL reads each held series' derived language by primary key;
// an artifact older than languagesSchemaVersion has no such column and selects NULL.
func seriesLanguagesByIDSQL(ph string, languages bool) string {
	col := "NULL"
	if languages {
		col = "language"
	}
	return `SELECT id, ` + col + ` FROM series WHERE id IN (` + ph + `)`
}
