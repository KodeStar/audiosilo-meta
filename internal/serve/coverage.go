package serve

import (
	"math"
	"sort"
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
	res := &coverageResult{Totals: coverageTotals{Works: s.stats.Works}}

	// Nil maps are fine here: they are only read below (a nil-map read is false).
	var hasChars, hasRecaps, hasSummary map[string]bool

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
// id). A work's series membership is its first series by id, matching the
// workCard convention (firstSeriesOf). Most works are expected in the list, so
// authors and series come from two bulk queries rather than per-work lookups.
func (s *snapshot) missingWorks(hasChars, hasRecaps, hasSummary map[string]bool, evalSummary bool) ([]missingWork, error) {
	authorNames, err := s.workAuthorNames()
	if err != nil {
		return nil, err
	}
	firstSeries, err := s.firstSeriesByWork()
	if err != nil {
		return nil, err
	}

	rows, err := s.db.Query(`SELECT id, title FROM works`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []missingWork{}
	for rows.Next() {
		var id, title string
		if err := rows.Scan(&id, &title); err != nil {
			return nil, err
		}
		var miss []string
		if !hasChars[id] {
			miss = append(miss, "characters")
		}
		if !hasRecaps[id] {
			miss = append(miss, "recaps")
		}
		if evalSummary && !hasSummary[id] {
			miss = append(miss, "recap_summary")
		}
		if len(miss) == 0 {
			continue
		}
		authors := authorNames[id]
		if authors == nil {
			authors = []string{}
		}
		out = append(out, missingWork{
			ID: id, Title: title, Authors: authors,
			Series: firstSeries[id], Missing: miss,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if (a.Series != nil) != (b.Series != nil) {
			return a.Series != nil // series works before standalone works
		}
		if a.Series != nil {
			if a.Series.Name != b.Series.Name {
				return a.Series.Name < b.Series.Name
			}
			if a.Series.ID != b.Series.ID {
				return a.Series.ID < b.Series.ID
			}
			pa, pb := positionStart(a.Series.Position), positionStart(b.Series.Position)
			if pa != pb {
				return pa < pb
			}
		}
		if a.Title != b.Title {
			return a.Title < b.Title
		}
		return a.ID < b.ID
	})
	return out, nil
}

// workAuthorNames returns every work's author display names in credit order,
// keyed by work id, in one query.
func (s *snapshot) workAuthorNames() (map[string][]string, error) {
	rows, err := s.db.Query(
		`SELECT wa.work_id, p.name FROM work_authors wa JOIN people p ON p.id = wa.person_id ORDER BY wa.work_id, wa.ord`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string][]string{}
	for rows.Next() {
		var workID, name string
		if err := rows.Scan(&workID, &name); err != nil {
			return nil, err
		}
		out[workID] = append(out[workID], name)
	}
	return out, rows.Err()
}

// firstSeriesByWork returns every work's first series membership (first by
// series id, mirroring firstSeriesOf's ORDER BY s.id LIMIT 1 semantics), keyed
// by work id, in one query.
func (s *snapshot) firstSeriesByWork() (map[string]*seriesRef, error) {
	rows, err := s.db.Query(
		`SELECT sw.work_id, s.id, s.name, sw.position FROM series_works sw JOIN series s ON s.id = sw.series_id ORDER BY sw.work_id, s.id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]*seriesRef{}
	for rows.Next() {
		var workID string
		var sr seriesRef
		if err := rows.Scan(&workID, &sr.ID, &sr.Name, &sr.Position); err != nil {
			return nil, err
		}
		if _, seen := out[workID]; !seen {
			out[workID] = &sr
		}
	}
	return out, rows.Err()
}

// workIDSet runs a query returning a single work_id column and folds it into a
// presence set (row scanning is scanIDs' job).
func (s *snapshot) workIDSet(query string) (map[string]bool, error) {
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set, nil
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
