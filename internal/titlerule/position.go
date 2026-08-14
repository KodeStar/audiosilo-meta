package titlerule

import (
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// position.go is the SERIES-POSITION primitive every reader of a membership goes
// through: what numeric span a stored position occupies, and whether two spellings name
// one slot.
//
// It is here for the reason the rest of this package is: the two-step below had grown
// FIVE spellings across internal/audit and internal/repair (a slot key, a span map, a
// range reader, a merge's slot comparison, a gap census), and a rule with five spellings
// is a rule that will disagree with itself. A position is a string in the data model, so
// "02", "2" and "2.0" are three different values that name one slot, and any rule that
// compares two of them has to say so the same way.
//
// THE ORDER IS LOAD-BEARING, and it is stated once, here. Acceptance goes through
// importer.NormalizeSequence - the rule of record for what a position may be and how it
// is spelled, which also tolerates the 104 real "1 - 3" range spellings the schema
// pattern rejects - and only then is the span read with model.ParsePositionRange. Doing
// it the other way round mints slots for values the data model rejects: the span reader
// is a ParseFloat, so it happily accepts "1e2", "+2" and "Inf", and a caller would then
// report those same values as malformed a rule later.

// PositionSpan is the numeric range a stored position occupies: [n, n] for a single
// position, [lo, hi] for an omnibus range. ok is false for anything the position grammar
// rejects.
func PositionSpan(pos string) (span [2]float64, ok bool) {
	norm, ok := importer.NormalizeSequence(pos)
	if !ok {
		return [2]float64{}, false
	}
	lo, hi, ok := model.ParsePositionRange(norm)
	if !ok {
		return [2]float64{}, false
	}
	return [2]float64{lo, hi}, true
}

// PositionRange is PositionSpan restricted to the RANGE spellings: isRange is false for
// a plain number, which is not a range rather than a malformed one.
func PositionRange(pos string) (lo, hi float64, isRange bool) {
	norm, ok := importer.NormalizeSequence(pos)
	if !ok || !strings.Contains(norm, "-") {
		return 0, 0, false
	}
	span, ok := PositionSpan(norm)
	if !ok {
		return 0, 0, false
	}
	return span[0], span[1], true
}

// SameSlot reports whether two stored positions name the same slot. Equal strings always
// do; otherwise they are compared as spans, so "02" and "2" are one slot while "2", "2.5"
// and "1-3.5" stay three. A position the grammar rejects is only ever equal to itself.
func SameSlot(a, b string) bool {
	if a == b {
		return true
	}
	sa, oka := PositionSpan(a)
	sb, okb := PositionSpan(b)
	return oka && okb && sa == sb
}
