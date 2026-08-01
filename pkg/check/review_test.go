package check

import (
	"strings"
	"testing"
)

// The regressions below were each found by an adversarial review of the
// dual-layout loader. Every one of them is a way for data to LEAVE the catalog
// - and so the release artifact - with the gate still reporting green.

// A JSON number the schema calls an integer need not be one Go can decode:
// "runtime_min": 1e3 is a valid JSON-Schema integer (1000 with a zero
// fractional part) and json.Unmarshal into an int rejects it. Whichever layout
// the record is in, and whatever kind it is, the load must say so rather than
// drop it.
func TestUndecodableRecordIsReported(t *testing.T) {
	const want = "passes its schema but cannot be decoded into the model"
	cases := map[string]struct {
		files map[string]string
		path  string
	}{
		"pack recording": {
			files: func() map[string]string {
				f := packValid()
				f["works/0/0.json"] = packOf(map[string]string{
					"book-one": composite(pkWorkOne, map[string]string{
						"rec-one": strings.Replace(pkRecOne, `"language":"en"`, `"language":"en","runtime_min":1e3`, 1),
					}),
				})
				return f
			}(),
			path: "works/0/0.json: entry book-one: recording rec-one",
		},
		// A spoiler position is the other integer in the model, and it reaches
		// the catalog through a works-community entry's member rather than a
		// nested recording, so it exercises the other report path.
		"pack community sidecar": {
			files: func() map[string]string {
				f := packValid()
				f["works-community/0/0.json"] = packOf(map[string]string{
					"book-one": `{"characters":` +
						strings.Replace(validCharacters("book-one"), `"reveal":{"chapter":1}`, `"reveal":{"chapter":1e1}`, 1) + `}`,
				})
				return f
			}(),
			path: "works-community/0/0.json: entry book-one: characters",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeTree(t, dir, c.files)
			res := Load(dir)
			if res.OK() {
				t.Fatalf("an undecodable record was accepted silently; catalog: %d works", len(res.Catalog.Works))
			}
			found := false
			for _, p := range res.Problems {
				if p.Path == c.path && strings.Contains(p.Msg, want) {
					found = true
				}
			}
			if !found {
				t.Errorf("no %q problem at %q; problems:\n%s", want, c.path, joinProblems(res.Problems))
			}
		})
	}
}

// A record the schema ALREADY rejected explains itself, so the decode failure
// that follows adds nothing and is not reported twice.
func TestUndecodableIsQuietWhenTheSchemaAlreadyFailed(t *testing.T) {
	dir := t.TempDir()
	files := packValid()
	files["works/0/0.json"] = packOf(map[string]string{
		"book-one": composite(pkWorkOne, map[string]string{
			"rec-one": strings.Replace(pkRecOne, `"language":"en"`, `"language":"en","runtime_min":"nope"`, 1),
		}),
	})
	writeTree(t, dir, files)
	res := Load(dir)
	if hasProblem(res.Problems, "cannot be decoded into the model") {
		t.Errorf("the decode failure was reported on top of the schema violation:\n%s", joinProblems(res.Problems))
	}
	if !hasProblem(res.Problems, "/runtime_min: got string, want integer") {
		t.Errorf("expected the schema's own message; problems:\n%s", joinProblems(res.Problems))
	}
}

// Nothing in the layout stops a work's sidecars from being read twice: an entry
// that drifted into the wrong pack still parses, so one work slug can hold a
// characters/recaps sidecar in two packs at once. Both copies load, and
// metabuild then dies on a raw UNIQUE constraint naming no file, so the
// duplicate has to be caught here.
func TestSidecarUniqueness(t *testing.T) {
	dir := t.TempDir()
	files := packValid()
	// "aa" sorts before "book-one", so this second pack legitimately covers the
	// slug and the copy in 0.json is the misplaced one: two entries, one work.
	files["works-community/0/aa.json"] = packOf(map[string]string{
		"book-one": `{"characters":` + validCharacters("book-one") + `,"recaps":` + validRecaps("book-one") + `}`,
	})
	writeTree(t, dir, files)

	res := Load(dir)
	for _, want := range []string{
		`work "book-one" already has a characters sidecar in works-community/0/0.json: entry book-one: characters`,
		`work "book-one" already has a recaps sidecar in works-community/0/0.json: entry book-one: recaps`,
	} {
		if !hasProblem(res.Problems, want) {
			t.Errorf("no problem contained %q; problems:\n%s", want, joinProblems(res.Problems))
		}
	}
	// The valid fixtures must stay valid: one sidecar per work is the norm.
	if clean := Load(mustWrite(t, packValid())); !clean.OK() {
		t.Errorf("the single-sidecar fixture reported problems:\n%s", joinProblems(clean.Problems))
	}
}

func mustWrite(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	writeTree(t, dir, files)
	return dir
}

// pkg/pack refuses a pack with a repeated JSON key, because encoding/json would
// keep the last one and the next rewrite would make the loss permanent. That
// refusal has to surface as a located problem rather than a crash or a blank.
func TestPackDuplicateKeyReported(t *testing.T) {
	cases := map[string]string{
		"duplicated entry key": `{"entries":{"book-one":` + pkWork("book-one") +
			`,"book-one":` + pkWork("book-one") + `}}`,
		"duplicated key inside a recordings map": `{"entries":{"book-one":` +
			strings.TrimSuffix(pkWorkOne, "}") + `,"recordings":{"rec-one":` + pkRecOne +
			`,"rec-one":` + pkRecOne + `}}}}`,
		"duplicated field inside an entry": `{"entries":{"book-one":` +
			strings.Replace(pkWorkOne, `"title":"Book One"`, `"title":"Book One","title":"Other"`, 1) + `}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			files := packValid()
			files["works/0/0.json"] = body
			writeTree(t, dir, files)

			res := Load(dir)
			if res.OK() {
				t.Fatal("a pack with a duplicate key was accepted")
			}
			found := false
			for _, p := range res.Problems {
				if p.Path == "works/0/0.json" && strings.Contains(p.Msg, "invalid pack: duplicate key") {
					found = true
				}
			}
			if !found {
				t.Errorf("no located duplicate-key problem; problems:\n%s", joinProblems(res.Problems))
			}
		})
	}
}

// A schema violation inside a composite is reported against the RECORDING it
// belongs to, in the schema's own words. The text is pinned here because it is
// what a contributor acts on: the entry path locates the record, and the reason
// has to reach them through the schema unedited.
func TestSchemaMessagesAtRecordingPrecision(t *testing.T) {
	cases := map[string]struct {
		rec  string // replaces rec-one inside the composite
		want string
	}{
		"missing a required property": {
			rec:  strings.Replace(pkRecOne, `"narrators":["narrator-one"],`, "", 1),
			want: "(root): missing property 'narrators'",
		},
		"wrong type": {
			rec:  strings.Replace(pkRecOne, `"language":"en"`, `"language":"en","runtime_min":"nope"`, 1),
			want: "/runtime_min: got string, want integer",
		},
		"share-alike license on a CC0 record": {
			rec:  strings.Replace(pkRecOne, `"license":"CC0-1.0"`, `"license":"CC-BY-SA-3.0"`, 1),
			want: "/license: value must be 'CC0-1.0'",
		},
		// An anyOf reports every branch it tried, which is the useful answer for
		// a value that is neither a date nor a timestamp.
		"an impossible calendar date": {
			rec: strings.Replace(pkRecOne, `"language":"en"`, `"added_at":"2026-02-30","language":"en"`, 1),
			want: "/added_at: '2026-02-30' is not valid date-time: less than 20 characters long\n" +
				"/added_at: '2026-02-30' is not valid date: parsing time \"2026-02-30\": day out of range",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			packed := packValid()
			packed["works/0/0.json"] = packOf(map[string]string{
				"book-one": composite(pkWorkOne, map[string]string{"rec-one": c.rec}),
			})
			res := Load(mustWrite(t, packed))
			got := messagesAt(res.Problems, "works/0/0.json: entry book-one: recording rec-one")
			if got != c.want {
				t.Errorf("said:\n%s\nwant:\n%s\nall:\n%s", got, c.want, joinProblems(res.Problems))
			}
		})
	}
}

// messagesAt returns every problem message reported at path, sorted, one per
// line. Load already sorts, so this is stable.
func messagesAt(ps []Problem, path string) string {
	var found []string
	for _, p := range ps {
		if p.Path == path {
			found = append(found, p.Msg)
		}
	}
	if len(found) == 0 {
		return "<no problem at " + path + ">"
	}
	return strings.Join(found, "\n")
}

// A badly named directory is one fact, not one fact per pack that happens to
// sit under it.
func TestBadDirectoryNameReportedOnce(t *testing.T) {
	dir := t.TempDir()
	files := packValid()
	for _, slug := range []string{"m-book", "n-book", "p-book"} {
		files["works/Mm/"+slug+".json"] = packOf(map[string]string{slug: pkWork(slug)})
	}
	writeTree(t, dir, files)

	res := Load(dir)
	n := 0
	for _, p := range res.Problems {
		if strings.Contains(p.Msg, "is not a valid slug bound") && strings.Contains(p.Msg, "directory name") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("directory-name problem reported %d times, want 1; problems:\n%s", n, joinProblems(res.Problems))
	}
}

// The walker reaches an entry through its family's wrapper schema, so a wrapper
// is load-bearing rather than documentation. These are its own rules - not any
// entity schema's - reported at entry precision.
func TestWrapperSchemasAreLoadBearing(t *testing.T) {
	cases := map[string]struct {
		file string
		body string
		path string
		want string
	}{
		"entry key is not a slug": {
			file: "people/0.json",
			body: packOf(map[string]string{
				"Author One":   pkAuthorOne,
				"narrator-one": pkNarratorOne,
			}),
			path: "people/0.json: entry Author One",
			want: `entry key "Author One" is not a valid slug`,
		},
		"community entry holds neither sidecar": {
			file: "works-community/0/0.json",
			body: packOf(map[string]string{"book-one": `{}`}),
			path: "works-community/0/0.json: entry book-one",
			want: "minProperties: got 0, want 1",
		},
		"community entry holds an unknown member": {
			file: "works-community/0/0.json",
			body: packOf(map[string]string{"book-one": `{"notes":{}}`}),
			path: "works-community/0/0.json: entry book-one",
			want: "additional properties 'notes' not allowed",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			files := packValid()
			files[c.file] = c.body
			writeTree(t, dir, files)

			res := Load(dir)
			for _, p := range res.Problems {
				if p.Path == c.path && strings.Contains(p.Msg, c.want) {
					return
				}
			}
			t.Errorf("no problem at %q containing %q; problems:\n%s", c.path, c.want, joinProblems(res.Problems))
		})
	}
}
