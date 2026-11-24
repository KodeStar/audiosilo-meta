package query

import "testing"

// TestSeriesLayerTexts pins the three texts seriesLayerCols spells, for every
// artifact shape each is asked of: the helper is a spelling, so each text must
// read exactly as it did when each wrote its own NULLs.
func TestSeriesLayerTexts(t *testing.T) {
	for _, tc := range []struct{ name, got, want string }{
		{"header, before v7", seriesHeaderSQL(false), `SELECT id, name, NULL, NULL, NULL FROM series WHERE id=?`},
		{"header, v7", seriesHeaderSQL(true), `SELECT id, name, language, ordering, ordering_of FROM series WHERE id=?`},
		{"languages by id, before v7", seriesLanguagesByIDSQL("?,?", false), `SELECT id, NULL FROM series WHERE id IN (?,?)`},
		{"languages by id, v7", seriesLanguagesByIDSQL("?,?", true), `SELECT id, language FROM series WHERE id IN (?,?)`},
		{"summaries, before v7", seriesSummariesByIDSQL("?", false, false),
			`SELECT s.id, s.name, NULL, NULL, NULL, NULL, COUNT(sw.series_id) FROM series s LEFT JOIN series_works sw ON sw.series_id = s.id WHERE s.id IN (?) GROUP BY s.id, s.name`},
		{"summaries, v7", seriesSummariesByIDSQL("?", true, false),
			`SELECT s.id, s.name, s.language, s.ordering, NULL, NULL, COUNT(sw.series_id) FROM series s LEFT JOIN series_works sw ON sw.series_id = s.id WHERE s.id IN (?) GROUP BY s.id, s.name`},
		{"summaries, v7 with orderings", seriesSummariesByIDSQL("?", true, true),
			`SELECT s.id, s.name, s.language, s.ordering, s.ordering_of, p.name, COUNT(sw.series_id) FROM series s LEFT JOIN series p ON p.id = s.ordering_of LEFT JOIN series_works sw ON sw.series_id = s.id WHERE s.id IN (?) GROUP BY s.id, s.name`},
	} {
		if tc.got != tc.want {
			t.Errorf("%s:\n got %s\nwant %s", tc.name, tc.got, tc.want)
		}
	}
}
