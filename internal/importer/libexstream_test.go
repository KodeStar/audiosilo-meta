package importer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// bufferedLibexEntries is the parse layer's former whole-file decoder, kept as
// the REFERENCE the streamed parser is held to: the file in memory, decoded to
// row maps in one go (decodeEntries for an array or a wrapper, a stream of
// objects otherwise, a lone object without an "asin" unwrapped).
func bufferedLibexEntries(data []byte) ([]rawBook, error) {
	trimmed := bytes.TrimLeft(bytes.TrimPrefix(data, utf8BOM), " \t\r\n")
	switch {
	case len(trimmed) == 0:
		return nil, errors.New("parse libex export: empty input")
	case trimmed[0] == '[':
		return decodeEntries(trimmed, "libex export")
	case trimmed[0] != '{':
		return nil, errors.New("parse libex export: expected a JSON array of rows, NDJSON, or a wrapper object holding an array")
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var objs []rawBook
	for {
		var obj map[string]any
		if err := dec.Decode(&obj); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("parse libex export: %w", err)
		}
		objs = append(objs, rawBook(obj))
	}
	if len(objs) == 1 {
		if _, isRow := objs[0]["asin"]; !isRow {
			return decodeEntries(trimmed, "libex export")
		}
	}
	return objs, nil
}

// bufferedLibexParse is the former in-memory parse: every row decoded first,
// then (for relocation) sorted by its re-marshalled JSON, then parsed in that
// order, with the relocation evidence read from the same sorted rows.
func bufferedLibexParse(data []byte, relocate bool) (libexParse, []relocationEvidence, error) {
	entries, err := bufferedLibexEntries(data)
	if err != nil {
		return libexParse{}, nil, err
	}
	if relocate {
		keys := make(map[int]string, len(entries))
		idx := make([]int, len(entries))
		for i, e := range entries {
			k, _ := json.Marshal(e)
			keys[i], idx[i] = string(k), i
		}
		sort.SliceStable(idx, func(a, b int) bool { return keys[idx[a]] < keys[idx[b]] })
		sorted := make([]rawBook, len(entries))
		for i, j := range idx {
			sorted[i] = entries[j]
		}
		entries = sorted
	}
	var lp libexParse
	var evidence []relocationEvidence
	for _, e := range entries {
		if relocate {
			lang, _ := mapLanguage(e.str("language"))
			evidence = append(evidence, relocationEvidence{asin: NormalizeASIN(e.str("asin")), lang: lang})
		}
		lp.parseRow(e)
	}
	return lp, evidence, nil
}

// libexStreamFixtureRows is a row set exercising every parse-layer outcome:
// accepted rows (with an http cover and a malformed ISBN warning), a no-ASIN
// row, an unknown marketplace, an AI and a platform-account credit, and the
// duplicate and regional copies whose ORDER relocation canonicalizes.
func libexStreamFixtureRows() []string {
	row := func(asin, title, region, lang, narrator, extra string) string {
		return fmt.Sprintf(`{"asin":%q,"title":%q,"region":%q,"language":%q,"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":%q}],"lengthMinutes":600%s}`,
			asin, title, region, lang, narrator, extra)
	}
	return []string{
		row("B0STREAM03", "Third Book", "us", "english", "Bea Reader", `,"imageUrl":"http://example.com/c.jpg"`),
		row("B0STREAM01", "First Book", "uk", "english", "Bea Reader", `,"isbn":"9781234567890"`),
		`{"title":"Nameless","region":"us","language":"english"}`,
		row("B0STREAM02", "Second Book", "zz", "german", "Bea Reader", ""),
		row("B0STREAM04", "Fourth Book", "us", "english", "Virtual Voice", ""),
		row("B0STREAM01", "First Book", "us", "german", "Bea Reader", ""),
		row("B0STREAM05", "Fifth Book", "de", "german", "Carl Sprecher", `,"series":[{"name":"Saga","position":"2"}]`),
		row("B0STREAM01", "First Book", "uk", "english", "Bea Reader", `,"isbn":"9781234567890"`),
		row("B0STREAM06", "Sixth Book", "us", "english", "Bea Reader", `,"genres":[{"asin":"1","name":"Mystery","type":"Genres"}]`),
	}
}

// TestLibexStreamMatchesBuffered pins the streamed parse layer to the former
// whole-file one over every file shape and both orders it produces (the file's
// own, and relocation's canonical sort): the same books, refusals, worklist
// skips and warnings in the same order, the same relocation evidence, and the
// same refusal of a broken file with the same wording.
func TestLibexStreamMatchesBuffered(t *testing.T) {
	rows := libexStreamFixtureRows()
	ndjson := strings.Join(rows, "\n") + "\n"
	array := "[" + strings.Join(rows, ",") + "]"
	cases := []struct{ name, input string }{
		{"ndjson", ndjson},
		{"array", array},
		{"bom + ndjson", "\xEF\xBB\xBF" + ndjson},
		{"wrapper", `{"books":` + array + `}`},
		{"wrapper after a null key", `{"Books":null,"books":` + array + `}`},
		{"array with non-object elements", "[7,null," + strings.Join(rows, ",") + `,"x",[]]`},
		{"wrapper with non-object elements", `{"books":[null,` + rows[0] + `,7]}`},
		{"ndjson null line", rows[0] + "\nnull\n" + rows[1] + "\n"},
		{"lone row", rows[0]},
		{"lone envelope with a null array", `{"books":null}`},
		{"empty", " \n"},
		{"truncated array", "[" + rows[0] + "," + rows[1]},
		{"concatenated arrays", "[" + rows[0] + "][" + rows[1] + "]"},
		{"non-object ndjson value", rows[0] + "\n7\n"},
		{"scalar", "42"},
	}
	for _, tc := range cases {
		for _, relocate := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/relocate=%v", tc.name, relocate), func(t *testing.T) {
				want, wantEvidence, wantErr := bufferedLibexParse([]byte(tc.input), relocate)
				var got libexParse
				var gotEvidence []relocationEvidence
				var gotErr error
				if relocate {
					got, gotEvidence, gotErr = parseLibexRelocation(strings.NewReader(tc.input))
				} else {
					got, gotErr = parseLibexStream(strings.NewReader(tc.input))
				}
				if (gotErr != nil) != (wantErr != nil) {
					t.Fatalf("refusal disagrees: streamed err = %v, buffered err = %v", gotErr, wantErr)
				}
				if gotErr != nil {
					if gotErr.Error() != wantErr.Error() {
						t.Errorf("error wording differs:\n streamed %v\n buffered %v", gotErr, wantErr)
					}
					return
				}
				if len(want.books) == 0 && len(want.warnings) == 0 {
					t.Fatalf("fixture %q parsed to nothing; the comparison would be vacuous", tc.name)
				}
				if len(got.books)+len(want.books) > 0 && !reflect.DeepEqual(got.books, want.books) {
					t.Errorf("books differ:\n streamed %+v\n buffered %+v", got.books, want.books)
				}
				if got.skipped != want.skipped {
					t.Errorf("skipped = %d, buffered %d", got.skipped, want.skipped)
				}
				if !reflect.DeepEqual(got.warnings, want.warnings) {
					t.Errorf("warnings differ:\n streamed %+v\n buffered %+v", got.warnings, want.warnings)
				}
				if !reflect.DeepEqual(got.skips, want.skips) {
					t.Errorf("skips differ:\n streamed %+v\n buffered %+v", got.skips, want.skips)
				}
				if len(gotEvidence)+len(wantEvidence) > 0 && !reflect.DeepEqual(gotEvidence, wantEvidence) {
					t.Errorf("relocation evidence differs:\n streamed %+v\n buffered %+v", gotEvidence, wantEvidence)
				}
			})
		}
	}
}

// TestLibexRelocationOrderIsCanonical pins the order-sensitive half on its own:
// the same rows in two different file orders parse to the SAME relocation
// result, and that result is not simply the file order (the fixture's first
// row sorts later than others).
func TestLibexRelocationOrderIsCanonical(t *testing.T) {
	rows := libexStreamFixtureRows()
	reversed := append([]string(nil), rows...)
	join := func(rs []string) string { return strings.Join(rs, "\n") + "\n" }
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	a, aEv, err := parseLibexRelocation(strings.NewReader(join(rows)))
	if err != nil {
		t.Fatal(err)
	}
	b, bEv, err := parseLibexRelocation(strings.NewReader(join(reversed)))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) || !reflect.DeepEqual(aEv, bEv) {
		t.Errorf("relocation parse depends on file order:\n %+v\n %+v", a, b)
	}
	plain, err := parseLibexStream(strings.NewReader(join(rows)))
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(plain.books, a.books) {
		t.Error("the relocation order equals the file order; the fixture no longer exercises the sort")
	}
}
