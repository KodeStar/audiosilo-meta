package importer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// unaddressableSeriesRows are three libex rows shaped like the ones that exposed
// the problem: two Russian books claiming DIFFERENT Cyrillic series (both names
// slug away to nothing, taken from a real seed wave), and one control row
// claiming an ordinary series.
//
// The two Cyrillic claims are the whole point: under the old fallback they both
// resolved to the base "series", so the first row seen took that slug and the
// second took "series-2" - an identity that says only which row arrived first.
func unaddressableSeriesRows() []string {
	return []string{
		`{"asin":"B0RUSSIA01","title":"Transformation One","region":"us","language":"russian",` +
			`"bookFormat":"unabridged","lengthMinutes":300,"authors":[{"name":"Ivan Petrov"}],` +
			`"narrators":[{"name":"Olga Volkova"}],"series":[{"name":"Трансформация.","position":"1"}]}`,
		`{"asin":"B0RUSSIA02","title":"Sorcerer One","region":"us","language":"russian",` +
			`"bookFormat":"unabridged","lengthMinutes":300,"authors":[{"name":"Ivan Petrov"}],` +
			`"narrators":[{"name":"Olga Volkova"}],"series":[{"name":"Колдун","position":"1"}]}`,
		`{"asin":"B0CONTROL1","title":"Cartographer One","region":"us","language":"english",` +
			`"bookFormat":"unabridged","lengthMinutes":300,"authors":[{"name":"Ada Mapper"}],` +
			`"narrators":[{"name":"Olga Volkova"}],"series":[{"name":"Cartographer Chronicles","position":"1"}]}`,
	}
}

// TestUnaddressableSeriesClaimIsRefused is the rule's acceptance test: a series
// whose NAME has no addressable slug is not minted under a degenerate
// series/series-2 identity. The books still import - only the series placement is
// dropped - and the control series is created normally.
func TestUnaddressableSeriesClaimIsRefused(t *testing.T) {
	sum, dataDir := runLibex(t, strings.Join(unaddressableSeriesRows(), "\n")+"\n", false)

	if sum.SkippedRows != 0 {
		t.Errorf("SkippedRows = %d, want 0: an unaddressable SERIES refuses the claim, not the row", sum.SkippedRows)
	}
	if sum.NewWorks != 3 || sum.NewRecordings != 3 {
		t.Errorf("summary = %d works / %d recordings, want 3/3 (every row still imports)",
			sum.NewWorks, sum.NewRecordings)
	}
	if sum.NewSeries != 1 {
		t.Errorf("NewSeries = %d, want 1 (the control series only)", sum.NewSeries)
	}
	for _, rel := range []string{
		"works/tr/transformation-one/work.json",
		"works/so/sorcerer-one/work.json",
		"works/ca/cartographer-one/work.json",
	} {
		if !entryExists(t, dataDir, rel) {
			t.Errorf("%s was not imported; only the series claim should be dropped", rel)
		}
	}
	if !entryExists(t, dataDir, "series/ca/cartographer-chronicles.json") {
		t.Error("the control series was not created")
	}
	for _, slug := range []string{"series", "series-2", "series-3"} {
		if entryExists(t, dataDir, "series/se/"+slug+".json") {
			t.Errorf("a degenerate series slug %q was minted", slug)
		}
	}
	// No series entry anywhere carries a name the catalogue cannot address.
	for _, name := range seriesNames(t, dataDir) {
		if Slugify(name) == "" {
			t.Errorf("series %q has no addressable slug but was written anyway", name)
		}
	}

	// One aggregated warning, naming the count and a few examples - the
	// unnamed-credit posture, not 32 identical per-row lines.
	var line string
	for _, w := range sum.Warnings {
		if strings.Contains(w, "series claims dropped") {
			line = w
		}
	}
	if line == "" {
		t.Fatalf("no aggregated warning for the dropped series claims: %v", sum.Warnings)
	}
	if !strings.HasPrefix(line, "2 series claims dropped") {
		t.Errorf("warning = %q, want it to count both refused claims", line)
	}
	if !strings.Contains(line, "Трансформация.") || !strings.Contains(line, "Колдун") {
		t.Errorf("warning %q names neither refused series", line)
	}
}

// TestUnaddressableSeriesIsOrderIndependent is why the refusal beats a
// better-behaved fallback. The old base-plus-number chain handed out slugs in
// ARRIVAL order, so the same input in a different order produced a different
// tree - a non-reproducible identity, and a rebuild that silently moved works
// between series. Refusing the claim is order-independent by construction, and
// this pins it: the two orderings agree byte for byte.
func TestUnaddressableSeriesIsOrderIndependent(t *testing.T) {
	rows := unaddressableSeriesRows()
	_, forward := runLibex(t, strings.Join(rows, "\n")+"\n", false)
	reversed := []string{rows[2], rows[1], rows[0]}
	_, backward := runLibex(t, strings.Join(reversed, "\n")+"\n", false)

	if got, want := seriesTree(t, backward), seriesTree(t, forward); got != want {
		t.Errorf("the series family depends on row order:\nforward:\n%s\nbackward:\n%s", want, got)
	}
}

// seriesNames returns every series name in the tree, read straight out of the
// pack files so nothing about the expected slugs is assumed.
func seriesNames(t *testing.T, dataDir string) []string {
	t.Helper()
	var names []string
	root := filepath.Join(dataDir, "series")
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Ext(path) != ".json" {
			return err
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		var pack struct {
			Entries map[string]struct {
				Name string `json:"name"`
			} `json:"entries"`
		}
		if jsonErr := json.Unmarshal(raw, &pack); jsonErr != nil {
			t.Fatalf("%s: %v", path, jsonErr)
		}
		for _, entry := range pack.Entries {
			names = append(names, entry.Name)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return names
}

// seriesTree renders the whole series family as one string, for comparing two
// runs.
func seriesTree(t *testing.T, dataDir string) string {
	t.Helper()
	var b strings.Builder
	root := filepath.Join(dataDir, "series")
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, _ := filepath.Rel(root, path)
		b.WriteString(rel + "\n" + string(raw) + "\n")
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return b.String()
}
