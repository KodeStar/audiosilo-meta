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

// TestISBNRefRejectsANonISBNShape covers the decode error path. The schema has
// already rejected anything that reaches here, so this is a belt-and-braces
// case: a malformed value must be an error, never a silently empty ISBN.
func TestISBNRefRejectsANonISBNShape(t *testing.T) {
	var got ISBNRef
	if err := json.Unmarshal([]byte(`12345`), &got); err == nil {
		t.Fatalf("a bare number decoded as an ISBN: %+v", got)
	}
}
