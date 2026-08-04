package model

import (
	"encoding/json"
	"testing"
)

// TestISBNRefRoundTripsBothSpellings pins the whole point of the custom
// (un)marshalling: the bare string and the region-scoped object are two
// spellings of one type, and a value that came off disk as one goes back as the
// same one. The string case is what makes the change need no data migration -
// every ISBN already in the tree is a bare string and re-renders byte for byte.
func TestISBNRefRoundTripsBothSpellings(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		want   ISBNRef
		render string
	}{
		{
			name:   "bare string has no region",
			in:     `"9781473647633"`,
			want:   ISBNRef{ISBN: "9781473647633"},
			render: `"9781473647633"`,
		},
		{
			name:   "object states its region",
			in:     `{"isbn":"9781473647633","region":"uk"}`,
			want:   ISBNRef{Region: "uk", ISBN: "9781473647633"},
			render: `{"region":"uk","isbn":"9781473647633"}`,
		},
		{
			name:   "ten-digit ISBN with an X check digit",
			in:     `"012345678X"`,
			want:   ISBNRef{ISBN: "012345678X"},
			render: `"012345678X"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got ISBNRef
			if err := json.Unmarshal([]byte(tc.in), &got); err != nil {
				t.Fatalf("unmarshal %s: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("unmarshal %s = %+v, want %+v", tc.in, got, tc.want)
			}
			out, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("marshal %+v: %v", got, err)
			}
			if string(out) != tc.render {
				t.Errorf("marshal %+v = %s, want %s", got, out, tc.render)
			}
		})
	}
}

// TestRecordingISBNListMixesSpellings covers a recording carrying both forms at
// once, which is the real shape: a production whose US identifier nobody scoped
// and whose UK identifier a submitter did.
func TestRecordingISBNListMixesSpellings(t *testing.T) {
	const in = `{"id":"rec","work":"w","narrators":["n"],"language":"en",` +
		`"isbn":["9780062898968",{"isbn":"9781473647633","region":"uk"}],` +
		`"publishers":[{"region":"uk","publisher":"Hodder & Stoughton"}],` +
		`"publisher":"Harper Voyager","license":"CC0-1.0","sources":[{"type":"user"}]}`
	var rec Recording
	if err := json.Unmarshal([]byte(in), &rec); err != nil {
		t.Fatalf("unmarshal recording: %v", err)
	}
	want := []ISBNRef{{ISBN: "9780062898968"}, {Region: "uk", ISBN: "9781473647633"}}
	if len(rec.ISBN) != len(want) {
		t.Fatalf("isbn = %+v, want %+v", rec.ISBN, want)
	}
	for i := range want {
		if rec.ISBN[i] != want[i] {
			t.Errorf("isbn[%d] = %+v, want %+v", i, rec.ISBN[i], want[i])
		}
	}
	if len(rec.Publishers) != 1 || rec.Publishers[0].Region != "uk" ||
		rec.Publishers[0].Publisher != "Hodder & Stoughton" {
		t.Errorf("publishers = %+v", rec.Publishers)
	}
	if rec.Publisher != "Harper Voyager" {
		t.Errorf("publisher of record = %q", rec.Publisher)
	}
}

// TestISBNRefOfAgreesWithUnmarshal binds the package's TWO readers of the
// on-disk spellings against each other: UnmarshalJSON, which decodes raw JSON
// BYTES, and ISBNRefOf, which reads an element of an already-decoded raw map.
// Both exist because both inputs occur (a typed decode of a record; a generic
// decode a raw-map writer edits in place), and a spelling one understands and
// the other does not would read as ABSENT on one path - which is how a person
// record once forked in two.
func TestISBNRefOfAgreesWithUnmarshal(t *testing.T) {
	cases := []struct {
		name       string
		raw        string
		wantISBN   string
		wantRegion string
	}{
		{"bare string", `"9781234567897"`, "9781234567897", ""},
		{"region-scoped object", `{"isbn":"9781234567897","region":"uk"}`, "9781234567897", "uk"},
		{"ten-digit with an X check digit", `"012345678X"`, "012345678X", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var typed ISBNRef
			if err := json.Unmarshal([]byte(tc.raw), &typed); err != nil {
				t.Fatalf("UnmarshalJSON of %s: %v", tc.raw, err)
			}
			if typed.ISBN != tc.wantISBN || typed.Region != tc.wantRegion {
				t.Errorf("UnmarshalJSON of %s = %+v, want isbn %q region %q", tc.raw, typed, tc.wantISBN, tc.wantRegion)
			}

			var generic any
			if err := json.Unmarshal([]byte(tc.raw), &generic); err != nil {
				t.Fatalf("generic decode of %s: %v", tc.raw, err)
			}
			got, ok := ISBNRefOf(generic)
			if !ok {
				t.Fatalf("ISBNRefOf(%s) reported the entry unusable; UnmarshalJSON read %+v", tc.raw, typed)
			}
			if got != typed {
				t.Errorf("ISBNRefOf(%s) = %+v, UnmarshalJSON = %+v: the two readers have drifted", tc.raw, got, typed)
			}
		})
	}
}

// TestISBNRefOfRejectsWhatUnmarshalRejects keeps the two readers in step on the
// REFUSALS too: an element stating no ISBN is unusable either way.
func TestISBNRefOfRejectsWhatUnmarshalRejects(t *testing.T) {
	for _, v := range []any{nil, "", map[string]any{}, map[string]any{"region": "uk"}, 12345, []any{"9781234567897"}} {
		if got, ok := ISBNRefOf(v); ok {
			t.Errorf("ISBNRefOf(%#v) = %+v, want it refused", v, got)
		}
	}
}

// TestISBNRefRejectsAnEmptyOrMalformedEntry covers the decode error paths. The
// schema rejects all of these before pkg/check ever decodes them, but pkg/model
// is a public dependency and a consumer that decodes without validating must
// not be handed an identifier that silently is not there.
//
// The three quiet ones are the point: `null` is a documented no-op for
// json.Unmarshal (it leaves the target untouched and returns nil), and `{}` and
// `""` decode just as cleanly, so without the emptiness check every one of them
// would yield a zero ISBNRef and a nil error.
func TestISBNRefRejectsAnEmptyOrMalformedEntry(t *testing.T) {
	for _, in := range []string{`12345`, `null`, `{}`, `""`, `{"region":"uk"}`} {
		t.Run(in, func(t *testing.T) {
			var got ISBNRef
			if err := json.Unmarshal([]byte(in), &got); err == nil {
				t.Fatalf("%s decoded as an ISBN: %+v", in, got)
			}
		})
	}
}

// TestRecordingRejectsAnEmptyISBNEntry proves the error propagates out of the
// enclosing record rather than being swallowed one level up.
func TestRecordingRejectsAnEmptyISBNEntry(t *testing.T) {
	const in = `{"id":"rec","work":"w","narrators":["n"],"language":"en",` +
		`"isbn":["9780062898968",null],"license":"CC0-1.0","sources":[{"type":"user"}]}`
	var rec Recording
	if err := json.Unmarshal([]byte(in), &rec); err == nil {
		t.Fatalf("a null isbn entry decoded silently: %+v", rec.ISBN)
	}
}
