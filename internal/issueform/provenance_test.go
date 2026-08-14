package issueform

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
)

// --- The marker's cross-language contract ------------------------------------

// The `libex: <ASIN>` marker line is WRITTEN by the site (TypeScript) and PARSED
// here (Go). Each side is otherwise tested against its own literal, so renaming
// the marker on one side leaves the other green and silently stops typing the
// provenance of every libex-seeded submission. This test reads the site module
// and drives THIS package's regex with the line that module composes - the same
// approach labels_test.go takes to the issue-form YAML.
const libexMapTS = "../../site/src/lib/libex-map.ts"

var (
	// The body of composeSources (tolerant of the signature's formatting).
	composeSourcesRE = regexp.MustCompile(`(?s)function\s+composeSources\b.*?\{(.*?)\n\}`)
	// The first element of the array it returns - the marker line - as a
	// template literal. Its being FIRST is part of the contract: splitLibexMarker
	// only sniffs the Sources field's first line.
	markerTemplateRE = regexp.MustCompile("return\\s*\\[\\s*`([^`]*)`")
	// One ${...} interpolation inside that literal.
	interpolationRE = regexp.MustCompile(`\$\{[^}]*\}`)
)

func TestLibexMarkerMatchesSiteTemplate(t *testing.T) {
	src, err := os.ReadFile(filepath.FromSlash(libexMapTS))
	if err != nil {
		t.Fatalf("read the site's marker writer: %v", err)
	}
	body := composeSourcesRE.FindStringSubmatch(string(src))
	if body == nil {
		t.Fatalf("%s no longer defines composeSources - the marker contract moved", libexMapTS)
	}
	tmpl := markerTemplateRE.FindStringSubmatch(body[1])
	if tmpl == nil {
		t.Fatalf("composeSources no longer returns a template literal as its first line:\n%s", body[1])
	}

	// The contract is a literal `libex: ` prefix followed by nothing but the
	// ASIN interpolation.
	const wantPrefix = "libex: "
	if !strings.HasPrefix(tmpl[1], wantPrefix) {
		t.Fatalf("marker template is %q, want it to start with %q", tmpl[1], wantPrefix)
	}
	rest := tmpl[1][len(wantPrefix):]
	if interpolationRE.ReplaceAllString(rest, "") != "" {
		t.Fatalf("marker template is %q, want %q plus a single ${asin} interpolation", tmpl[1], wantPrefix)
	}

	// Compose the line the site would emit and drive the Go parser with it.
	const sample = "B015RQON6I"
	line := interpolationRE.ReplaceAllString(tmpl[1], sample)
	m := libexMarkerRE.FindStringSubmatch(line)
	if m == nil {
		t.Fatalf("libexMarkerRE does not match the line the site writes: %q", line)
	}
	if m[1] != sample {
		t.Errorf("captured ASIN = %q, want %q", m[1], sample)
	}
	if asin, _ := splitLibexMarker(line + "\na human provenance note"); asin != sample {
		t.Errorf("splitLibexMarker(%q...) asin = %q, want %q", line, asin, sample)
	}
}

func TestSplitLibexMarker(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		wantASIN string
		wantRest string
	}{
		{
			name:     "site shape",
			in:       "libex: B015RQON6I\nLibex (libexdb.com) lookup for B015RQON6I (retrieved 2026-07-30)",
			wantASIN: "B015RQON6I",
			wantRest: "Libex (libexdb.com) lookup for B015RQON6I (retrieved 2026-07-30)",
		},
		{
			name:     "lower-case asin is normalized",
			in:       "libex: b015rqon6i\nnote",
			wantASIN: "B015RQON6I",
			wantRest: "note",
		},
		{
			name:     "marker only",
			in:       "libex: B015RQON6I",
			wantASIN: "B015RQON6I",
			wantRest: "",
		},
		{
			name:     "no marker",
			in:       "Audible US product page for B015RQON6I",
			wantASIN: "",
			wantRest: "Audible US product page for B015RQON6I",
		},
		{
			name:     "marker is not the first line",
			in:       "Audible US product page\nlibex: B015RQON6I",
			wantASIN: "",
			wantRest: "Audible US product page\nlibex: B015RQON6I",
		},
		{
			name:     "malformed: too short",
			in:       "libex: B015RQON6\nnote",
			wantASIN: "",
			wantRest: "libex: B015RQON6\nnote",
		},
		{
			name:     "malformed: trailing prose on the marker line",
			in:       "libex: B015RQON6I (looked up by hand)\nnote",
			wantASIN: "",
			wantRest: "libex: B015RQON6I (looked up by hand)\nnote",
		},
		{
			name:     "malformed: no asin",
			in:       "libex: unknown\nnote",
			wantASIN: "",
			wantRest: "libex: unknown\nnote",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asin, rest := splitLibexMarker(tc.in)
			if asin != tc.wantASIN {
				t.Errorf("asin = %q, want %q", asin, tc.wantASIN)
			}
			if rest != tc.wantRest {
				t.Errorf("rest = %q, want %q", rest, tc.wantRest)
			}
		})
	}
}

// recordSources reads the sources array of a composed record.
func recordSources(t *testing.T, dir, address string) []map[string]string {
	t.Helper()
	var doc struct {
		Sources []map[string]string `json:"sources"`
	}
	if err := json.Unmarshal([]byte(readFile(t, dir, address)), &doc); err != nil {
		t.Fatalf("parse %s: %v", address, err)
	}
	return doc.Sources
}

// composedRecords returns the records in dir that seedTree did not put there:
// exactly what a submission composed. Result.Files names packs now, so a test
// that wants to iterate the composed RECORDS asks the tree.
func composedRecords(t *testing.T, dir string) []string {
	t.Helper()
	seeded := map[string]bool{}
	for address := range seedFiles() {
		seeded[address] = true
	}
	var out []string
	for _, address := range testpack.Addresses(t, dir) {
		if !seeded[address] {
			out = append(out, address)
		}
	}
	return out
}

// TestAddWorkLibexMarkerTypedSource proves the site's `libex: <ASIN>` marker
// line becomes a typed provenance entry on EVERY composed record, with the
// human-authored remainder still riding as the user entry.
func TestAddWorkLibexMarkerTypedSource(t *testing.T) {
	dir := seedTree(t)
	human := "Libex (libexdb.com) lookup for B015RQON6I (retrieved 2026-07-30)"
	body := addWorkBody("Marked Book", "Mona Author", "en", "Milo Voice", "us: B015RQON6I", "libex: b015rqon6i\n"+human, true)
	body = strings.Replace(body, field(fWorkSeriesName, ""), field(fWorkSeriesName, "Marked Saga"), 1)
	body = strings.Replace(body, field(fWorkSeriesPosition, ""), field(fWorkSeriesPosition, "1"), 1)
	res := Process(Options{DataDir: dir, Template: "add-work", Body: body, Date: "2026-07-30"})
	if res.Status != StatusOK {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}
	// Iterate what was actually composed rather than a hard-coded list, so a
	// record kind added later through the plain c.source() helper fails here.
	composed := composedRecords(t, dir)
	if len(composed) < 4 {
		t.Fatalf("composed %d records, expected work + recording + person + series: %v", len(composed), composed)
	}

	for _, address := range composed {
		sources := recordSources(t, dir, address)
		if len(sources) != 2 {
			t.Fatalf("%s: got %d sources, want 2: %v", address, len(sources), sources)
		}
		typed, user := sources[0], sources[1]
		if typed["type"] != sourceLibexImport || typed["ref"] != "B015RQON6I" || typed["imported_at"] != "2026-07-30" {
			t.Errorf("%s: typed source = %v", address, typed)
		}
		if user["type"] != sourceUser || user["ref"] != human {
			t.Errorf("%s: user source = %v, want the human line only", address, user)
		}
		if strings.Contains(user["ref"], "libex:") {
			t.Errorf("%s: marker line was not stripped from the user source: %q", address, user["ref"])
		}
	}
}

// TestAddWorkLibexMarkerOnly covers a Sources field that held nothing but the
// marker: the user entry keeps the submitter's text verbatim rather than being
// stamped with an empty ref.
func TestAddWorkLibexMarkerOnly(t *testing.T) {
	dir := seedTree(t)
	body := addWorkBody("Bare Marker Book", "Bea Author", "en", "Ben Voice", "us: B015RQON6I", "libex: B015RQON6I", true)
	res := Process(Options{DataDir: dir, Template: "add-work", Body: body, Date: "2026-07-30"})
	if res.Status != StatusOK {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}
	sources := recordSources(t, dir, "works/ba/bare-marker-book/work.json")
	if len(sources) != 2 {
		t.Fatalf("got %d sources, want 2: %v", len(sources), sources)
	}
	if sources[0]["type"] != sourceLibexImport || sources[0]["ref"] != "B015RQON6I" {
		t.Errorf("typed source = %v", sources[0])
	}
	if sources[1]["type"] != sourceUser || sources[1]["ref"] != "libex: B015RQON6I" {
		t.Errorf("user source = %v, want the original text verbatim", sources[1])
	}
}

// TestAddWorkWithoutLibexMarkerUnchanged pins the no-match path: a submission
// whose Sources field is ordinary prose composes exactly as it did before.
func TestAddWorkWithoutLibexMarkerUnchanged(t *testing.T) {
	dir := seedTree(t)
	prose := "Audible US product page for B015RQON6I (read 2026-07-30)"
	body := addWorkBody("Plain Sources Book", "Pia Author", "en", "Pat Voice", "", prose, true)
	res := Process(Options{DataDir: dir, Template: "add-work", Body: body, Date: "2026-07-30"})
	if res.Status != StatusOK {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}
	sources := recordSources(t, dir, "works/pl/plain-sources-book/work.json")
	if len(sources) != 1 {
		t.Fatalf("got %d sources, want 1: %v", len(sources), sources)
	}
	if sources[0]["type"] != sourceUser || sources[0]["ref"] != prose {
		t.Errorf("source = %v, want the user entry with the full text", sources[0])
	}
}

// TestAddWorkLibexMarkerASINNotInSubmission is the forgery guard: a marker may
// only vouch for an ASIN the submission itself states. Stamped on an unrelated
// book it would record an auditably false provenance ref (and, via the
// importer's (type, ref) dedup, suppress a later genuine stamp), so the whole
// Sources text rides as free text instead.
func TestAddWorkLibexMarkerASINNotInSubmission(t *testing.T) {
	dir := seedTree(t)
	raw := "libex: B0FORGED12\nSaid to come from libex"
	// The submission's own ASIN is a different book entirely.
	body := addWorkBody("Forged Marker Book", "Fay Author", "en", "Fred Voice", "us: B015RQON6I", raw, true)
	res := Process(Options{DataDir: dir, Template: "add-work", Body: body, Date: "2026-07-30"})
	if res.Status != StatusOK {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}
	for _, address := range composedRecords(t, dir) {
		sources := recordSources(t, dir, address)
		if len(sources) != 1 {
			t.Fatalf("%s: got %d sources, want 1 (no typed entry): %v", address, len(sources), sources)
		}
		if sources[0]["type"] != sourceUser || sources[0]["ref"] != raw {
			t.Errorf("%s: source = %v, want the user entry with the full original text", address, sources[0])
		}
	}
}

// TestAddWorkLibexMarkerWithoutAnyASIN covers the same rule when the submission
// states no ASIN at all - there is nothing for the marker to be provenance for.
func TestAddWorkLibexMarkerWithoutAnyASIN(t *testing.T) {
	dir := seedTree(t)
	raw := "libex: B015RQON6I\nLibex lookup"
	body := addWorkBody("Unbacked Marker Book", "Una Author", "en", "Uri Voice", "", raw, true)
	res := Process(Options{DataDir: dir, Template: "add-work", Body: body, Date: "2026-07-30"})
	if res.Status != StatusOK {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}
	sources := recordSources(t, dir, "works/un/unbacked-marker-book/work.json")
	if len(sources) != 1 {
		t.Fatalf("got %d sources, want 1: %v", len(sources), sources)
	}
	if sources[0]["type"] != sourceUser || sources[0]["ref"] != raw {
		t.Errorf("source = %v, want the user entry with the full original text", sources[0])
	}
}

// TestAddRecordingLibexMarkerTypedSource covers the second form that carries a
// Sources field seeded by the lookup assist.
func TestAddRecordingLibexMarkerTypedSource(t *testing.T) {
	dir := seedTree(t)
	human := "Libex lookup for B222222222"
	body := addRecordingBody("existing-work", "Nina Voice", "US: B222222222", true)
	body = strings.Replace(body, field(fSources, "web"), field(fSources, "libex: B222222222\n"+human), 1)
	res := Process(Options{DataDir: dir, Template: "add-recording", Body: body, Date: "2026-07-30"})
	if res.Status != StatusOK {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}
	sources := recordSources(t, dir, "works/ex/existing-work/recordings/nina-voice-2021.json")
	if len(sources) != 2 {
		t.Fatalf("got %d sources, want 2: %v", len(sources), sources)
	}
	if sources[0]["type"] != sourceLibexImport || sources[0]["ref"] != "B222222222" {
		t.Errorf("typed source = %v", sources[0])
	}
	if sources[1]["type"] != sourceUser || sources[1]["ref"] != human {
		t.Errorf("user source = %v", sources[1])
	}
}
