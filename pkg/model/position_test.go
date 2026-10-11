package model

import (
	"encoding/json"
	"regexp"
	"testing"

	meta "github.com/kodestar/audiosilo-meta"
)

// The two-step's ORDER is what these pin: acceptance through NormalizeSequence FIRST, then
// the span. ParsePositionRange is a ParseFloat, so reading the span first would mint
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

func TestNormalizeSequence(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"3", "3", true},
		{"0.5", "0.5", true},
		{"1-3.5", "1-3.5", true},
		{" 2 ", "2", true},
		{"12-13", "12-13", true},
		// A position is a STRING, so two spellings of one number would be two
		// different positions: trailing fractional zeros are canonicalized away
		// (a Postgres numeric renders book 1 as "1.0"). "10" keeps its zero.
		{"1.0", "1", true},
		{"2.50", "2.5", true},
		{"1.00", "1", true},
		{"10", "10", true},
		{"10.0", "10", true},
		{"1.0-3.50", "1-3.5", true},
		// A source spells an omnibus range with spaces around the dash. All four
		// spellings below are real dump values (104 stated positions over 100
		// books were dropped for this before the tolerance existed); the schema
		// admits no whitespace, so what is STORED is always the tight form.
		{"1 - 3", "1-3", true},
		{"3040 - 3049", "3040-3049", true},
		{"14 -15", "14-15", true},
		{"1.5- 3.5", "1.5-3.5", true},
		{" 0.1 - 0.5 ", "0.1-0.5", true},
		{"", "", false},
		{"one", "", false},
		{"1.2.3", "", false},
		{"1-", "", false},
		{"-2", "", false},
		// The tolerance is WHITESPACE, not a wider dash class: zero of the 104
		// dump spellings use an en or em dash, and the schema pattern names the
		// plain hyphen.
		{"1 – 3", "", false},
		{"1 — 3", "", false},
		// Still not a range: a dash with nothing on one side of it, however it is
		// spaced, and two numbers with no dash at all.
		{"1 - ", "", false},
		{" - 3", "", false},
		{"1 3", "", false},
	}
	for _, c := range cases {
		got, ok := NormalizeSequence(c.in)
		if got != c.want || ok != c.wantOK {
			t.Errorf("NormalizeSequence(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

// seriesPositionSchemaPattern reads the position pattern out of
// series.schema.json, so the guard below compares against the contract itself
// rather than a re-spelling of it.
func seriesPositionSchemaPattern(t *testing.T) string {
	t.Helper()
	raw, err := meta.SchemaFS.ReadFile("schema/series.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Properties struct {
			Works struct {
				Items struct {
					Properties struct {
						Position struct {
							Pattern string `json:"pattern"`
						} `json:"position"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"works"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	pattern := doc.Properties.Works.Items.Properties.Position.Pattern
	if pattern == "" {
		t.Fatal("series.schema.json states no position pattern; the drift guard would pass vacuously")
	}
	return pattern
}

// TestNormalizeSequenceSatisfiesTheSchema is the drift guard between the
// TOLERANT input grammar and the contract it writes into: whatever
// NormalizeSequence accepts, what it RETURNS must match series.schema.json's
// position pattern exactly, or a spelled range would be normalized into a
// record metacheck then rejects.
func TestNormalizeSequenceSatisfiesTheSchema(t *testing.T) {
	schemaPattern := regexp.MustCompile(seriesPositionSchemaPattern(t))
	inputs := []string{
		"1", "0.5", "1-3.5", "1 - 3", "3040 - 3049", "14 -15", "1.5- 3.5",
		" 2 ", "1.0", "2.50", "1.0-3.50", " 0.1 - 0.5 ",
	}
	for _, in := range inputs {
		pos, ok := NormalizeSequence(in)
		if !ok {
			t.Errorf("NormalizeSequence(%q) was rejected", in)
			continue
		}
		if !schemaPattern.MatchString(pos) {
			t.Errorf("NormalizeSequence(%q) = %q, which the schema's position pattern rejects", in, pos)
		}
	}
}
