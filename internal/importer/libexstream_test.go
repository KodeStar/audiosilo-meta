package importer

import (
	"bytes"
	"fmt"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"unsafe"
)

// parseLibex is parseLibexStream over an export already in memory (the tests'
// door; RunLibex streams the file).
func parseLibex(data []byte) (libexParse, error) {
	return parseLibexStream(bytes.NewReader(data), nil)
}

// libexStreamRow is one well-formed libex row; extra is spliced in verbatim.
func libexStreamRow(asin, title, region, lang, narrator, extra string) string {
	return fmt.Sprintf(`{"asin":%q,"title":%q,"region":%q,"language":%q,"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":%q}],"lengthMinutes":600%s}`,
		asin, title, region, lang, narrator, extra)
}

// TestLibexStreamShapes pins the one export reader (streamLibexRows) over the
// whole shape vocabulary, through the importer's parse layer: the ASINs it
// accepts, the rows it refuses, and the exact wording it refuses a file with.
// libex-select reads through the same function with keepRaw, so each case is
// also streamed that way and must hand over the same rows, each row's bytes
// decoding to the map it came with.
func TestLibexStreamShapes(t *testing.T) {
	one := libexStreamRow("B0STREAM01", "First Book", "us", "english", "Bea Reader", "")
	two := libexStreamRow("B0STREAM02", "Second Book", "uk", "english", "Bea Reader", "")
	noASIN := `{"title":"Nameless","region":"us","language":"english"}`
	notAList := errNotAnEntryList("libex export").Error()
	const (
		truncated  = "parse libex export: unexpected EOF"
		trailing   = "parse libex export: trailing content after the first JSON value (concatenated exports?)"
		wrongShape = "parse libex export: expected a JSON array of rows, NDJSON, or a wrapper object holding an array"
	)
	cases := []struct {
		name    string
		input   string
		asins   []string
		skipped int
		err     string
	}{
		{"array", "[" + one + "," + two + "]", []string{"B0STREAM01", "B0STREAM02"}, 0, ""},
		{"empty array", "[]", nil, 0, ""},
		{"ndjson", one + "\n" + two + "\n", []string{"B0STREAM01", "B0STREAM02"}, 0, ""},
		{"bom + ndjson", "\xEF\xBB\xBF" + one + "\n" + two, []string{"B0STREAM01", "B0STREAM02"}, 0, ""},
		{"bom + array", "\xEF\xBB\xBF[" + one + "]", []string{"B0STREAM01"}, 0, ""},
		{"wrapper", `{"books":[` + one + "," + two + `]}`, []string{"B0STREAM01", "B0STREAM02"}, 0, ""},
		{"wrapper after a null key", `{"Books":null,"books":[` + one + `]}`, []string{"B0STREAM01"}, 0, ""},
		{"empty wrapper", `{"library":[]}`, nil, 0, ""},
		{"lone row", one, []string{"B0STREAM01"}, 0, ""},
		{"ndjson leading no-asin row", noASIN + "\n" + one + "\n", []string{"B0STREAM01"}, 1, ""},
		{"ndjson null line", one + "\nnull\n" + two + "\n", []string{"B0STREAM01", "B0STREAM02"}, 1, ""},
		{"non-object array elements", "[7,null," + one + `,"x"]`, []string{"B0STREAM01"}, 3, ""},
		{"non-object wrapper elements", `{"books":[null,` + one + `,7]}`, []string{"B0STREAM01"}, 2, ""},
		{"lone object without asin", noASIN, nil, 0, notAList},
		{"wrapper with a null array", `{"books":null}`, nil, 0, notAList},
		{"wrapper under an unknown key", `{"data":[` + one + `]}`, nil, 0, notAList},
		{"empty", "", nil, 0, "parse libex export: empty input"},
		{"whitespace only", "  \n\t ", nil, 0, "parse libex export: empty input"},
		{"truncated array", "[" + one + "," + two, nil, 0, truncated},
		{"truncated object", `{"asin":`, nil, 0, truncated},
		{"concatenated arrays", "[" + one + "][" + two + "]", nil, 0, trailing},
		{"array then garbage", "[" + one + "] garbage", nil, 0, trailing},
		{"array then a stray brace", "[" + one + "]}" + two, nil, 0, trailing},
		{"array then a stray bracket", "[" + one + "]]", nil, 0, trailing},
		{"wrapper then a stray bracket", `{"books":[` + one + `]}]` + "\n" + two, nil, 0, trailing},
		{"wrapper then a stray brace", `{"books":[` + one + `]}}`, nil, 0, trailing},
		{"non-object ndjson value", one + "\n7\n", nil, 0, "parse libex export: json: cannot unmarshal number into Go value of type map[string]interface {}"},
		{"scalar", "42", nil, 0, wrongShape},
		{"not json at all", "hello", nil, 0, wrongShape},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lp, err := parseLibex([]byte(tc.input))
			if gotErr := errString(err); gotErr != tc.err {
				t.Fatalf("error = %q, want %q", gotErr, tc.err)
			}
			var asins []string
			for _, b := range lp.books {
				asins = append(asins, b.str("asin"))
			}
			if !slices.Equal(asins, tc.asins) || lp.skipped != tc.skipped {
				t.Errorf("accepted %v, skipped %d; want %v, %d", asins, lp.skipped, tc.asins, tc.skipped)
			}

			var plain, kept []rawBook
			plainErr := streamLibexRows(strings.NewReader(tc.input), false, func(raw []byte, e rawBook) {
				if raw != nil {
					t.Error("keepRaw=false handed over row bytes")
				}
				plain = append(plain, e)
			})
			keptErr := streamLibexRows(strings.NewReader(tc.input), true, func(raw []byte, e rawBook) {
				var back any
				err := decodeNumbers(raw, &back)
				m, _ := back.(map[string]any)
				if err != nil || !reflect.DeepEqual(rawBook(m), e) {
					t.Errorf("row bytes %s do not decode to the row handed over (%v)", raw, err)
				}
				kept = append(kept, e)
			})
			if errString(plainErr) != tc.err || errString(keptErr) != tc.err {
				t.Errorf("stream errors = %q / %q, want %q", errString(plainErr), errString(keptErr), tc.err)
			}
			if !reflect.DeepEqual(plain, kept) {
				t.Errorf("keepRaw changed the rows:\n plain %v\n kept  %v", plain, kept)
			}
		})
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// TestLibexRelocationOrderIsCanonical pins the order-sensitive path: the same
// rows in two file orders parse to the SAME relocation result (books, refusals,
// warnings and evidence), and that result is not simply the file order.
func TestLibexRelocationOrderIsCanonical(t *testing.T) {
	rows := []string{
		libexStreamRow("B0STREAM03", "Third Book", "us", "english", "Bea Reader", `,"imageUrl":"http://example.com/c.jpg"`),
		libexStreamRow("B0STREAM01", "First Book", "uk", "english", "Bea Reader", `,"isbn":"9781234567890"`),
		`{"title":"Nameless","region":"us","language":"english"}`,
		libexStreamRow("B0STREAM02", "Second Book", "zz", "german", "Bea Reader", ""),
		libexStreamRow("B0STREAM04", "Fourth Book", "us", "english", "Virtual Voice", ""),
		libexStreamRow("B0STREAM01", "First Book", "us", "german", "Bea Reader", ""),
		libexStreamRow("B0STREAM05", "Fifth Book", "de", "german", "Carl Sprecher", `,"series":[{"name":"Saga","position":"2"}]`),
	}
	reversed := slices.Clone(rows)
	slices.Reverse(reversed)
	join := func(rs []string) string { return strings.Join(rs, "\n") + "\n" }

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
	if len(aEv) != len(rows) || a.skipped != 3 || len(a.warnings) != 4 {
		t.Errorf("evidence %d rows, skipped %d, %d warnings; want %d, 3, 4", len(aEv), a.skipped, len(a.warnings), len(rows))
	}
	plain, err := parseLibexStream(strings.NewReader(join(rows)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(plain.books, a.books) {
		t.Error("the relocation order equals the file order; the fixture no longer exercises the sort")
	}
}

// TestLibexGenreClaimsAreInterned pins that the parse holds ONE copy of a genre
// spelling repeated across rows, and that a GC between two rows does not undo it
// (what interning through package unique did).
func TestLibexGenreClaimsAreInterned(t *testing.T) {
	genres := `,"genres":[{"asin":"18580606011","name":"Science Fiction &amp; Fantasy"}]`
	lp, err := parseLibex([]byte(libexStreamRow("B0STREAM01", "First Book", "us", "english", "Bea Reader", genres) + "\n" +
		libexStreamRow("B0STREAM02", "Second Book", "us", "english", "Bea Reader", genres) + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(lp.books) != 2 || len(lp.books[0].genres) != 1 || len(lp.books[1].genres) != 1 {
		t.Fatalf("books = %+v", lp.books)
	}
	a, b := lp.books[0].genres[0], lp.books[1].genres[0]
	if a.name != "Science Fiction & Fantasy" || unsafe.StringData(a.name) != unsafe.StringData(b.name) ||
		unsafe.StringData(a.node) != unsafe.StringData(b.node) {
		t.Errorf("claims %+v and %+v do not share one copy of each string", a, b)
	}

	first := lp.intern(strings.Clone("Mystery"))
	runtime.GC()
	if again := lp.intern(strings.Clone("Mystery")); unsafe.StringData(first) != unsafe.StringData(again) {
		t.Error("a GC between two rows made the parse copy a spelling again")
	}
}
