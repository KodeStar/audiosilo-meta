package model

import (
	"regexp"
	"strconv"
	"strings"
)

// position.go holds the ONE reading of a series position's numeric span.
//
// It lives in pkg/model - the leaf every other package can reach - beside the
// schema's own position rules, because two packages that may not import each
// other both need it: internal/serve orders a series listing by it and compares a
// user's stated volume against it, and internal/audit judges range bounds and
// slot identity with it. internal/serve's own doc comment nominated this home for
// exactly that case, and the audit is the outside consumer it was waiting for.
//
// What it deliberately does NOT do is decide whether a position is WELL-FORMED.
// ParseFloat is liberal - it accepts "1e2", "+2", "Inf" and " 2 " - and the
// grammar the data model allows is narrower (a number, or two joined by a hyphen;
// see series.schema.json and NormalizeSequence below, the rule of record for
// acceptance and canonical spelling). A caller that cares about well-formedness
// normalizes FIRST and reads the span second - PositionSpan, PositionRange and SameSlot
// are that two-step - while a caller that only needs a sort key or a span comparison
// can read it directly, which is what internal/serve does with values a validated tree
// already vouched for.

// ParsePositionRange parses a series position string into its numeric span:
// "2" -> (2, 2), "2.5" -> (2.5, 2.5), "1-3.5" -> (1, 3.5). ok is false when
// either bound fails to parse.
//
// Callers apply their own policy to the span (sort key, integer coverage,
// equality, range validity). See the package note above on why this is a span
// reader and not a validator.
func ParsePositionRange(pos string) (lo, hi float64, ok bool) {
	pos = strings.TrimSpace(pos)
	if i := strings.IndexByte(pos, '-'); i > 0 {
		var err1, err2 error
		lo, err1 = strconv.ParseFloat(strings.TrimSpace(pos[:i]), 64)
		hi, err2 = strconv.ParseFloat(strings.TrimSpace(pos[i+1:]), 64)
		return lo, hi, err1 == nil && err2 == nil
	}
	f, err := strconv.ParseFloat(pos, 64)
	return f, f, err == nil
}

// sequencePattern matches a series position AS A SOURCE MAY SPELL IT: a number,
// or an omnibus range whose dash may carry whitespace on either side. The
// schema's own pattern (series.schema.json) admits no whitespace at all, which
// is the point of NORMALIZING rather than merely validating - what a source
// typed and what the record stores are two different strings, and only the
// canonical one is ever written.
//
// The whitespace tolerance was measured over the full 1.13M-book dump: of the
// 345,140 stated series positions, 4,080 fail the schema pattern and 104 of
// those (100 distinct books, 53 distinct spellings) are an omnibus range spelled
// with a space - "1 - 3", "3040 - 3049", and the one-sided "14 -15", "1.5- 3.5".
// Every one of them was dropped, which cost the book its place in its series.
// ZERO of the 104 use an en or em dash, so the dash class stays the plain hyphen
// the schema pattern names: the tolerance is whitespace, nothing else.
var sequencePattern = regexp.MustCompile(`^\d+(\.\d+)?(\s*-\s*\d+(\.\d+)?)?$`)

// NormalizeSequence trims a raw series_sequence, canonicalizes it, and reports
// whether it is a valid position (a single number or a range like "1-3.5").
//
// A position is a STRING in the schema, so two spellings of the same number are
// two different positions to every rule that compares them (series membership,
// the position-uniqueness check, the importer's same-position merge test).
// Sources spell them differently - a Postgres numeric renders "1" as "1.0", and
// a range is written both "1-3" and "1 - 3" - so trailing fractional zeros are
// stripped ("1.0" -> "1", "2.50" -> "2.5", both endpoints of a range) and the
// whitespace around a range's dash is removed. One book cannot occupy a series
// twice, and what is stored always satisfies the schema pattern.
func NormalizeSequence(raw string) (pos string, ok bool) {
	pos = strings.TrimSpace(raw)
	if pos == "" || !sequencePattern.MatchString(pos) {
		return "", false
	}
	// Removing ALL whitespace is equivalent to removing it around the dash: the
	// value has been trimmed and has matched sequencePattern, whose only
	// whitespace is the `\s*` on either side of that dash. A regexp replace here
	// cost ~130ns and an allocation on every position; the guard keeps the
	// overwhelmingly common tight spelling free.
	if strings.ContainsAny(pos, " \t\n\v\f\r") {
		pos = strings.Join(strings.Fields(pos), "")
	}
	if !strings.Contains(pos, ".") {
		return pos, true
	}
	parts := strings.Split(pos, "-")
	for i, part := range parts {
		parts[i] = trimFractionalZeros(part)
	}
	return strings.Join(parts, "-"), true
}

// trimFractionalZeros drops the trailing zeros (and then a bare trailing dot) of
// a decimal number. It is only ever called on a value the sequence pattern
// matched, and only touches a value that HAS a fractional part - so "10" keeps
// its zero.
func trimFractionalZeros(n string) string {
	if !strings.Contains(n, ".") {
		return n
	}
	return strings.TrimSuffix(strings.TrimRight(n, "0"), ".")
}

// The SERIES-POSITION primitive every reader of a membership goes through: what
// numeric span a stored position occupies, and whether two spellings name one slot.
//
// It lives HERE, in the leaf, beside NormalizeSequence (the rule of record it composes
// over), so every package can reach it: internal/importer re-exports it under its
// long-standing names for internal/audit, internal/repair and internal/issueform, and
// pkg/check - which internal/importer imports, so it cannot import that package -
// calls it directly for its duplicate-series advisory.
//
// The two-step below had grown FIVE spellings across internal/audit and
// internal/repair (a slot key, a span map, a range reader, a merge's slot comparison, a
// gap census), and pkg/check briefly held a sixth; a rule with that many spellings is a
// rule that will disagree with itself. A position is a string in the data
// model, so "02", "2" and "2.0" are three different values that name one slot, and any
// rule that compares two of them has to say so the same way.
//
// THE ORDER IS LOAD-BEARING, and it is stated once, here. Acceptance goes through
// NormalizeSequence - the rule of record for what a position may be and how it
// is spelled, which also tolerates the 104 real "1 - 3" range spellings the schema
// pattern rejects - and only then is the span read with ParsePositionRange. Doing
// it the other way round mints slots for values the data model rejects: the span reader
// is a ParseFloat, so it happily accepts "1e2", "+2" and "Inf", and a caller would then
// report those same values as malformed a rule later.

// PositionSpan is the numeric range a stored position occupies: [n, n] for a single
// position, [lo, hi] for an omnibus range. ok is false for anything the position grammar
// rejects.
func PositionSpan(pos string) (span [2]float64, ok bool) {
	norm, ok := NormalizeSequence(pos)
	if !ok {
		return [2]float64{}, false
	}
	lo, hi, ok := ParsePositionRange(norm)
	if !ok {
		return [2]float64{}, false
	}
	return [2]float64{lo, hi}, true
}

// PositionRange is PositionSpan restricted to the RANGE spellings: isRange is false for
// a plain number, which is not a range rather than a malformed one.
func PositionRange(pos string) (lo, hi float64, isRange bool) {
	norm, ok := NormalizeSequence(pos)
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
