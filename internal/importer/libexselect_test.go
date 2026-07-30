package importer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedSelectCatalogue writes the catalogue every selection test selects
// against: one series ("Cartographer Chronicles") holding volume 1, whose
// recording already carries ASIN B0PRESENT1.
func seedSelectCatalogue(t *testing.T) string {
	t.Helper()
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{
		"people/ad/ada-mapmaker.json": `{"id":"ada-mapmaker","license":"CC0-1.0","name":"Ada Mapmaker","sources":[{"type":"user"}]}`,
		"people/be/bea-reader.json":   `{"id":"bea-reader","license":"CC0-1.0","name":"Bea Reader","sources":[{"type":"user"}]}`,
		"works/vo/volume-one/work.json": `{"authors":["ada-mapmaker"],"id":"volume-one","language":"en","license":"CC0-1.0",` +
			`"sources":[{"type":"user"}],"title":"Volume One"}`,
		"works/vo/volume-one/recordings/bea-reader-2024.json": `{"asin":[{"asin":"B0PRESENT1","region":"us"}],"id":"bea-reader-2024",` +
			`"language":"en","license":"CC0-1.0","narrators":["bea-reader"],"sources":[{"type":"user"}],"work":"volume-one"}`,
		"series/ca/cartographer-chronicles.json": `{"id":"cartographer-chronicles","license":"CC0-1.0","name":"Cartographer Chronicles",` +
			`"sources":[{"type":"user"}],"works":[{"position":"1","work":"volume-one"}]}`,
	})
	return dataDir
}

// selectRow renders one compact libex row. Rows are written compact and on one
// line so the byte-identity assertion is meaningful: the selector must pass a
// row through verbatim, never re-render it.
func selectRow(asin, title, region, language, series, position string) string {
	seriesJSON := "[]"
	if series != "" {
		seriesJSON = `[{"name":"` + series + `","position":"` + position + `"}]`
	}
	return `{"asin":"` + asin + `","title":"` + title + `","region":"` + region + `","language":"` + language +
		`","authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}],"series":` + seriesJSON + `}`
}

const seriesName = "Cartographer Chronicles"

// selectExportRows is the export every selection test reads: one selectable
// volume, one book in no catalogue series, an already-present ASIN, an
// unknown-language volume, an unmapped-region volume, two per-region sibling
// rows of one title, and two further volumes (so a cap has something to cut).
func selectExportRows() []string {
	return []string{
		selectRow("B0SELECT02", "Volume Two", "us", "english", seriesName, "2"),
		selectRow("B0OTHER001", "Unrelated Book", "us", "english", "Some Other Series", "1"),
		selectRow("B0PRESENT1", "Volume One", "us", "english", seriesName, "1"),
		selectRow("B0LANG0003", "Volume Three", "us", "klingon", seriesName, "3"),
		selectRow("B0REGION04", "Volume Four", "zz", "english", seriesName, "4"),
		selectRow("B0SIBLIN5A", "Volume Five", "us", "english", seriesName, "5"),
		selectRow("B0SIBLIN5B", "Volume Five", "gb", "english", seriesName, "5"),
		selectRow("B0SELECT06", "Volume Six", "us", "english", seriesName, "6"),
		selectRow("B0SELECT07", "Volume Seven", "us", "english", seriesName, "7"),
	}
}

// runSelect writes rows as NDJSON, selects against dataDir, and returns the
// report plus the output file's lines.
func runSelect(t *testing.T, dataDir string, rows []string, maxPerSeries int) (SelectResult, []string) {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, "export.ndjson")
	if err := os.WriteFile(in, []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "subset.ndjson")
	res, err := SelectLibex(in, out, SelectOptions{DataDir: dataDir, MaxPerSeries: maxPerSeries})
	if err != nil {
		t.Fatalf("SelectLibex: %v", err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read subset: %v", err)
	}
	body := strings.TrimSuffix(string(raw), "\n")
	if body == "" {
		return res, nil
	}
	return res, strings.Split(body, "\n")
}

func TestLibexSelectSeriesCompletion(t *testing.T) {
	dataDir := seedSelectCatalogue(t)
	rows := selectExportRows()
	res, lines := runSelect(t, dataDir, rows, 0)

	if len(res.Warnings) != 0 {
		t.Errorf("warnings = %v, want none", res.Warnings)
	}
	if res.RowsRead != 9 {
		t.Errorf("RowsRead = %d, want 9", res.RowsRead)
	}
	// Volume Two + both Volume Five siblings + Volume Six + Volume Seven.
	if res.RowsSelected != 5 {
		t.Errorf("RowsSelected = %d, want 5", res.RowsSelected)
	}
	// The two sibling rows are one projected work, not two.
	if res.ProjectedWorks != 4 {
		t.Errorf("ProjectedWorks = %d, want 4", res.ProjectedWorks)
	}
	if res.SeriesMatched != 1 {
		t.Errorf("SeriesMatched = %d, want 1", res.SeriesMatched)
	}

	want := map[string]int{
		reasonAlreadyASIN: 1,
		reasonNoSeries:    1,
		reasonLanguage:    1,
		reasonRegion:      1,
	}
	for _, reason := range reasonOrder {
		if res.Excluded[reason] != want[reason] {
			t.Errorf("excluded[%s] = %d, want %d", reason, res.Excluded[reason], want[reason])
		}
	}

	// Every row read is accounted for.
	var excluded int
	for _, n := range res.Excluded {
		excluded += n
	}
	if res.RowsSelected+excluded != res.RowsRead {
		t.Errorf("selected %d + excluded %d != read %d", res.RowsSelected, excluded, res.RowsRead)
	}

	// The output rows are the input rows, byte for byte and in input order.
	wantLines := []string{rows[0], rows[5], rows[6], rows[7], rows[8]}
	if len(lines) != len(wantLines) {
		t.Fatalf("output has %d lines, want %d:\n%s", len(lines), len(wantLines), strings.Join(lines, "\n"))
	}
	for i, line := range lines {
		if line != wantLines[i] {
			t.Errorf("output line %d:\n got %s\nwant %s", i, line, wantLines[i])
		}
	}

	// Per-series breakdown.
	if len(res.PerSeries) != 1 {
		t.Fatalf("PerSeries = %+v, want one entry", res.PerSeries)
	}
	s := res.PerSeries[0]
	if s.Series != "cartographer-chronicles" || s.Name != seriesName {
		t.Errorf("PerSeries[0] identity = %q/%q", s.Series, s.Name)
	}
	if s.Works != 4 || s.Rows != 5 || s.CutWorks != 0 || s.CutRows != 0 {
		t.Errorf("PerSeries[0] = %+v, want 4 works / 5 rows / no cut", s)
	}
}

// TestLibexSelectOutputRoundTrips feeds the emitted subset straight back into
// the libex parser: the whole point of the tool is that its output is a valid
// input to `metaimport libex`.
func TestLibexSelectOutputRoundTrips(t *testing.T) {
	dataDir := seedSelectCatalogue(t)
	_, lines := runSelect(t, dataDir, selectExportRows(), 0)

	lp, err := parseLibex([]byte(strings.Join(lines, "\n") + "\n"))
	if err != nil {
		t.Fatalf("parseLibex of the subset: %v", err)
	}
	if len(lp.warnings) != 0 {
		t.Errorf("parse warnings = %v, want none", lp.warnings)
	}
	got := make([]string, 0, len(lp.books))
	for _, b := range lp.books {
		got = append(got, b.str("asin"))
	}
	want := []string{"B0SELECT02", "B0SIBLIN5A", "B0SIBLIN5B", "B0SELECT06", "B0SELECT07"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("round-tripped ASINs = %v, want %v", got, want)
	}
}

func TestLibexSelectMaxPerSeries(t *testing.T) {
	dataDir := seedSelectCatalogue(t)
	rows := selectExportRows()
	res, lines := runSelect(t, dataDir, rows, 2)

	// Position order keeps Volume Two (2) and Volume Five (5, two sibling rows);
	// Volumes Six and Seven are cut.
	if res.ProjectedWorks != 2 {
		t.Errorf("ProjectedWorks = %d, want 2", res.ProjectedWorks)
	}
	if res.RowsSelected != 3 {
		t.Errorf("RowsSelected = %d, want 3", res.RowsSelected)
	}
	if res.Excluded[reasonSeriesCap] != 2 {
		t.Errorf("excluded[cap] = %d, want 2", res.Excluded[reasonSeriesCap])
	}
	wantLines := []string{rows[0], rows[5], rows[6]}
	for i, line := range lines {
		if i >= len(wantLines) || line != wantLines[i] {
			t.Errorf("output line %d = %s", i, line)
		}
	}
	if len(lines) != len(wantLines) {
		t.Errorf("output has %d lines, want %d", len(lines), len(wantLines))
	}

	// The cut is reported, never silent.
	if len(res.PerSeries) != 1 {
		t.Fatalf("PerSeries = %+v, want one entry", res.PerSeries)
	}
	if s := res.PerSeries[0]; s.CutWorks != 2 || s.CutRows != 2 {
		t.Errorf("cut = %d works / %d rows, want 2/2", s.CutWorks, s.CutRows)
	}
	if report := res.Report(); !strings.Contains(report, "cut 2 works, 2 rows") {
		t.Errorf("report does not name the cut:\n%s", report)
	}
}

// TestLibexSelectRowIdentityRules covers the two identity exclusions: a row
// with no well-formed ASIN, and a second row repeating an ASIN already
// selected this run (the export can list a title twice).
func TestLibexSelectRowIdentityRules(t *testing.T) {
	dataDir := seedSelectCatalogue(t)
	rows := []string{
		selectRow("B0SELECT02", "Volume Two", "us", "english", seriesName, "2"),
		selectRow("B0SELECT02", "Volume Two", "us", "english", seriesName, "2"),
		selectRow("nope", "Volume Three", "us", "english", seriesName, "3"),
	}
	res, lines := runSelect(t, dataDir, rows, 0)

	if res.RowsSelected != 1 || len(lines) != 1 {
		t.Fatalf("RowsSelected = %d, lines = %v, want 1", res.RowsSelected, lines)
	}
	if res.Excluded[reasonDuplicateASIN] != 1 {
		t.Errorf("excluded[duplicate] = %d, want 1", res.Excluded[reasonDuplicateASIN])
	}
	if res.Excluded[reasonNoASIN] != 1 {
		t.Errorf("excluded[no asin] = %d, want 1", res.Excluded[reasonNoASIN])
	}
}

// TestLibexSelectSeriesNameCollision pins the match rule to the importer's own
// findSeries: a numeric-suffix slug belongs to whichever series NAME claimed
// it, so a differently-named series that merely slugs onto a taken base is not
// a completion target.
func TestLibexSelectSeriesNameCollision(t *testing.T) {
	dataDir := seedSelectCatalogue(t)
	rows := []string{
		// Same slug base as the seeded series, different name: no match.
		selectRow("B0COLLIDE1", "Impostor", "us", "english", "Cartographer  Chronicles!", "1"),
	}
	res, lines := runSelect(t, dataDir, rows, 0)
	if res.RowsSelected != 0 || len(lines) != 0 {
		t.Fatalf("RowsSelected = %d, want 0 (a same-slug, different-name series is not a target)", res.RowsSelected)
	}
	if res.Excluded[reasonNoSeries] != 1 {
		t.Errorf("excluded[no catalogue series] = %d, want 1", res.Excluded[reasonNoSeries])
	}
}

// TestLibexSelectRefusesPositionlessRow pins the completion rule: a row that
// names a catalogue series but states no usable position in it is NOT a
// completion. The importer would create the work and then refuse to place it,
// leaving an orphan outside the very series the row was selected to complete.
func TestLibexSelectRefusesPositionlessRow(t *testing.T) {
	dataDir := seedSelectCatalogue(t)
	rows := []string{
		selectRow("B0NOPOS001", "Volume Two", "us", "english", seriesName, ""),
		selectRow("B0NOPOS002", "Volume Three", "us", "english", seriesName, "the third one"),
		selectRow("B0SELECT04", "Volume Four", "us", "english", seriesName, "4"),
	}
	res, lines := runSelect(t, dataDir, rows, 0)

	if res.RowsSelected != 1 || len(lines) != 1 || !strings.Contains(lines[0], "B0SELECT04") {
		t.Fatalf("RowsSelected = %d, lines = %v, want only the positioned volume", res.RowsSelected, lines)
	}
	if res.Excluded[reasonNoPosition] != 2 {
		t.Errorf("excluded[no position] = %d, want 2", res.Excluded[reasonNoPosition])
	}
	assertPartition(t, res)
	if report := res.Report(); !strings.Contains(report, reasonNoPosition) {
		t.Errorf("report does not name the reason:\n%s", report)
	}
}

// TestLibexSelectRefusesTakenPositions pins the collision rule from both sides:
// a position the CATALOGUE's series already fills, and a position an earlier
// row of this run claimed for a different work (libex lists per-country volumes
// of some series at one position). The importer places only one of them and
// creates the loser as an orphan work, so a taken slot is enrichment fodder,
// not a completion.
func TestLibexSelectRefusesTakenPositions(t *testing.T) {
	dataDir := seedSelectCatalogue(t)
	rows := []string{
		// Position 1 is Volume One's, already in the catalogue series.
		selectRow("B0RETAKE01", "Volume One Redux", "us", "english", seriesName, "1"),
		// First-seen wins position 9; the different-titled row behind it loses.
		selectRow("B0FIRST009", "Volume Nine", "us", "english", seriesName, "9"),
		selectRow("B0SECOND09", "Neunter Band", "de", "german", seriesName, "9"),
		// The sibling row of the SAME title shares the slot legitimately.
		selectRow("B0FIRST09B", "Volume Nine", "gb", "english", seriesName, "9"),
	}
	res, lines := runSelect(t, dataDir, rows, 0)

	if res.Excluded[reasonPositionTaken] != 2 {
		t.Errorf("excluded[position taken] = %d, want 2", res.Excluded[reasonPositionTaken])
	}
	if res.RowsSelected != 2 || res.ProjectedWorks != 1 {
		t.Errorf("selected %d rows / %d works, want the 2 sibling rows of 1 work", res.RowsSelected, res.ProjectedWorks)
	}
	if len(lines) != 2 || !strings.Contains(lines[0], "B0FIRST009") || !strings.Contains(lines[1], "B0FIRST09B") {
		t.Errorf("output = %v, want the first claimant and its sibling", lines)
	}
	assertPartition(t, res)
}

// TestLibexSelectCatalogueNormalizesPositions checks the catalogue's stored
// position is compared in the same canonical spelling a row's claim arrives in,
// so "1.0" on disk and "1" in the export are one slot, not two.
func TestLibexSelectCatalogueNormalizesPositions(t *testing.T) {
	dataDir := seedSelectCatalogue(t)
	res, _ := runSelect(t, dataDir, []string{
		selectRow("B0RETAKE01", "Volume One Redux", "us", "english", seriesName, "1.0"),
	}, 0)
	if res.Excluded[reasonPositionTaken] != 1 {
		t.Errorf("excluded[position taken] = %d, want 1 (\"1.0\" is position \"1\")", res.Excluded[reasonPositionTaken])
	}
}

// assertPartition re-checks the invariant the whole report rests on: every row
// read is either selected or counted under exactly one exclusion reason.
func assertPartition(t *testing.T, res SelectResult) {
	t.Helper()
	var excluded int
	for _, n := range res.Excluded {
		excluded += n
	}
	if res.RowsSelected+excluded != res.RowsRead {
		t.Errorf("selected %d + excluded %d != read %d", res.RowsSelected, excluded, res.RowsRead)
	}
}

// TestLibexSelectAcceptsExportShapes checks the selector reads the same three
// shapes the importer's parser does (array, NDJSON, wrapper object), so an
// operator cannot be told "select it" and then "that file is not selectable".
func TestLibexSelectAcceptsExportShapes(t *testing.T) {
	dataDir := seedSelectCatalogue(t)
	row := selectRow("B0SELECT02", "Volume Two", "us", "english", seriesName, "2")
	for _, tc := range []struct {
		name  string
		input string
	}{
		{"array", "[" + row + "]"},
		{"ndjson", row + "\n"},
		{"wrapper", `{"books":[` + row + `]}`},
		{"bom+array", "\ufeff  [" + row + "]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, lines := runSelect(t, dataDir, []string{tc.input}, 0)
			if res.RowsRead != 1 || res.RowsSelected != 1 {
				t.Fatalf("read/selected = %d/%d, want 1/1", res.RowsRead, res.RowsSelected)
			}
			if len(lines) != 1 || !strings.Contains(lines[0], "B0SELECT02") {
				t.Fatalf("output = %v", lines)
			}
		})
	}
}

// TestLibexSelectRefusesBrokenExports pins the shapes the streamer must NOT
// accept. Each is a file the in-memory parser already refuses, and each used to
// stream through happily because the array loop never consumed the closing
// bracket: a truncated dump imported as though it were whole, and two
// concatenated exports imported only the first.
func TestLibexSelectRefusesBrokenExports(t *testing.T) {
	dataDir := seedSelectCatalogue(t)
	row := selectRow("B0SELECT02", "Volume Two", "us", "english", seriesName, "2")
	other := selectRow("B0SELECT06", "Volume Six", "us", "english", seriesName, "6")
	for _, tc := range []struct {
		name  string
		input string
		want  string
	}{
		{"truncated array", "[" + row + "," + other, "unexpected EOF"},
		{"concatenated arrays", "[" + row + "][" + other + "]", "trailing content"},
		{"array then garbage", "[" + row + "] garbage", "trailing content"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			in := filepath.Join(dir, "export.ndjson")
			if err := os.WriteFile(in, []byte(tc.input), 0o644); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(dir, "subset.ndjson")
			_, err := SelectLibex(in, out, SelectOptions{DataDir: dataDir})
			if err == nil {
				t.Fatalf("SelectLibex accepted a broken export")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
			// An aborted run leaves no subset behind: a partial NDJSON file is
			// still a perfectly importable one.
			if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
				t.Errorf("output file exists after an aborted run (stat err = %v)", statErr)
			}
		})
	}
}

// TestLibexSelectParserParity pins the selector's streamer to the importer's
// in-memory parser over the whole shape vocabulary. Two parsers for one file
// format is one more than the operator can reason about: a file the selector
// accepts must be a file `metaimport libex` accepts, and vice versa. The
// verdicts compared are what each one ACCEPTS as a usable row, plus whether it
// refused the file at all.
func TestLibexSelectParserParity(t *testing.T) {
	row := selectRow("B0SELECT02", "Volume Two", "us", "english", seriesName, "2")
	other := selectRow("B0SELECT06", "Volume Six", "us", "english", seriesName, "6")
	noASIN := `{"title":"Nameless","region":"us","language":"english","series":[]}`

	for _, tc := range []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"whitespace only", "  \n\t "},
		{"bom + array", "\ufeff[" + row + "]"},
		{"bom + ndjson", "\ufeff" + row + "\n" + other + "\n"},
		{"array", "[" + row + "," + other + "]"},
		{"empty array", "[]"},
		{"ndjson", row + "\n" + other + "\n"},
		{"wrapper books", `{"books":[` + row + `]}`},
		{"wrapper items", `{"Items":[` + row + `,` + other + `]}`},
		{"wrapper library empty", `{"library":[]}`},
		{"lone object with asin", row},
		{"lone object without asin", noASIN},
		{"ndjson leading no-asin row", noASIN + "\n" + row + "\n"},
		{"non-object array element", `[7,` + row + `]`},
		{"non-object ndjson value", row + "\n7\n"},
		{"truncated array", "[" + row},
		{"truncated object", `{"asin":`},
		{"concatenated arrays", "[" + row + "][" + other + "]"},
		{"array then garbage", "[" + row + "] garbage"},
		{"not json at all", "hello"},
		{"scalar", "42"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var streamed []string
			streamErr := streamLibexRows(strings.NewReader(tc.input), func(_ []byte, e rawBook) {
				if asin := NormalizeASIN(e.str("asin")); asin != "" {
					streamed = append(streamed, asin)
				}
			})

			lp, parseErr := parseLibex([]byte(tc.input))
			var parsed []string
			for _, b := range lp.books {
				parsed = append(parsed, b.str("asin"))
			}

			if (streamErr != nil) != (parseErr != nil) {
				t.Fatalf("refusal disagrees: stream err = %v, parse err = %v", streamErr, parseErr)
			}
			if streamErr != nil {
				// The wording is shared too, so an operator reads one message
				// whichever tool refused the file.
				if streamErr.Error() != parseErr.Error() {
					t.Errorf("error wording differs:\n stream %v\n parse  %v", streamErr, parseErr)
				}
				return
			}
			if strings.Join(streamed, ",") != strings.Join(parsed, ",") {
				t.Errorf("accepted rows differ: stream %v, parse %v", streamed, parsed)
			}
		})
	}
}

// TestLibexSelectCountsNonObjectElements is the one place the selector is
// deliberately NOT silent where the import parser is: a non-object ARRAY
// element is dropped by decodeEntries without a trace, but the selector's whole
// output is a report, so it counts the element (under the first rule it fails)
// in both array-carrying shapes. Neither tool accepts it as a row.
// (NDJSON has no such case - a non-object top-level value is refused outright
// by both, which the parity table pins.)
func TestLibexSelectCountsNonObjectElements(t *testing.T) {
	dataDir := seedSelectCatalogue(t)
	row := selectRow("B0SELECT02", "Volume Two", "us", "english", seriesName, "2")
	for _, tc := range []struct {
		name  string
		input string
	}{
		{"array", `[7,` + row + `]`},
		{"wrapper", `{"books":[7,` + row + `]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, lines := runSelect(t, dataDir, []string{tc.input}, 0)
			if res.RowsRead != 2 || res.RowsSelected != 1 {
				t.Errorf("read/selected = %d/%d, want 2/1", res.RowsRead, res.RowsSelected)
			}
			if res.Excluded[reasonNoASIN] != 1 {
				t.Errorf("excluded[no asin] = %d, want 1", res.Excluded[reasonNoASIN])
			}
			if len(lines) != 1 || !strings.Contains(lines[0], "B0SELECT02") {
				t.Errorf("output = %v", lines)
			}
			assertPartition(t, res)
		})
	}
}

// TestLibexSelectRefusesSelfOverwrite is the belt-and-braces half of the
// argument-order fix: whatever the flags did, the selector never writes its
// subset over the export it is reading. Losing an operator's multi-GB dump to a
// swapped argument is not a recoverable mistake.
func TestLibexSelectRefusesSelfOverwrite(t *testing.T) {
	dataDir := seedSelectCatalogue(t)
	dir := t.TempDir()
	export := filepath.Join(dir, "full.ndjson")
	body := selectRow("B0SELECT02", "Volume Two", "us", "english", seriesName, "2") + "\n"
	if err := os.WriteFile(export, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{
		export,
		filepath.Join(dir, "sub", "..", "full.ndjson"),
		"./" + filepath.Base(export),
	} {
		if out == "./"+filepath.Base(export) {
			// A relative -o resolves against the working directory, so make it
			// the export's directory for this one case.
			t.Chdir(dir)
		}
		_, err := SelectLibex(export, out, SelectOptions{DataDir: dataDir})
		if err == nil {
			t.Fatalf("-o %s overwrote the input export", out)
		}
		if !strings.Contains(err.Error(), "same file") {
			t.Errorf("error = %v, want it to name the self-overwrite", err)
		}
	}
	if got, err := os.ReadFile(export); err != nil || string(got) != body {
		t.Errorf("input export was modified: %q (err %v)", got, err)
	}
}

// TestLibexSelectPassesRowsThroughVerbatim is the fidelity assertion with teeth:
// the row arrives PRETTY-PRINTED (so the compaction is not a no-op) and carries
// every spelling a re-marshal would quietly normalize - a literal 'é' and '<'
// that Go's encoder would re-escape to é / <, the \u escapes for the
// same two characters that it would fold back to literals, and the trailing
// zero of 1.50 that any decode-then-encode round trip would drop. Only
// insignificant whitespace may be removed: the selector must never become a
// second mapping layer between the dump and the importer.
func TestLibexSelectPassesRowsThroughVerbatim(t *testing.T) {
	dataDir := seedSelectCatalogue(t)
	pretty := `{
  "asin": "B0SELECT02",
  "title": "Café <Volume Two>",
  "subtitle": "Caf\u00e9 \u003cdeux\u003e",
  "region": "us",
  "language": "english",
  "lengthMinutes": 1.50,
  "authors": [
    { "name": "Ada Mapmaker" }
  ],
  "narrators": [ { "name": "Bea Reader" } ],
  "series": [ { "name": "` + seriesName + `", "position": "2" } ]
}`
	want := `{"asin":"B0SELECT02","title":"Café <Volume Two>","subtitle":"Caf\u00e9 \u003cdeux\u003e",` +
		`"region":"us","language":"english","lengthMinutes":1.50,"authors":[{"name":"Ada Mapmaker"}],` +
		`"narrators":[{"name":"Bea Reader"}],"series":[{"name":"` + seriesName + `","position":"2"}]}`

	res, lines := runSelect(t, dataDir, []string{pretty}, 0)
	if res.RowsSelected != 1 || len(lines) != 1 {
		t.Fatalf("RowsSelected = %d, lines = %v, want 1", res.RowsSelected, lines)
	}
	if lines[0] != want {
		t.Errorf("row was not passed through verbatim:\n got %s\nwant %s", lines[0], want)
	}
	// A guard on the guard: if the fixture ever became compact, the compaction
	// step would stop being exercised at all.
	if !strings.Contains(pretty, "\n") || len(want) >= len(pretty) {
		t.Fatal("the fixture must be pretty-printed for this test to mean anything")
	}
}
