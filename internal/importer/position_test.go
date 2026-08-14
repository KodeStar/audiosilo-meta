package importer

import "testing"

// The two-step's ORDER is what these pin: acceptance through NormalizeSequence FIRST, then
// the span. model.ParsePositionRange is a ParseFloat, so reading the span first would mint
// slots for "1e2", "+2" and "Inf" - values the data model rejects - and a caller would then
// report those same values as malformed one rule later.
func TestPositionSpanAcceptsOnlyWhatTheModelAccepts(t *testing.T) {
	for _, tc := range []struct {
		pos    string
		lo, hi float64
		ok     bool
	}{
		{pos: "1", lo: 1, hi: 1, ok: true},
		{pos: "02", lo: 2, hi: 2, ok: true},
		{pos: "2.5", lo: 2.5, hi: 2.5, ok: true},
		{pos: "2.50", lo: 2.5, hi: 2.5, ok: true},
		{pos: "1-3.5", lo: 1, hi: 3.5, ok: true},
		{pos: "1 - 3", lo: 1, hi: 3, ok: true}, // the 104 real whitespace-spelled ranges
		// Everything the grammar rejects, including the three shapes a bare ParseFloat takes.
		{pos: ""}, {pos: "the third"}, {pos: "1e2"}, {pos: "+2"}, {pos: "Inf"}, {pos: "-1"},
	} {
		span, ok := PositionSpan(tc.pos)
		if ok != tc.ok {
			t.Errorf("PositionSpan(%q) ok = %v, want %v", tc.pos, ok, tc.ok)
			continue
		}
		if ok && (span[0] != tc.lo || span[1] != tc.hi) {
			t.Errorf("PositionSpan(%q) = %v, want [%v %v]", tc.pos, span, tc.lo, tc.hi)
		}
	}
}

// A RANGE is not a single slot, and a plain number is not a range - which is what a gap
// census and a slot lookup each need to know.
func TestPositionRangeSeparatesRangesFromNumbers(t *testing.T) {
	for _, tc := range []struct {
		pos     string
		lo, hi  float64
		isRange bool
	}{
		{pos: "1-3", lo: 1, hi: 3, isRange: true},
		{pos: "1 - 3", lo: 1, hi: 3, isRange: true},
		{pos: "2.1-2.10", lo: 2.1, hi: 2.1, isRange: true}, // a decimal is not a two-part number
		{pos: "2"},
		{pos: "2.5"},
		{pos: "nonsense"},
	} {
		lo, hi, isRange := PositionRange(tc.pos)
		if isRange != tc.isRange {
			t.Errorf("PositionRange(%q) isRange = %v, want %v", tc.pos, isRange, tc.isRange)
			continue
		}
		if isRange && (lo != tc.lo || hi != tc.hi) {
			t.Errorf("PositionRange(%q) = %v-%v, want %v-%v", tc.pos, lo, hi, tc.lo, tc.hi)
		}
	}
}

// SameSlot is what every "is this place in the order taken" question asks: a position is a
// STRING in the data model, so two spellings of one number are one slot while a novella and
// an omnibus stay their own.
func TestSameSlot(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		same bool
	}{
		{a: "3", b: "3", same: true},
		{a: "3", b: "03", same: true},
		{a: "2.5", b: "2.50", same: true},
		{a: "1-3", b: "1 - 3", same: true},
		{a: "2", b: "2.5"},
		{a: "2", b: "1-3.5"},
		{a: "1-3", b: "1-4"},
		// A position the grammar rejects is only ever equal to itself, never to another.
		{a: "the third", b: "the third", same: true},
		{a: "the third", b: "3"},
	} {
		if got := SameSlot(tc.a, tc.b); got != tc.same {
			t.Errorf("SameSlot(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.same)
		}
		if got := SameSlot(tc.b, tc.a); got != tc.same {
			t.Errorf("SameSlot is not symmetric for (%q, %q)", tc.a, tc.b)
		}
	}
}
