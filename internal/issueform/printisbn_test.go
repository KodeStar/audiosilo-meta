package issueform

import (
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/check"
)

// One print ISBN in its two spellings, held by existing-work at xref.isbn, and an
// ISBN no record holds.
const (
	printISBN10 = "0870111175"
	printISBN13 = "9780870111174"
	freeISBN    = "9781473647633"
)

// printISBNTree is the seed tree with existing-work holding the print ISBN.
func printISBNTree(t *testing.T, overrides map[string]string) string {
	t.Helper()
	files := seedFiles()
	const workRel = "works/ex/existing-work/work.json"
	files[workRel] = strings.Replace(files[workRel], `"id": "existing-work",`,
		`"id": "existing-work",`+"\n"+`  "xref": {"isbn": ["`+printISBN10+`"]},`, 1)
	for rel, content := range overrides {
		files[rel] = content
	}
	dir := t.TempDir()
	testpack.Seed(t, dir, files)
	if res := check.Load(dir); !res.OK() {
		t.Fatalf("seed tree does not validate: %v", res.Problems)
	}
	return dir
}

// printISBNDoor is one way a form can put an ISBN on a recording.
type printISBNDoor struct {
	name, template string
	seed           map[string]string
	body           func(t *testing.T, isbn string) string
	// refused is the status the door gives any claimed ISBN.
	refused Status
}

func printISBNDoors() []printISBNDoor {
	mirrorSeed := map[string]string{recRel: libexOnlyRecording}
	return []printISBNDoor{
		{"add-recording", "add-recording", nil, func(t *testing.T, isbn string) string {
			return setField(t, addRecordingBody("existing-work", "Somebody New", "", true), fRecISBNs, isbn)
		}, StatusDuplicate},
		{"add-work", "add-work", nil, func(t *testing.T, isbn string) string {
			return setField(t, addWorkBody("Whatever Title", "Some Author", "en", "Some Narrator", "", "web", true), fRecISBNs, isbn)
		}, StatusDuplicate},
		{"correct-data", "correct-data", nil, func(_ *testing.T, isbn string) string {
			return correctBody(recAddress, "isbn", isbn, "a publisher's page", true)
		}, StatusDuplicate},
		{"takeover", "add-recording", mirrorSeed, func(t *testing.T, isbn string) string {
			return setField(t, addRecordingBody("existing-work", "John Smith", "US: B000000001", true), fRecISBNs, isbn)
		}, StatusNeedsHuman},
	}
}

// Every form door refuses a work's print ISBN on a recording, whichever spelling it is
// stated in, with the status it gives any other claimed ISBN, and writes nothing; any
// other ISBN goes through it as it did.
func TestFormDoorsAndAPrintISBN(t *testing.T) {
	for _, door := range printISBNDoors() {
		for _, isbn := range []string{printISBN10, printISBN13} {
			t.Run(door.name+" refuses "+isbn, func(t *testing.T) {
				dir := printISBNTree(t, door.seed)
				before := readFile(t, dir, recRel)
				res := Process(Options{DataDir: dir, Template: door.template, Body: door.body(t, isbn)})
				if res.Status != door.refused {
					t.Fatalf("status = %q, want %q; messages = %v", res.Status, door.refused, res.Messages)
				}
				if !anyContains(res.Messages, `the print ISBN of work "existing-work"`) {
					t.Errorf("the message must name the work: %v", res.Messages)
				}
				if after := readFile(t, dir, recRel); after != before {
					t.Errorf("a refused submission wrote the record:\n%s", after)
				}
			})
		}
		t.Run(door.name+" allows an ISBN no work holds", func(t *testing.T) {
			dir := printISBNTree(t, door.seed)
			res := Process(Options{DataDir: dir, Template: door.template, Body: door.body(t, freeISBN)})
			if res.Status != StatusOK {
				t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
			}
		})
	}
}

// An add-work form may state a print ISBN of its own, but not one a recording claims:
// the same submission's, or one the catalogue records, under either spelling.
func TestAddWorkPrintISBN(t *testing.T) {
	body := func(t *testing.T, recISBN, workISBN string) string {
		t.Helper()
		b := addWorkBody("Whatever Title", "Some Author", "en", "Some Narrator", "", "web", true)
		return setField(t, setField(t, b, fRecISBNs, recISBN), fWorkISBN, workISBN)
	}
	submit := func(t *testing.T, recording, recISBN, workISBN string) Result {
		t.Helper()
		dir := t.TempDir()
		files := seedFiles()
		if recording != "" {
			files[recRel] = strings.Replace(files[recRel], `"id": "john-smith-2020",`, `"id": "john-smith-2020", "isbn": ["`+recording+`"],`, 1)
		}
		testpack.Seed(t, dir, files)
		return Process(Options{DataDir: dir, Template: "add-work", Body: body(t, recISBN, workISBN)})
	}
	for _, tc := range []struct {
		name                         string
		recording, recISBN, workISBN string
		want                         Status
	}{
		{"its own, beside a different recording ISBN", "", freeISBN, printISBN13, StatusOK},
		{"stated as the recording's too", "", freeISBN, freeISBN, StatusInvalid},
		{"stated as the recording's too, other spelling", "", printISBN10, printISBN13, StatusInvalid},
		{"a recording in the catalogue holds it", printISBN13, freeISBN, printISBN13, StatusDuplicate},
		{"a recording in the catalogue holds the other spelling", printISBN10, freeISBN, printISBN13, StatusDuplicate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := submit(t, tc.recording, tc.recISBN, tc.workISBN)
			if res.Status != tc.want {
				t.Fatalf("status = %q, want %q; messages = %v", res.Status, tc.want, res.Messages)
			}
		})
	}
}
