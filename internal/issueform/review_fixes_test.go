package issueform

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// --- Fix 1: a bare ASIN with no region prefix defaults to us ----------------

func TestParseASINsBareDefaultsToUS(t *testing.T) {
	c := &composer{}
	got := c.parseASINs("US: B017V4IMVG\nB0BAREASIN\nGB: B017WPFBHU\nnot an asin at all")
	want := []outASIN{
		{Region: "us", ASIN: "B017V4IMVG"},
		{Region: "us", ASIN: "B0BAREASIN"}, // bare -> defaulted to us
		{Region: "uk", ASIN: "B017WPFBHU"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseASINs = %+v, want %+v (messages: %v)", got, want, c.messages)
	}
	if !anyContains(c.messages, "defaulted to us") {
		t.Errorf("expected a default-region note, got %v", c.messages)
	}
	// The garbage line is still rejected (not turned into a us ASIN).
	if !anyContains(c.messages, "neither") {
		t.Errorf("expected the non-ASIN line to be rejected, got %v", c.messages)
	}
}

// --- Fix 4: abridged is tri-state (omitted when Unknown) --------------------

// abridgedBody builds a minimal, valid add-work body with a chosen rec_abridged
// value. Title/narrator/release are fixed so the recording path is deterministic:
// works/ab/abridge-book/recordings/bob-reader-1999.json.
func abridgedBody(abridged string) string {
	return field(fWorkTitle, "Abridge Book") +
		field(fWorkAuthors, "Al Author") +
		field(fWorkLanguage, "en") +
		field(fRecNarrators, "Bob Reader") +
		field(fRecAbridged, abridged) +
		field(fRecRelease, "1999-11-01") +
		field(fSources, "web") +
		"### " + fCC0 + "\n\n" + checkedBox()
}

func TestAbridgedTriState(t *testing.T) {
	const recRel = "works/ab/abridge-book/recordings/bob-reader-1999.json"
	cases := []struct {
		abridged string
		wantHas  bool
		wantVal  string
	}{
		{"Unknown", false, ""},
		{"", false, ""}, // empty dropdown also omits
		{"Unabridged", true, `"abridged": false`},
		{"Abridged", true, `"abridged": true`},
	}
	for _, c := range cases {
		t.Run(c.abridged, func(t *testing.T) {
			dir := seedTree(t)
			res := Process(Options{DataDir: dir, Template: "add-work", Body: abridgedBody(c.abridged), Date: "2026-07-14"})
			if res.Status != StatusOK {
				t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
			}
			rec := readFile(t, dir, recRel)
			if c.wantHas {
				if !strings.Contains(rec, c.wantVal) {
					t.Errorf("recording missing %s:\n%s", c.wantVal, rec)
				}
			} else if strings.Contains(rec, `"abridged"`) {
				t.Errorf("abridged must be omitted for %q, but recording has it:\n%s", c.abridged, rec)
			}
		})
	}
}

// --- Fix 6: a person meta URL resolves to a people/ path --------------------

func TestResolveRecordRefURLKinds(t *testing.T) {
	cases := []struct {
		name     string
		ref      string
		wantKind model.Kind
		wantSlug string
	}{
		{"work", "https://meta.audiosilo.app/work?id=some-book", model.KindWork, "some-book"},
		{"series", "https://meta.audiosilo.app/series?id=some-series", model.KindSeries, "some-series"},
		{"person", "https://meta.audiosilo.app/person?id=some-author", model.KindPerson, "some-author"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ref, ok := resolveRecordRef(c.ref)
			if !ok {
				t.Fatalf("resolveRecordRef(%q) not ok", c.ref)
			}
			if ref.kind != c.wantKind || ref.slug != c.wantSlug {
				t.Errorf("resolveRecordRef(%q) = %v/%q, want %v/%q", c.ref, ref.kind, ref.slug, c.wantKind, c.wantSlug)
			}
		})
	}
}

// --- Fix 8: a field value line starting with # is part of the value ---------

func TestParseBodyHashValue(t *testing.T) {
	body := "### Sources\n\n# 1 New York Times bestseller\nSee the Audible page\n\n" +
		"### " + fCC0 + "\n\n- [x] agree\n"
	s := parseBody(body)
	got := s.get("Sources")
	if !strings.Contains(got, "# 1 New York Times bestseller") || !strings.Contains(got, "See the Audible page") {
		t.Errorf("Sources value truncated at the # line: %q", got)
	}
	// The following field must still parse (the # line did not start a new section).
	if !s.checked(fCC0) {
		t.Error("the CC0 field after a #-containing value did not parse")
	}
}

// --- Fix 9: extractAttachment prefers an allowlisted host -------------------

func TestExtractAttachmentPrefersAllowedHost(t *testing.T) {
	// A prose Goodreads link sits ABOVE the real GitHub attachment.
	attach := "https://github.com/user-attachments/files/9/characters.json"
	block := "See also [Goodreads](https://www.goodreads.com/book/show/123)\n\n" +
		"[characters.json](" + attach + ")"
	url, inline, ok := extractAttachment(block)
	if !ok || inline != nil {
		t.Fatalf("expected a URL attachment, got ok=%v inline=%v", ok, inline)
	}
	if url != attach {
		t.Errorf("extractAttachment picked %q, want the GitHub attachment %q", url, attach)
	}

	// When no link is on an allowed host, it falls back to the first (so
	// defaultFetch surfaces its clear not-allowed-host failure).
	only := "[ref](https://www.goodreads.com/book/show/1)"
	url2, _, ok2 := extractAttachment(only)
	if !ok2 || url2 != "https://www.goodreads.com/book/show/1" {
		t.Errorf("fallback = %q ok=%v, want the sole link", url2, ok2)
	}
}

// --- Fix 12: the license layer rides on the Result --------------------------

func TestResultLicenseLayer(t *testing.T) {
	dir := seedTree(t)

	core := Process(Options{DataDir: dir, Template: "add-work", Body: abridgedBody("Unknown"), Date: "2026-07-14"})
	if core.Status != StatusOK {
		t.Fatalf("core status = %q, %v", core.Status, core.Messages)
	}
	if core.License != "CC0-1.0 (factual core)" {
		t.Errorf("core License = %q", core.License)
	}

	side := Process(Options{DataDir: seedTree(t), Template: "characters", Body: charactersBody("existing-work", validCharactersJSON, true)})
	if side.Status != StatusOK {
		t.Fatalf("sidecar status = %q, %v", side.Status, side.Messages)
	}
	if side.License != "CC BY-SA 4.0 (community expressive layer)" {
		t.Errorf("sidecar License = %q", side.License)
	}
}

// --- Fix 2 (issueform side): an Audiobookshelf import is automated -----------

const audiosiloBooksEnvelope = `{"format":"audiosilo-books","version":1,"books":[` +
	`{"title":"Imported ABS Book","authors":["Abs Author"],"narrators":["Abs Narrator"],` +
	`"asin":"B0ABSBOOK1","language":"en","abridged":true}]}`

func TestImportAudiobookshelfOK(t *testing.T) {
	dir := seedTree(t)
	body := importBody("Audiobookshelf (audiosilo-books JSON)", audiosiloBooksEnvelope)
	res := Process(Options{DataDir: dir, Template: "import", Body: body})
	if res.Status != StatusOK {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}
	if !fileExists(t, dir, "works/im/imported-abs-book/work.json") {
		t.Errorf("imported work not written")
	}
}

// --- helpers ----------------------------------------------------------------

func anyContains(msgs []string, sub string) bool {
	for _, m := range msgs {
		if strings.Contains(m, sub) {
			return true
		}
	}
	return false
}

func fileExists(t *testing.T, dir, address string) bool {
	t.Helper()
	return recordExists(t, dir, address)
}
