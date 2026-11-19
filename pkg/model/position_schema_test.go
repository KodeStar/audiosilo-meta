package model_test

import (
	"regexp"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// TestNormalizeSequenceSatisfiesTheSchema is the drift guard between the
// TOLERANT input grammar and the contract it writes into: whatever
// NormalizeSequence accepts, what it RETURNS must match series.schema.json's
// position pattern exactly, or a spelled range would be normalized into a
// record metacheck then rejects.
func TestNormalizeSequenceSatisfiesTheSchema(t *testing.T) {
	schemaPattern := regexp.MustCompile(testpack.SeriesPositionPattern(t))
	inputs := []string{
		"1", "0.5", "1-3.5", "1 - 3", "3040 - 3049", "14 -15", "1.5- 3.5",
		" 2 ", "1.0", "2.50", "1.0-3.50", " 0.1 - 0.5 ",
	}
	for _, in := range inputs {
		pos, ok := model.NormalizeSequence(in)
		if !ok {
			t.Errorf("NormalizeSequence(%q) was rejected", in)
			continue
		}
		if !schemaPattern.MatchString(pos) {
			t.Errorf("NormalizeSequence(%q) = %q, which the schema's position pattern rejects", in, pos)
		}
	}
}
