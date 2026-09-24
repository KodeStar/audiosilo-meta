package issueform

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/check"
)

// TestSplitNamesRejoinsASuffixPieceAcrossLines is the form-specific half of the
// importer's suffix-piece rule (internal/importer/suffixpiece.go, whose own
// tests cover the comma list): a form field also takes one name per LINE, and a
// line holding only a post-nominal belongs to the line before it.
func TestSplitNamesRejoinsASuffixPieceAcrossLines(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"Anthony Rao, Ph.D.\nBob Reader", []string{"Anthony Rao Ph.D.", "Bob Reader"}},
		{"Jane Roe\nJr.", []string{"Jane Roe Jr."}},
		{"MD\nJane Roe", []string{"Jane Roe"}},
		{"Jane Roe\nEd", []string{"Jane Roe", "Ed"}},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := splitNames(c.in); !reflect.DeepEqual(got, c.want) {
				t.Errorf("splitNames(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestAddWorkKeepsACredentialOnItsName is issue #2320 through the add-work
// form: the two credits compose as two people, not four.
func TestAddWorkKeepsACredentialOnItsName(t *testing.T) {
	dir := seedTree(t)
	body := addWorkBody("The Joy of Tension", "David Posen, MD", "en", "Anthony Rao, Ph.D.", "US: B333333334", "Audible product page", true)
	res := Process(Options{DataDir: dir, Template: "add-work", Body: body, Date: "2026-07-14"})
	if res.Status != StatusOK {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}
	for _, junk := range []string{"people/md/md.json", "people/ph/ph-d.json"} {
		if recordExists(t, dir, junk) {
			t.Errorf("%s was minted from a comma-split credential", junk)
		}
	}
	if person := readFile(t, dir, "people/da/david-posen-md.json"); !strings.Contains(person, `"name": "David Posen MD"`) {
		t.Errorf("the author is not named as submitted:\n%s", person)
	}
	if !recordExists(t, dir, "people/an/anthony-rao-ph-d.json") {
		t.Error("the narrator record is missing")
	}
	if res := check.Load(dir); !res.OK() {
		t.Fatalf("tree failed validation after the submission:\n%v", res.Problems)
	}
}
