package issueform

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/check"
)

// TestSplitNamesRejoinsASuffixPiece pins the form's own splitter to the
// importer's rule (importer.MergeSuffixPieces): a piece that is only a
// post-nominal belongs to the name before it, on either separator the form
// accepts, and one with nothing before it is dropped.
func TestSplitNamesRejoinsASuffixPiece(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"David Posen, MD", []string{"David Posen, MD"}},
		{"Anthony Rao, Ph.D.\nBob Reader", []string{"Anthony Rao, Ph.D.", "Bob Reader"}},
		{"Jane Roe\nJr.", []string{"Jane Roe, Jr."}},
		{"PhD, Jane Roe", []string{"Jane Roe"}},
		{"Jane Roe, Ed", []string{"Jane Roe", "Ed"}},
	}
	for _, c := range cases {
		if got := splitNames(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("splitNames(%q) = %q, want %q", c.in, got, c.want)
		}
		if got := splitNarratorNames(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("splitNarratorNames(%q) = %q, want %q", c.in, got, c.want)
		}
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
	if person := readFile(t, dir, "people/da/david-posen-md.json"); !strings.Contains(person, `"name": "David Posen, MD"`) {
		t.Errorf("the author is not named as submitted:\n%s", person)
	}
	if !recordExists(t, dir, "people/an/anthony-rao-ph-d.json") {
		t.Error("the narrator record is missing")
	}
	if res := check.Load(dir); !res.OK() {
		t.Fatalf("tree failed validation after the submission:\n%v", res.Problems)
	}
}
