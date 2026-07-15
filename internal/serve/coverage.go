package serve

import (
	"math"
	"sort"
	"strings"
)

// coverageTotals is the top-line coverage count: how many works exist and how
// many carry each expressive-layer sidecar. The sidecar counts are pointers so
// an unknowable dimension (the artifact schema_version predates its table) is
// omitted rather than reported as a misleading 0 - a real zero (evaluable, no
// work covered) still serializes as 0.
type coverageTotals struct {
	Works            int  `json:"works"`
	WithCharacters   *int `json:"with_characters,omitempty"`
	WithRecaps       *int `json:"with_recaps,omitempty"`
	WithRecapSummary *int `json:"with_recap_summary,omitempty"`
}

// coverageWork is one work row in the coverage browser. Missing lists which of
// characters/recaps/recap_summary the work still lacks (in that fixed order);
// for a "has X" filter it may be empty (the work is fully covered). Series is
// omitted for standalone works.
type coverageWork struct {
	ID      string      `json:"id"`
	Title   string      `json:"title"`
	Authors []personRef `json:"authors"`
	Series  *seriesRef  `json:"series,omitempty"`
	Missing []string    `json:"missing"`
}

// seriesGap reports the integer positions absent from a series between its
// lowest and highest present integer position. Present is the raw position
// strings in numeric-ish order; MissingPositions is the interior integer holes.
type seriesGap struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Present          []string `json:"present"`
	MissingPositions []int    `json:"missing_positions"`
}

// coverageResult is the /api/v1/coverage payload: just the top-line totals. The
// full missing list and series gaps used to live here but are now their own
// paginated endpoints, so this stays small no matter how large the catalogue
// grows. Whether the per-work browser is computable degrades through the
// totals (an omitted sidecar count) and, per filter, through the `available`
// flag on /coverage/works - so there is no separate availability field here.
type coverageResult struct {
	Totals coverageTotals `json:"totals"`
}

// coverage computes the top-line expressive-layer totals for the snapshot.
//
// Degradation follows the artifact schema_version so a newer binary briefly
// serving an older release never reports "everything is missing":
//   - schema_version < 2: characters/recaps tables absent, so their totals are
//     omitted (and /coverage/works reports available:false).
//   - schema_version == 2: characters/recaps are evaluable; recap_summary is
//     not (the recap_summaries table arrived in v3), so its total is omitted.
//   - schema_version >= 3: all three dimensions are evaluable.
func (s *snapshot) coverage() (*coverageResult, error) {
	res := &coverageResult{Totals: coverageTotals{Works: s.stats.Works}}

	if s.schemaVersion >= sidecarSchemaVersion {
		nChars, err := s.scalarInt(`SELECT COUNT(DISTINCT work_id) FROM characters`)
		if err != nil {
			return nil, err
		}
		nRecaps, err := s.scalarInt(`SELECT COUNT(DISTINCT work_id) FROM recaps`)
		if err != nil {
			return nil, err
		}
		res.Totals.WithCharacters = &nChars
		res.Totals.WithRecaps = &nRecaps
	}
	if s.schemaVersion >= summarySchemaVersion {
		nSummary, err := s.scalarInt(
			`SELECT COUNT(*) FROM recap_summaries WHERE COALESCE(in_short,'') <> '' OR COALESCE(ending,'') <> ''`)
		if err != nil {
			return nil, err
		}
		res.Totals.WithRecapSummary = &nSummary
	}
	return res, nil
}

// coverageFilter selects which works the coverage browser lists.
type coverageFilter string

const (
	filterMissing         coverageFilter = "missing"           // missing at least one evaluable dimension
	filterHasCharacters   coverageFilter = "has_characters"    // carries a character guide
	filterHasRecaps       coverageFilter = "has_recaps"        // carries a story-so-far recap
	filterHasRecapSummary coverageFilter = "has_recap_summary" // carries a whole-book recap summary
)

// validCoverageFilter maps a raw ?filter= value to a known filter (defaulting
// to filterMissing on an empty value); ok is false for an unrecognized value.
func validCoverageFilter(raw string) (coverageFilter, bool) {
	switch coverageFilter(raw) {
	case "", filterMissing:
		return filterMissing, true
	case filterHasCharacters, filterHasRecaps, filterHasRecapSummary:
		return coverageFilter(raw), true
	default:
		return "", false
	}
}

// Correlated presence predicates. They are constant SQL (no user input) and
// only referenced for tables that exist at the current schema_version, so an
// older artifact never queries a missing table.
const (
	hasCharsExpr   = `EXISTS(SELECT 1 FROM characters c WHERE c.work_id=w.id)`
	hasRecapsExpr  = `EXISTS(SELECT 1 FROM recaps r WHERE r.work_id=w.id)`
	hasSummaryExpr = `EXISTS(SELECT 1 FROM recap_summaries rs WHERE rs.work_id=w.id AND (COALESCE(rs.in_short,'')<>'' OR COALESCE(rs.ending,'')<>''))`
)

// coverageWorksResult is the /api/v1/coverage/works payload: one page of works
// for the selected filter, plus the unpaged total (for the pager) and the
// echoed limit/offset. Available is false when the requested filter's dimension
// is not evaluable at this artifact schema_version (Works is then empty).
type coverageWorksResult struct {
	Works     []coverageWork `json:"works"`
	Total     int            `json:"total"`
	Limit     int            `json:"limit"`
	Offset    int            `json:"offset"`
	Available bool           `json:"available"`
}

// boolSQL returns expr when the dimension is evaluable, else the literal "0", so
// a SELECT can always project a has-flag column even when the backing table is
// absent at this schema_version.
func boolSQL(evaluable bool, expr string) string {
	if evaluable {
		return expr
	}
	return "0"
}

// coverageWorks lists works for the coverage browser: filtered by expressive-
// layer status, optionally narrowed by a title/author substring query, ordered
// by title then id, and paginated. It runs a COUNT plus one page query rather
// than materializing the whole catalogue, so it scales to any library size.
//
// Availability degrades with the artifact schema_version: a "has X" filter for
// a dimension whose table is absent (recap_summary before v3; everything before
// v2) returns Available=false with no rows, and the missing filter needs the
// characters/recaps tables (v2+).
func (s *snapshot) coverageWorks(filter coverageFilter, q string, limit, offset int) (*coverageWorksResult, error) {
	res := &coverageWorksResult{Works: []coverageWork{}, Limit: limit, Offset: offset}
	evalSidecars := s.schemaVersion >= sidecarSchemaVersion
	evalSummary := s.schemaVersion >= summarySchemaVersion

	var where string
	switch filter {
	case filterHasCharacters:
		if !evalSidecars {
			return res, nil
		}
		where = hasCharsExpr
	case filterHasRecaps:
		if !evalSidecars {
			return res, nil
		}
		where = hasRecapsExpr
	case filterHasRecapSummary:
		if !evalSummary {
			return res, nil
		}
		where = hasSummaryExpr
	default: // filterMissing
		if !evalSidecars {
			return res, nil
		}
		parts := []string{"NOT " + hasCharsExpr, "NOT " + hasRecapsExpr}
		if evalSummary {
			parts = append(parts, "NOT "+hasSummaryExpr)
		}
		where = "(" + strings.Join(parts, " OR ") + ")"
	}
	res.Available = true

	var args []any
	if q != "" {
		like := "%" + escapeLike(q) + "%"
		where += ` AND (w.title LIKE ? ESCAPE '\' OR EXISTS(` +
			`SELECT 1 FROM work_authors wa JOIN people p ON p.id=wa.person_id ` +
			`WHERE wa.work_id=w.id AND p.name LIKE ? ESCAPE '\'))`
		args = append(args, like, like)
	}

	total, err := s.scalarInt(`SELECT COUNT(*) FROM works w WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	res.Total = total
	if total == 0 || offset >= total {
		return res, nil
	}

	// Past the switch, evalSidecars is guaranteed true (every filter returns
	// early without it), so characters/recaps are always projected directly;
	// only recap_summary can be absent (v2), and boolSQL guards that one column.
	pageArgs := append(append([]any{}, args...), limit, offset)
	rows, err := s.db.Query(
		`SELECT w.id, w.title, `+hasCharsExpr+`, `+hasRecapsExpr+`, `+boolSQL(evalSummary, hasSummaryExpr)+
			` FROM works w WHERE `+where+` ORDER BY w.title, w.id LIMIT ? OFFSET ?`, pageArgs...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, title string
		var hasChars, hasRecaps, hasSummary int
		if err := rows.Scan(&id, &title, &hasChars, &hasRecaps, &hasSummary); err != nil {
			return nil, err
		}
		miss := []string{}
		if hasChars == 0 {
			miss = append(miss, "characters")
		}
		if hasRecaps == 0 {
			miss = append(miss, "recaps")
		}
		if evalSummary && hasSummary == 0 {
			miss = append(miss, "recap_summary")
		}
		authors, err := s.authorsOf(id)
		if err != nil {
			return nil, err
		}
		series, err := s.firstSeriesOf(id)
		if err != nil {
			return nil, err
		}
		res.Works = append(res.Works, coverageWork{
			ID: id, Title: title, Authors: authors, Series: series, Missing: miss,
		})
	}
	return res, rows.Err()
}

// seriesGapsResult is the /api/v1/coverage/series-gaps payload: one page of
// series with position gaps, the unpaged total, and the echoed limit/offset.
type seriesGapsResult struct {
	Gaps   []seriesGap `json:"gaps"`
	Total  int         `json:"total"`
	Limit  int         `json:"limit"`
	Offset int         `json:"offset"`
}

// seriesGapsPage returns a name/id-ordered, optionally name-filtered, paginated
// slice of the series that have interior position gaps. The full gap set is
// cheap to compute (one bulk query over positions); only the response is bounded
// by the page, so the payload stays small for any catalogue size.
func (s *snapshot) seriesGapsPage(q string, limit, offset int) (*seriesGapsResult, error) {
	all, err := s.seriesGaps()
	if err != nil {
		return nil, err
	}
	needle := strings.ToLower(strings.TrimSpace(q))
	filtered := make([]seriesGap, 0, len(all))
	for _, g := range all {
		if needle == "" || strings.Contains(strings.ToLower(g.Name), needle) {
			filtered = append(filtered, g)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].Name != filtered[j].Name {
			return filtered[i].Name < filtered[j].Name
		}
		return filtered[i].ID < filtered[j].ID
	})

	res := &seriesGapsResult{Gaps: []seriesGap{}, Total: len(filtered), Limit: limit, Offset: offset}
	if offset < len(filtered) {
		res.Gaps = filtered[offset:min(offset+limit, len(filtered))]
	}
	return res, nil
}

// seriesGaps reports, per series, the integer positions missing strictly between
// its lowest and highest present integer position. A range position ("1-3.5")
// covers every integer within it (ceil(lo)..floor(hi)); a bare decimal ("2.5")
// fills no integer slot. Series with fewer than two covered integers, or with no
// interior gap, are omitted. Output is sorted by series id. All positions come
// from one bulk query, grouped in memory.
func (s *snapshot) seriesGaps() ([]seriesGap, error) {
	positions, err := s.positionsBySeries()
	if err != nil {
		return nil, err
	}

	rows, err := s.db.Query(`SELECT id, name FROM series ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []seriesGap{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		present := positions[id]
		covered := map[int]bool{}
		for _, pos := range present {
			for _, iv := range coveredIntegers(pos) {
				covered[iv] = true
			}
		}
		if len(covered) < 2 {
			continue // need a distinct min and max integer for an interior gap
		}
		minI, maxI := math.MaxInt, math.MinInt
		for iv := range covered {
			if iv < minI {
				minI = iv
			}
			if iv > maxI {
				maxI = iv
			}
		}
		var gaps []int
		for iv := minI + 1; iv < maxI; iv++ {
			if !covered[iv] {
				gaps = append(gaps, iv)
			}
		}
		if len(gaps) == 0 {
			continue
		}
		sort.SliceStable(present, func(i, j int) bool {
			pi, pj := positionStart(present[i]), positionStart(present[j])
			if pi != pj {
				return pi < pj
			}
			return present[i] < present[j]
		})
		out = append(out, seriesGap{ID: id, Name: name, Present: present, MissingPositions: gaps})
	}
	return out, rows.Err()
}

// positionsBySeries returns every series' raw position strings, keyed by series
// id, in one query.
func (s *snapshot) positionsBySeries() (map[string][]string, error) {
	rows, err := s.db.Query(`SELECT series_id, position FROM series_works ORDER BY series_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string][]string{}
	for rows.Next() {
		var seriesID, pos string
		if err := rows.Scan(&seriesID, &pos); err != nil {
			return nil, err
		}
		out[seriesID] = append(out[seriesID], pos)
	}
	return out, rows.Err()
}

// coveredIntegers returns the integer position slots a single series position
// string fills. A bare integer ("2") fills that integer; a range ("1-3.5") fills
// every integer in [ceil(lo), floor(hi)]; a bare decimal ("2.5") fills nothing;
// an unparseable or inverted value fills nothing. The grammar is parsed by the
// shared parsePositionRange (queries.go).
func coveredIntegers(pos string) []int {
	lo, hi, ok := parsePositionRange(pos)
	if !ok || hi < lo {
		return nil
	}
	var out []int
	for v := int(math.Ceil(lo)); v <= int(math.Floor(hi)); v++ {
		out = append(out, v)
	}
	return out
}
