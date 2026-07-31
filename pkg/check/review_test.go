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
		"legacy recording": {
			files: func() map[string]string {
				f := legacyTwin()
				f["works/bo/book-one/recordings/rec-one.json"] = strings.Replace(
					f["works/bo/book-one/recordings/rec-one.json"], `"language":"en"`, `"language":"en","runtime_min":1e3`, 1)
				return f
			}(),
			path: "works/bo/book-one/recordings/rec-one.json",
		},
		"legacy community sidecar": {
			files: func() map[string]string {
				f := legacyTwin()
				f["works/bo/book-one/recaps.json"] = strings.Replace(
					validRecaps("book-one"), `"through":{"chapter":3}`, `"through":{"chapter":3e0}`, 1)
				return f
			}(),
			path: "works/bo/book-one/recaps.json",
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

// work.schema.json gained "recordings" so a PACK entry can carry the composite.
// That must not make a per-file work legal with one: model.Work drops the field,
// so the recordings would load as nothing while the gate stayed green.
func TestLegacyWorkRejectsRecordingsMember(t *testing.T) {
	dir := t.TempDir()
	files := legacyTwin()
	files["works/bo/book-one/work.json"] = composite(pkWorkOne, map[string]string{"rec-one": pkRecOne})
	writeTree(t, dir, files)

	res := Load(dir)
	if !hasProblem(res.Problems, `"recordings" is a pack-composite field`) {
		t.Fatalf("a legacy work carrying a recordings map was accepted; problems:\n%s", joinProblems(res.Problems))
	}
}

// Nothing about the layout stops a work from having its sidecars in BOTH places
// during the migration window - packed into works-community and still sitting in
// its legacy work directory. Both copies load, and metabuild then dies on a raw
// UNIQUE constraint naming no file, so the duplicate has to be caught here.
func TestSidecarUniqueness(t *testing.T) {
	dir := t.TempDir()
	files := legacyTwin() // works/people/series legacy, sidecars in the work dir
	files["works-community/0/0.json"] = packOf(map[string]string{
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

// The two walkers must describe the same defect with the same words. They are
// different code reading different files, so the only thing keeping them in
// step is that both reach the reason through the same schemas - which is why
// this compares the MESSAGE, not just that each side failed.
func TestMessagesIdenticalAcrossLayouts(t *testing.T) {
	cases := map[string]struct {
		legacyRec string // replaces the legacy recording file
		want      string
	}{
		"missing a required property": {
			legacyRec: strings.Replace(pkRecOne, `"narrators":["narrator-one"],`, "", 1),
			want:      "(root): missing property 'narrators'",
		},
		"wrong type": {
			legacyRec: strings.Replace(pkRecOne, `"language":"en"`, `"language":"en","runtime_min":"nope"`, 1),
			want:      "/runtime_min: got string, want integer",
		},
		"share-alike license on a CC0 record": {
			legacyRec: strings.Replace(pkRecOne, `"license":"CC0-1.0"`, `"license":"CC-BY-SA-3.0"`, 1),
			want:      "/license: value must be 'CC0-1.0'",
		},
		// An anyOf reports every branch it tried, which is the useful answer for
		// a value that is neither a date nor a timestamp.
		"an impossible calendar date": {
			legacyRec: strings.Replace(pkRecOne, `"language":"en"`, `"added_at":"2026-02-30","language":"en"`, 1),
			want: "/added_at: '2026-02-30' is not valid date-time: less than 20 characters long\n" +
				"/added_at: '2026-02-30' is not valid date: parsing time \"2026-02-30\": day out of range",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			legacy := legacyTwin()
			legacy["works/bo/book-one/recordings/rec-one.json"] = c.legacyRec
			legacyRes := Load(mustWrite(t, legacy))

			packed := packValid()
			packed["works/0/0.json"] = packOf(map[string]string{
				"book-one": composite(pkWorkOne, map[string]string{"rec-one": c.legacyRec}),
			})
			packRes := Load(mustWrite(t, packed))

			legacyMsgs := messagesAt(legacyRes.Problems, "works/bo/book-one/recordings/rec-one.json")
			packMsgs := messagesAt(packRes.Problems, "works/0/0.json: entry book-one: recording rec-one")
			if legacyMsgs != c.want {
				t.Errorf("legacy said:\n%s\nwant:\n%s\nall:\n%s", legacyMsgs, c.want, joinProblems(legacyRes.Problems))
			}
			if packMsgs != legacyMsgs {
				t.Errorf("pack said:\n%s\nlegacy said:\n%s\nall:\n%s", packMsgs, legacyMsgs, joinProblems(legacyRes.Problems))
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
