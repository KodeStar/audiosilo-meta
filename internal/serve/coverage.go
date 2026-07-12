package serve

import (
	"math"
	"sort"
	"strconv"
	"strings"
)

// coverageTotals is the top-line coverage count: how many works exist and how
// many carry each expressive-layer sidecar.
type coverageTotals struct {
	Works            int `json:"works"`
	WithCharacters   int `json:"with_characters"`
	WithRecaps       int `json:"with_recaps"`
	WithRecapSummary int `json:"with_recap_summary"`
}

// missingWork is one work that lacks at least one evaluable expressive-layer
// dimension. Missing lists exactly which of characters/recaps/recap_summary it
// is missing (in that fixed order). Series is omitted for standalone works.
type missingWork struct {
	ID      string     `json:"id"`
	Title   string     `json:"title"`
	Authors []string   `json:"authors"`
	Series  *seriesRef `json:"series,omitempty"`
	Missing []string   `json:"missing"`
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

// coverageResult is the /api/v1/coverage payload. Missing is a pointer so it can
// be present-but-empty (all works covered, schema_version >= 2) versus omitted
// entirely (schema_version < 2, where sidecar coverage is unknowable). SeriesGaps
// is always an array (positions are schema_version 1 data).
type coverageResult struct {
	Totals     coverageTotals `json:"totals"`
	Missing    *[]missingWork `json:"missing,omitempty"`
	SeriesGaps []seriesGap    `json:"series_gaps"`
}

// coverage computes the expressive-layer coverage report for the snapshot.
//
// Degradation follows the artifact schema_version, so a newer binary briefly
// serving an older release never reports "everything is missing":
//   - schema_version < 2: characters/recaps tables absent, so their totals are 0
//     and the missing list is omitted entirely (coverage is unknowable).
//   - schema_version == 2: characters/recaps are evaluable; recap_summary is not
//     (the recap_summaries table arrived in v3), so recap_summary is neither
//     counted nor treated as a missing dimension.
//   - schema_version >= 3: all three dimensions are evaluable.
//
// series_gaps has no schema_version dependency and is always computed.
func (s *snapshot) coverage() (*coverageResult, error) {
	res := &coverageResult{
		Totals:     coverageTotals{Works: s.stats.Works},
		SeriesGaps: []seriesGap{},
	}

	hasChars := map[string]bool{}
	hasRecaps := map[string]bool{}
	hasSummary := map[string]bool{}

	if s.schemaVersion >= sidecarSchemaVersion {
		var err error
		if hasChars, err = s.workIDSet(`SELECT DISTINCT work_id FROM characters`); err != nil {
			return nil, err
		}
		if hasRecaps, err = s.workIDSet(`SELECT DISTINCT work_id FROM recaps`); err != nil {
			return nil, err
		}
		res.Totals.WithCharacters = len(hasChars)
		res.Totals.WithRecaps = len(hasRecaps)
	}
	evalSummary := s.schemaVersion >= summarySchemaVersion
	if evalSummary {
		var err error
		if hasSummary, err = s.workIDSet(
			`SELECT work_id FROM recap_summaries WHERE COALESCE(in_short,'') <> '' OR COALESCE(ending,'') <> ''`); err != nil {
			return nil, err
		}
		res.Totals.WithRecapSummary = len(hasSummary)
	}

	// series_gaps is independent of the sidecar schema version.
	gaps, err := s.seriesGaps()
	if err != nil {
		return nil, err
	}
	res.SeriesGaps = gaps

	// The missing list needs at least characters/recaps to be evaluable.
	if s.schemaVersion < sidecarSchemaVersion {
		return res, nil
	}

	missing, err := s.missingWorks(hasChars, hasRecaps, hasSummary, evalSummary)
	if err != nil {
		return nil, err
	}
	res.Missing = &missing
	return res, nil
}

// missingWorks builds the ordered list of works missing at least one evaluable
// expressive-layer dimension. Ordering (deterministic): series works first,
// grouped by series name (then series id), then numeric-ish position order,
// then title, then id; standalone works follow, alphabetically by title (then
// id). A work's series membership is its first series by id (firstSeriesOf),
// matching the workCard convention.
func (s *snapshot) missingWorks(hasChars, hasRecaps, hasSummary map[string]bool, evalSummary bool) ([]missingWork, error) {
	rows, err := s.db.Query(`SELECT id, title FROM works`)
	if err != nil {
		return nil, err
	}
	type workRow struct{ id, title string }
	var works []workRow
	for rows.Next() {
		var wr workRow
		if err := rows.Scan(&wr.id, &wr.title); err != nil {
			_ = rows.Close()
			return nil, err
		}
		works = append(works, wr)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// sortable carries the row plus the keys used to order it.
	type sortable struct {
		mw         missingWork
		hasSeries  bool
		seriesName string
		seriesID   string
		pos        float64
	}
	var items []sortable
	for _, wr := range works {
		var miss []string
		if !hasChars[wr.id] {
			miss = append(miss, "characters")
		}
		if !hasRecaps[wr.id] {
			miss = append(miss, "recaps")
		}
		if evalSummary && !hasSummary[wr.id] {
			miss = append(miss, "recap_summary")
		}
		if len(miss) == 0 {
			continue
		}
		mw := missingWork{ID: wr.id, Title: wr.title, Missing: miss, Authors: []string{}}
		authors, err := s.authorsOf(wr.id)
		if err != nil {
			return nil, err
		}
		for _, a := range authors {
			mw.Authors = append(mw.Authors, a.Name)
		}
		sr, err := s.firstSeriesOf(wr.id)
		if err != nil {
			return nil, err
		}
		it := sortable{}
		if sr != nil {
			mw.Series = sr
			it.hasSeries = true
			it.seriesName = sr.Name
			it.seriesID = sr.ID
			it.pos = positionStart(sr.Position)
		}
		it.mw = mw
		items = append(items, it)
	}

	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.hasSeries != b.hasSeries {
			return a.hasSeries // series works before standalone works
		}
		if a.hasSeries {
			if a.seriesName != b.seriesName {
				return a.seriesName < b.seriesName
			}
			if a.seriesID != b.seriesID {
				return a.seriesID < b.seriesID
			}
			if a.pos != b.pos {
				return a.pos < b.pos
			}
		}
		if a.mw.Title != b.mw.Title {
			return a.mw.Title < b.mw.Title
		}
		return a.mw.ID < b.mw.ID
	})

	out := make([]missingWork, 0, len(items))
	for _, it := range items {
		out = append(out, it.mw)
	}
	return out, nil
}

// workIDSet runs a query returning a single work_id column and collects it into
// a presence set.
func (s *snapshot) workIDSet(query string) (map[string]bool, error) {
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	set := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		set[id] = true
	}
	return set, rows.Err()
}

// seriesGaps reports, per series, the integer positions missing strictly between
// its lowest and highest present integer position. A range position ("1-3.5")
// covers every integer within it (ceil(lo)..floor(hi)); a bare decimal ("2.5")
// fills no integer slot. Series with fewer than two covered integers, or with no
// interior gap, are omitted. Output is sorted by series id.
func (s *snapshot) seriesGaps() ([]seriesGap, error) {
	rows, err := s.db.Query(`SELECT id, name FROM series ORDER BY id`)
	if err != nil {
		return nil, err
	}
	type ser struct{ id, name string }
	var all []ser
	for rows.Next() {
		var sv ser
		if err := rows.Scan(&sv.id, &sv.name); err != nil {
			_ = rows.Close()
			return nil, err
		}
		all = append(all, sv)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := []seriesGap{}
	for _, sv := range all {
		positions, covered, err := s.seriesPositions(sv.id)
		if err != nil {
			return nil, err
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
		sort.SliceStable(positions, func(i, j int) bool {
			pi, pj := positionStart(positions[i]), positionStart(positions[j])
			if pi != pj {
				return pi < pj
			}
			return positions[i] < positions[j]
		})
		out = append(out, seriesGap{ID: sv.id, Name: sv.name, Present: positions, MissingPositions: gaps})
	}
	return out, nil
}

// seriesPositions returns a series' raw position strings and the set of integer
// slots they cover.
func (s *snapshot) seriesPositions(seriesID string) ([]string, map[int]bool, error) {
	rows, err := s.db.Query(`SELECT position FROM series_works WHERE series_id=?`, seriesID)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	var positions []string
	covered := map[int]bool{}
	for rows.Next() {
		var pos string
		if err := rows.Scan(&pos); err != nil {
			return nil, nil, err
		}
		positions = append(positions, pos)
		for _, iv := range coveredIntegers(pos) {
			covered[iv] = true
		}
	}
	return positions, covered, rows.Err()
}

// coveredIntegers returns the integer position slots a single series position
// string fills. A bare integer ("2") fills that integer; a range ("1-3.5") fills
// every integer in [ceil(lo), floor(hi)]; a bare decimal ("2.5") fills nothing;
// an unparseable value fills nothing.
func coveredIntegers(pos string) []int {
	pos = strings.TrimSpace(pos)
	if pos == "" {
		return nil
	}
	if i := strings.IndexByte(pos, '-'); i > 0 {
		lo, err1 := strconv.ParseFloat(strings.TrimSpace(pos[:i]), 64)
		hi, err2 := strconv.ParseFloat(strings.TrimSpace(pos[i+1:]), 64)
		if err1 != nil || err2 != nil || hi < lo {
			return nil
		}
		var out []int
		for v := int(math.Ceil(lo)); v <= int(math.Floor(hi)); v++ {
			out = append(out, v)
		}
		return out
	}
	f, err := strconv.ParseFloat(pos, 64)
	if err != nil || f != math.Trunc(f) {
		return nil
	}
	return []int{int(f)}
}
