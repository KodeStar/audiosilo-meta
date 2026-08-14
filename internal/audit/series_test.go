package audit

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// works seeds n plainly-titled works, for the series rules that only need members.
func works(t testing.TB, ids ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, id := range ids {
		out["works/xx/"+id+"/work.json"] = workJSON(t, id, strings.ToUpper(id[:1])+id[1:])
		out["works/xx/"+id+"/recordings/r-"+id+".json"] = recJSON(t, "r-"+id, id)
	}
	return out
}

// seriesFixture merges the baseline, some works and the caller's extra files.
func seriesFixture(t testing.TB, workIDs []string, extra map[string]string) map[string]string {
	t.Helper()
	out := fixture(t, works(t, workIDs...))
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func TestSeriesIntegrityReportsADuplicatePosition(t *testing.T) {
	// metacheck refuses this, which is why the fixture is allowed to fail
	// validation - the audit must still name it rather than crash.
	rep, res := runFixtureAllowingProblems(t, seriesFixture(t, []string{"one", "two"}, map[string]string{
		"series/du/dup.json": seriesJSON(t, "dup", "Dup Chronicles", "one@1", "two@1"),
	}))
	if len(res.Problems) == 0 {
		t.Fatal("the fixture was supposed to violate a metacheck rule")
	}
	got := subclassOf(t, rep, ClassSeriesInteg, sDupPosition)
	if len(got) != 1 {
		t.Fatalf("want one duplicate-position record, got %d", len(got))
	}
	if want := []string{"one", "two"}; !reflect.DeepEqual(workIDs(got[0]), want) {
		t.Errorf("works = %v, want %v", workIDs(got[0]), want)
	}
	// And the loader's own refusal travels through too.
	if len(subclassOf(t, rep, ClassLoader, "problem")) == 0 {
		t.Error("pkg/check's problem was not carried into the report")
	}
}

func TestSeriesIntegrityReportsPositionGaps(t *testing.T) {
	rep := runFixture(t, seriesFixture(t, []string{"one", "four"}, map[string]string{
		"series/ga/gappy.json": seriesJSON(t, "gappy", "Gappy Chronicles", "one@1", "four@4"),
	}))
	got := subclassOf(t, rep, ClassSeriesInteg, sGap)
	if len(got) != 1 {
		t.Fatalf("want one gap record, got %d", len(got))
	}
	note := strings.Join(got[0].Notes, " ")
	if !strings.Contains(note, "2, 3") {
		t.Errorf("the missing positions are not named: %q", note)
	}
	if !strings.Contains(got[0].Action, "advisory") {
		t.Errorf("the gap rule must be advisory: %q", got[0].Action)
	}
}

func TestSeriesIntegrityIsSilentOnAContiguousSeries(t *testing.T) {
	rep := runFixture(t, seriesFixture(t, []string{"one", "two", "three"}, map[string]string{
		"series/ti/tidy.json": seriesJSON(t, "tidy", "Tidy Chronicles", "one@1", "two@2", "three@3"),
	}))
	if got := classOf(t, rep, ClassSeriesInteg); len(got) != 0 {
		t.Errorf("a tidy series produced findings: %+v", got)
	}
}

// A decimal novella does not fill an integer slot, and an omnibus RANGE beside its
// members is the modeled concept - neither is a gap or a defect.
func TestSeriesIntegrityAcceptsDecimalsAndOmnibusRanges(t *testing.T) {
	files := seriesFixture(t, []string{"one", "two", "three"}, map[string]string{
		"series/mi/mixed.json": seriesJSON(t, "mixed", "Mixed Chronicles",
			"one@1", "two@2", "three@3", "collected@1-3"),
	})
	files["works/xx/collected/work.json"] = workJSON(t, "collected", "Mixed Chronicles Omnibus")
	files["works/xx/collected/recordings/r-collected.json"] = recJSON(t, "r-collected", "collected")
	files["works/xx/novella/work.json"] = workJSON(t, "novella", "A Mixed Novella")
	files["works/xx/novella/recordings/r-novella.json"] = recJSON(t, "r-novella", "novella")
	files["series/mi/mixed.json"] = seriesJSON(t, "mixed", "Mixed Chronicles",
		"one@1", "two@2", "three@3", "collected@1-3", "novella@2.5")

	rep := runFixture(t, files)
	for _, f := range classOf(t, rep, ClassSeriesInteg) {
		t.Errorf("the modeled omnibus/decimal shape produced a finding: %s %s", f.Subclass, f.Key)
	}
}

// A work whose own title says it is a collection, sitting on a single integer slot,
// is the suspicious shape: it occupies a volume's place.
func TestSeriesIntegrityFlagsAnOmnibusOnASingleSlot(t *testing.T) {
	files := seriesFixture(t, []string{"one"}, map[string]string{})
	files["works/xx/boxed/work.json"] = workJSON(t, "boxed", "Chronicles Box Set")
	files["works/xx/boxed/recordings/r-boxed.json"] = recJSON(t, "r-boxed", "boxed")
	files["series/sq/squatted.json"] = seriesJSON(t, "squatted", "Squatted Chronicles", "one@1", "boxed@2")

	rep := runFixture(t, files)
	got := subclassOf(t, rep, ClassSeriesInteg, sOmnibusSlot)
	if len(got) != 1 {
		t.Fatalf("want one omnibus-in-a-slot record, got %d: %+v", len(got), classOf(t, rep, ClassSeriesInteg))
	}
	if got[0].Propose.From != "2" {
		t.Errorf("have = %q, want the occupied position", got[0].Propose.From)
	}
}

func TestSeriesIntegrityFlagsAMinorityLanguageMember(t *testing.T) {
	files := seriesFixture(t, []string{"one", "two"}, map[string]string{})
	files["works/xx/drei/work.json"] = workJSON(t, "drei", "Drei", withLanguage("de"))
	files["works/xx/drei/recordings/r-drei.json"] = recJSON(t, "r-drei", "drei")
	files["series/mo/mostly-en.json"] = seriesJSON(t, "mostly-en", "Mostly English", "one@1", "two@2", "drei@3")

	rep := runFixture(t, files)
	got := subclassOf(t, rep, ClassSeriesInteg, sMinorityLanguage)
	if len(got) != 1 {
		t.Fatalf("want one minority-language record, got %d", len(got))
	}
	if got[0].Propose.To != "en" || got[0].Propose.From != "de" {
		t.Errorf("record = have %q want %q, expected de -> en", got[0].Propose.From, got[0].Propose.To)
	}
	if want := []string{"drei"}; !reflect.DeepEqual(workIDs(got[0]), want) {
		t.Errorf("works = %v, want %v", workIDs(got[0]), want)
	}
}

// A one-to-one language split has no majority, so nothing is a minority OF it.
func TestSeriesIntegrityNeedsAStrictMajorityToCallSomethingAMinority(t *testing.T) {
	files := fixture(t, works(t, "one"))
	files["works/xx/zwei/work.json"] = workJSON(t, "zwei", "Zwei", withLanguage("de"))
	files["works/xx/zwei/recordings/r-zwei.json"] = recJSON(t, "r-zwei", "zwei")
	files["series/sp/split.json"] = seriesJSON(t, "split", "Split Chronicles", "one@1", "zwei@2")

	rep := runFixture(t, files)
	if got := subclassOf(t, rep, ClassSeriesInteg, sMinorityLanguage); len(got) != 0 {
		t.Errorf("a tied language split was reported as a minority: %+v", got)
	}
}

// A range whose bounds are the same NUMBER spans nothing: "2.1-2.10" reads as 2.1
// to 2.1, the shape a season/episode numbering takes when written as a decimal.
func TestSeriesIntegrityFlagsARangeThatSpansNothing(t *testing.T) {
	rep := runFixture(t, seriesFixture(t, []string{"one"}, map[string]string{
		"series/se/seasons.json": seriesJSON(t, "seasons", "Season Chronicles", "one@2.1-2.10"),
	}))
	got := subclassOf(t, rep, ClassSeriesInteg, sMalformed)
	if len(got) != 1 {
		t.Fatalf("want one malformed-position record, got %d: %+v", len(got), classOf(t, rep, ClassSeriesInteg))
	}
	if !strings.Contains(got[0].Action, "same NUMBER") {
		t.Errorf("action = %q, want it to explain the decimal", got[0].Action)
	}
}

func TestSeriesIntegrityReportsADanglingMember(t *testing.T) {
	rep, res := runFixtureAllowingProblems(t, seriesFixture(t, []string{"one"}, map[string]string{
		"series/gh/ghosts.json": seriesJSON(t, "ghosts", "Ghost Chronicles", "one@1", "nobody@2"),
	}))
	if len(res.Problems) == 0 {
		t.Fatal("a dangling member was supposed to fail validation")
	}
	got := subclassOf(t, rep, ClassSeriesInteg, sDanglingWork)
	if len(got) != 1 {
		t.Fatalf("want one dangling-work record, got %d", len(got))
	}
	if got[0].Propose.From != "nobody" {
		t.Errorf("have = %q, want the missing work id", got[0].Propose.From)
	}
}

func TestSeriesDupGroupsOneNameSpelledTwoWays(t *testing.T) {
	cases := []struct{ a, b string }{
		{"Dragon Heart", "Dragon Heart Series"},
		{"Richard Sharpe", "Richard Sharpe Novels"},
		{"The Primal Hunter", "Primal Hunter"},
		{"Cafe Society", "Café Society"},
		{"3 a.m.", "The 3 A.M."},
	}
	for _, c := range cases {
		t.Run(c.a+" / "+c.b, func(t *testing.T) {
			rep := runFixture(t, seriesFixture(t, []string{"one", "two"}, map[string]string{
				"series/aa/alpha.json": seriesJSON(t, "alpha", c.a, "one@1"),
				"series/bb/beta.json":  seriesJSON(t, "beta", c.b, "two@1"),
			}))
			got := subclassOf(t, rep, ClassSeriesDup, serDupName)
			if len(got) != 1 {
				t.Fatalf("want one cluster for %q / %q, got %d", c.a, c.b, len(got))
			}
			if len(got[0].Series) != 2 {
				t.Errorf("cluster cites %d series, want 2", len(got[0].Series))
			}
			if got[0].Propose.Target == "" {
				t.Error("no canonical series proposed")
			}
		})
	}
}

func TestSeriesDupIsSilentOnTwoRealSeries(t *testing.T) {
	rep := runFixture(t, seriesFixture(t, []string{"one", "two"}, map[string]string{
		"series/aa/alpha.json": seriesJSON(t, "alpha", "Dragon Heart", "one@1"),
		"series/bb/beta.json":  seriesJSON(t, "beta", "Dragon Blood", "two@1"),
	}))
	if got := classOf(t, rep, ClassSeriesDup); len(got) != 0 {
		t.Errorf("two different series were grouped: %+v", got)
	}
}

// A trailing "Saga" is part of a real series name at least as often as it is
// decoration, so the looser key gets its own subclass and proposes no merge.
func TestSeriesDupFilesASagaSuffixSeparately(t *testing.T) {
	rep := runFixture(t, seriesFixture(t, []string{"one", "two"}, map[string]string{
		"series/aa/alpha.json": seriesJSON(t, "alpha", "Vorkosigan", "one@1"),
		"series/bb/beta.json":  seriesJSON(t, "beta", "Vorkosigan Saga", "two@1"),
	}))
	if got := subclassOf(t, rep, ClassSeriesDup, serDupName); len(got) != 0 {
		t.Errorf("a Saga suffix was folded into the tight key: %+v", got)
	}
	got := subclassOf(t, rep, ClassSeriesDup, serDupSaga)
	if len(got) != 1 {
		t.Fatalf("want one suffix-saga record, got %d", len(got))
	}
	if !strings.Contains(got[0].Action, "no merge is proposed") {
		t.Errorf("action = %q, want it to withhold a merge", got[0].Action)
	}
}

func TestSeriesParenReportsADecoratedNameWithASibling(t *testing.T) {
	rep := runFixture(t, seriesFixture(t, []string{"one", "two"}, map[string]string{
		"series/aa/vork.json":       seriesJSON(t, "vork", "Vorkosigan Saga", "one@1"),
		"series/bb/vork-chron.json": seriesJSON(t, "vork-chron", "Vorkosigan Saga (chronological)", "two@1"),
	}))
	got := subclassOf(t, rep, ClassSeriesParen, serParenPair)
	if len(got) != 1 {
		t.Fatalf("want one decorated-pair record, got %d: %+v", len(got), classOf(t, rep, ClassSeriesParen))
	}
	if got[0].Key != "vork-chron" {
		t.Errorf("key = %q, want the decorated series", got[0].Key)
	}
	if !strings.Contains(strings.Join(got[0].Notes, " "), "vork") {
		t.Errorf("the undecorated sibling is not named: %v", got[0].Notes)
	}
	if !strings.Contains(got[0].Action, "never merged automatically") {
		t.Errorf("action = %q, want it to refuse an automatic merge", got[0].Action)
	}
}

func TestSeriesParenReportsADecoratedNameWithNoSibling(t *testing.T) {
	rep := runFixture(t, seriesFixture(t, []string{"one"}, map[string]string{
		"series/aa/spanish.json": seriesJSON(t, "spanish", "365 Days [Spanish Edition]", "one@1"),
	}))
	got := subclassOf(t, rep, ClassSeriesParen, serParenSolo)
	if len(got) != 1 {
		t.Fatalf("want one decorated-only record, got %d", len(got))
	}
	if got[0].Propose.To != "365 Days" {
		t.Errorf("want = %q, expected the undecorated name", got[0].Propose.To)
	}
}

func TestSeriesParenIsSilentOnAPlainName(t *testing.T) {
	rep := runFixture(t, seriesFixture(t, []string{"one"}, map[string]string{
		"series/aa/plain.json": seriesJSON(t, "plain", "Dragon Heart", "one@1"),
	}))
	if got := classOf(t, rep, ClassSeriesParen); len(got) != 0 {
		t.Errorf("a plain series name was reported: %+v", got)
	}
}

// The empty-series rule cannot be reached through a fixture TREE: the schema's
// minItems refuses a memberless series, so pkg/check would never hand one over.
// It is a defensive rule for a catalogue assembled some other way (a consumer of
// pkg/check's exported Catalog, which is public API), so it is driven directly.
func TestSeriesIntegrityReportsAnEmptySeries(t *testing.T) {
	ix := newIndex(&model.Catalog{Series: []*model.Series{{
		ID: "empty", Name: "Empty Chronicles", License: "CC0-1.0",
	}}})
	f := detectSeriesIntegrity(ix)
	f.finalize()
	got := f.rows
	if len(got) != 1 || got[0].Subclass != sEmpty {
		t.Fatalf("want one empty-series record, got %+v", got)
	}
	if !strings.Contains(got[0].Action, "unreachable") {
		t.Errorf("action = %q", got[0].Action)
	}
}

func TestPositionKeyNormalizesNumbersAndRejectsRanges(t *testing.T) {
	cases := []struct{ in, want string }{
		{"2", "2"}, {"02", "2"}, {"2.0", "2"}, {"2.5", "2.5"}, {"2.50", "2.5"},
		{"1-3", ""}, {"", ""}, {"abc", ""},
	}
	for _, c := range cases {
		if got := positionKey(c.in); got != c.want {
			t.Errorf("positionKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
