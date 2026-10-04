package titlerule

import "testing"

// The franchise key meets two franchise words where SeriesKey keeps them apart, and is
// still a key over the WHOLE name: a different franchise, a franchise word inside the
// name and a sub-series stay apart.
func TestSeriesFranchiseKey(t *testing.T) {
	same := [][2]string{
		{"The Jack Ryan Universe (publication order)", "A Jack Ryan Novel (Publication Order)"},
		{"Jack Ryan Saga", "Jack Ryan Series"},
		{"The Expanse Universe", "The Expanse"},
	}
	for _, p := range same {
		if a, b := SeriesFranchiseKey(p[0]), SeriesFranchiseKey(p[1]); a != b || a == "" {
			t.Errorf("SeriesFranchiseKey(%q) = %q, (%q) = %q: want one key", p[0], a, p[1], b)
		}
	}
	if SeriesKey("The Jack Ryan Universe") == SeriesKey("A Jack Ryan Novel") {
		t.Error("SeriesKey already meets the two franchise words; the franchise key would add nothing")
	}
	apart := [][2]string{
		{"Rincewind", "Discworld"},
		{"Discworld: Rincewind", "Discworld"},
		{"Universe Saga Origins", "Origins"},
	}
	for _, p := range apart {
		if SeriesFranchiseKey(p[0]) == SeriesFranchiseKey(p[1]) {
			t.Errorf("SeriesFranchiseKey met %q and %q", p[0], p[1])
		}
	}
}
