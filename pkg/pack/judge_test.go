package pack

import (
	"reflect"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/canonical"
)

// metafmt --check judges canonical form from the survey's own parse and render
// (Store.PendingCanonical) instead of a second pass of pkg/canonical over every
// pack, so the judged verdict must be canonical.IsCanonical's answer for every
// document Parse accepts, and every document Parse refuses must go unjudged
// (the caller asks pkg/canonical about it, which keeps its old verdict and
// error). The cases are the spellings a canonical render normalizes.
func TestJudgedVerdictMatchesCanonical(t *testing.T) {
	canonicalPack := samplePack
	judgedCases := map[string]string{
		"canonical":                  canonicalPack,
		"compact":                    `{"entries":{"a":{"x":1}}}`,
		"keys out of order":          "{\n  \"entries\": {\n    \"b\": {\n      \"id\": \"b\"\n    },\n    \"a\": {\n      \"id\": \"a\"\n    }\n  }\n}\n",
		"no trailing newline":        canonicalPack[:len(canonicalPack)-1],
		"two trailing newlines":      canonicalPack + "\n",
		"crlf":                       "{\r\n  \"entries\": {}\r\n}\r\n",
		"empty entries, canonical":   "{\n  \"entries\": {}\n}\n",
		"empty entries, compact":     `{"entries":{}}`,
		"html escaped":               "{\n  \"entries\": {\n    \"a\": {\n      \"s\": \"\\u003cb\\u003e\"\n    }\n  }\n}\n",
		"html raw":                   "{\n  \"entries\": {\n    \"a\": {\n      \"s\": \"<b>&\"\n    }\n  }\n}\n",
		"unicode escaped":            "{\n  \"entries\": {\n    \"a\": {\n      \"s\": \"caf\\u00e9\"\n    }\n  }\n}\n",
		"unicode raw":                "{\n  \"entries\": {\n    \"a\": {\n      \"s\": \"café\"\n    }\n  }\n}\n",
		"escaped key":                "{\n  \"entries\": {\n    \"\\u0061\": {\n      \"id\": \"a\"\n    }\n  }\n}\n",
		"number spellings":           "{\n  \"entries\": {\n    \"a\": {\n      \"n\": [1.0, 1e3, -0, 12345678901234567890]\n    }\n  }\n}\n",
		"number spellings canonical": "{\n  \"entries\": {\n    \"a\": {\n      \"n\": [\n        1.0,\n        1e3,\n        -0,\n        12345678901234567890\n      ]\n    }\n  }\n}\n",
		"tab indent":                 "{\n\t\"entries\": {}\n}\n",
		"leading space":              " " + canonicalPack,
	}
	// Parse refuses each of these, so the survey cannot judge it.
	unjudgedCases := map[string]string{
		"not json":          `{`,
		"not a pack":        `{"other":{}}`,
		"null entries":      `{"entries":null}`,
		"duplicate key":     `{"entries":{"a":{"x":1},"a":{"x":2}}}`,
		"trailing document": `{"entries":{}} {}`,
	}

	dir := t.TempDir()
	files := map[string]string{}
	var rels []string
	want := map[string]bool{}
	i := 0
	add := func(body string, judged bool) {
		rel := "people/" + string(rune('a'+i/26)) + string(rune('a'+i%26)) + ".json"
		i++
		files[rel] = body
		rels = append(rels, rel)
		if judged {
			ok, err := canonical.IsCanonical([]byte(body))
			if err != nil {
				t.Fatalf("pkg/canonical cannot read a document Parse accepts: %v\n%s", err, body)
			}
			want[rel] = ok
		}
	}
	for _, body := range judgedCases {
		add(body, true)
	}
	for _, body := range unjudgedCases {
		add(body, false)
	}
	writeFiles(t, dir, files)

	r := NewReader(dir)
	got := map[string]bool{}
	r.prefetchJudged(rels, got)
	if !reflect.DeepEqual(got, want) {
		for rel, w := range want {
			if g, ok := got[rel]; !ok || g != w {
				t.Errorf("%s: judged %v (present %v), pkg/canonical says %v:\n%s", rel, g, ok, w, files[rel])
			}
		}
		for rel := range got {
			if _, ok := want[rel]; !ok {
				t.Errorf("%s was judged, but Parse refuses it:\n%s", rel, files[rel])
			}
		}
	}

	// A judged pack keeps its entries and its size, not its render.
	for rel := range want {
		f, ok := r.Cached(rel)
		if !ok {
			t.Fatalf("%s was judged but not cached", rel)
		}
		if f.rendered != nil || f.sizes != nil {
			t.Errorf("%s still holds its render after being judged", rel)
		}
		size, err := f.Size()
		if err != nil {
			t.Fatal(err)
		}
		if f.rendered != nil {
			t.Errorf("%s: Size rendered again although the size was kept", rel)
		}
		data, err := f.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		if size != len(data) {
			t.Errorf("%s: kept size %d, render is %d bytes", rel, size, len(data))
		}
	}
}

// A mutation after a release must not leave the kept size answering for
// entries it no longer describes.
func TestKeptSizeIsDroppedByAMutation(t *testing.T) {
	f, err := Parse([]byte(`{"entries":{"a":{"x":1}}}`))
	if err != nil {
		t.Fatal(err)
	}
	before, err := f.Size()
	if err != nil {
		t.Fatal(err)
	}
	f.ReleaseMemo()
	f.Set("b", []byte(`{"x":2}`))
	after, err := f.Size()
	if err != nil {
		t.Fatal(err)
	}
	if after <= before {
		t.Errorf("Size after a Set = %d, want more than the released %d", after, before)
	}
	var zero File
	if n, err := zero.Size(); err == nil && n == 0 {
		// The zero File has no entries map; it must render rather than answer
		// with a size it never computed. Rendering an empty pack is not zero bytes.
		t.Errorf("the zero File reported a size of 0 it never rendered")
	}
}

// PendingCanonical is Pending plus the verdicts: the structural report must be
// the one Pending gives over the same tree.
func TestPendingCanonicalReportsWhatPendingDoes(t *testing.T) {
	withCaps(t, FamilyWorks, Caps{TargetSize: 1 << 20, HardSize: 1 << 21, Entries: 2, DirPacks: 512})
	dir := surveyFixture(t)
	want := pendingOf(t, dir, FamilyWorks)

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, canon, err := s.PendingCanonical(FamilyWorks)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.LinesUnder(dir), want.LinesUnder(dir)) {
		t.Errorf("PendingCanonical reports\n%v\nPending reports\n%v", got.LinesUnder(dir), want.LinesUnder(dir))
	}
	if _, ok := canon["works/0/dd.json"]; ok {
		t.Error("an unreadable file was judged")
	}
	// The fixture's packs are compact, so every parsed one is not canonical -
	// the salvaged file nested too deep among them.
	for _, rel := range []string{"works/0/0.json", "works/0/bb.json", "works/0/extra/far.json"} {
		if ok, judged := canon[rel]; !judged || ok {
			t.Errorf("%s: verdict %v (judged %v), want judged and not canonical", rel, ok, judged)
		}
	}
}
