package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/pkg/check"
)

const librivoxFixture = "../../internal/importer/testdata/librivox/snapshot"

func TestLibrivoxImportRequiresFromAndIDs(t *testing.T) {
	run := func(string, importer.LibrivoxIDs, importer.Options) (importer.Summary, error) {
		t.Fatal("the importer ran despite a refused invocation")
		return importer.Summary{}, nil
	}
	for _, args := range [][]string{
		{"--ids", "1-"},             // no --from
		{"--from", librivoxFixture}, // no --ids
		{"--from", librivoxFixture, "--ids", "x"},
		{"--from", librivoxFixture, "--ids", "1-", "stray"},
	} {
		if code := runLibrivoxImport(args, run); code != 2 {
			t.Errorf("%v exit = %d, want 2", args, code)
		}
	}
}

func TestLibrivoxImportEndToEnd(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	args := []string{"--from", librivoxFixture, "--ids", "1-", "--data", dataDir, "--date", "2026-10-27"}
	out := captureStdout(t, func() {
		if code := runLibrivoxImport(args, importer.RunLibrivox); code != 0 {
			t.Errorf("exit = %d", code)
		}
	})
	if !strings.Contains(out, "librivox: 4 projects selected") || !strings.Contains(out, "4 new works, 4 new recordings") {
		t.Errorf("summary:\n%s", out)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("imported tree failed validation: %v", res.Problems)
	}
	// The rerun writes nothing.
	out = captureStdout(t, func() {
		if code := runLibrivoxImport(args, importer.RunLibrivox); code != 0 {
			t.Errorf("rerun exit = %d", code)
		}
	})
	if !strings.Contains(out, "0 new works, 0 new recordings, 0 new people") || !strings.Contains(out, "4 skipped (already present)") ||
		!strings.Contains(out, "0 pack files written") {
		t.Errorf("rerun summary:\n%s", out)
	}
}

func TestLibrivoxFetchRequiresOut(t *testing.T) {
	if code := runLibrivoxFetch(nil, importer.NewLibrivoxFetcher()); code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
}

func TestLibrivoxFetchWritesASnapshot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("offset") != "0" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"Audiobooks could not be found"}`))
			return
		}
		_, _ = w.Write([]byte(`{"books":[{"id":"1","title":"One"},{"id":"2","title":"Two"}]}`))
	}))
	defer srv.Close()
	f := importer.NewLibrivoxFetcher()
	f.FeedURL, f.HTTP, f.Spacing = srv.URL, srv.Client(), 0
	dir := t.TempDir()
	out := captureStdout(t, func() {
		if code := runLibrivoxFetch([]string{"--out", dir}, f); code != 0 {
			t.Errorf("exit = %d", code)
		}
	})
	if !strings.Contains(out, "snapshot holds 2 projects") || !strings.Contains(out, "complete") {
		t.Errorf("summary: %s", out)
	}
	projects, err := importer.ReadLibrivoxSnapshot(dir)
	if err != nil || len(projects) != 2 {
		t.Errorf("snapshot = %v, %v", projects, err)
	}
}

func TestLibrivoxImportPrintsTheWorklist(t *testing.T) {
	sum := importer.Summary{LibrivoxWorklist: []importer.LibrivoxWorklistEntry{{
		Project: 527, Title: "The Metamorphosis", Authors: []string{"Franz Kafka"}, Reason: importer.LibrivoxSeveralWorks,
		Candidates: []importer.LibrivoxCandidate{{Work: "metamorphosis", Title: "Metamorphosis"}, {Work: "metamorphosis-2", Title: "Metamorphosis"}},
	}}, LibrivoxFallbackSlugs: 2, UnrecordedCredits: []importer.UnrecordedCredit{{
		Project: 2027, AuthorID: 9876, Name: "Amy Coulter", Roles: []string{"translator"}, Work: "siddhartha",
	}, {
		Project: 2607, AuthorID: 6294, Name: "Йордан Йовков", Work: "staroplaninski-legendi", Reason: "unsluggable",
	}}, LibrivoxInProgress: []int{2073, 4333}, LibrivoxAuthorPairs: []importer.LibrivoxAuthorPair{{
		Titled: importer.LibrivoxPairAuthor{AuthorID: 1456, Name: "Mrs. C. M. Livingston", Person: "mrs-c-m-livingston", Project: 4714},
		Bare:   importer.LibrivoxPairAuthor{AuthorID: 15071, Name: "C. M. Livingston", Person: "c-m-livingston", Project: 9999},
	}}, LibrivoxAuthorSplits: []importer.LibrivoxAuthorSplit{{
		Author:  importer.LibrivoxPairAuthor{AuthorID: 16428, Name: "Ralph Scott", Person: "ralph-scott-librivox-author-16428", Project: 17170},
		Against: "ralph-scott",
	}}}
	run := func(string, importer.LibrivoxIDs, importer.Options) (importer.Summary, error) { return sum, nil }
	out := captureStdout(t, func() {
		if code := runLibrivoxImport([]string{"--from", librivoxFixture, "--ids", "527", "--dry-run"}, run); code != 0 {
			t.Errorf("exit = %d", code)
		}
	})
	for _, want := range []string{
		"librivox: skipped 2 in-progress projects (no runtime and no archive.org item; nothing written, re-import once complete): 2073, 4333",
		"librivox: 2 new works at librivox-<id> (non-Latin or unsluggable title)",
		"librivox worklist: 1 project skipped for a person to resolve",
		`  527 "The Metamorphosis" by Franz Kafka [several-works]: metamorphosis "Metamorphosis"; metamorphosis-2 "Metamorphosis"`,
		"credits not recorded: 2 (",
		`  2027 "Amy Coulter" (translator, librivox_author_id 9876) -> siddhartha, person not created`,
		`  2607 "Йордан Йовков" (author, librivox_author_id 6294) -> staroplaninski-legendi, unsluggable`,
		"librivox worklist: 1 author id pair one courtesy title apart, kept as two people for a person to merge or confirm:",
		`  1456 "Mrs. C. M. Livingston" -> mrs-c-m-livingston (project 4714); 15071 "C. M. Livingston" -> c-m-livingston (project 9999)`,
		"librivox worklist: 1 author id filed at <name>-librivox-author-<id> beside a same-name person, kept as two people for a person to merge or confirm:",
		`  16428 "Ralph Scott" -> ralph-scott-librivox-author-16428 (project 17170); apart from ralph-scott`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
}

func TestLibrivoxImportWritesTheWorklistFile(t *testing.T) {
	// meta#2751: the worklist is a file as well as printed lines, one NDJSON
	// row per project, appended run after run as --conflicts is.
	entry := importer.LibrivoxWorklistEntry{
		Project: 527, Title: "The Metamorphosis", Authors: []string{"Franz Kafka"}, Reason: importer.LibrivoxSeveralWorks,
		Candidates: []importer.LibrivoxCandidate{{Work: "metamorphosis", Title: "Metamorphosis"}},
	}
	run := func(string, importer.LibrivoxIDs, importer.Options) (importer.Summary, error) {
		return importer.Summary{LibrivoxWorklist: []importer.LibrivoxWorklistEntry{entry}}, nil
	}
	path := filepath.Join(t.TempDir(), "worklist.ndjson")
	for range 2 {
		captureStdout(t, func() {
			if code := runLibrivoxImport([]string{"--from", librivoxFixture, "--ids", "527", "--dry-run", "--worklist", path}, run); code != 0 {
				t.Errorf("exit = %d", code)
			}
		})
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	line := `{"librivox_id":527,"title":"The Metamorphosis","authors":["Franz Kafka"],"reason":"several-works","candidates":[{"work":"metamorphosis","title":"Metamorphosis"}]}` + "\n"
	if string(body) != line+line {
		t.Errorf("worklist file =\n%s", body)
	}
	if code := runLibrivoxImport([]string{"--from", librivoxFixture, "--ids", "527", "--worklist", filepath.Join(path, "x")}, run); code != 2 {
		t.Errorf("an unwritable --worklist exit = %d, want 2", code)
	}
}

func TestLibrivoxWorklistFileNeverWritesNullCandidates(t *testing.T) {
	// A worklist row naming no catalogued work (a fallback slot a tombstone
	// holds) is written with an empty list, never "candidates":null.
	var b strings.Builder
	entry := importer.LibrivoxWorklistEntry{Project: 99001, Title: "Война и мир", Authors: []string{"Leo Tolstoy"}, Reason: importer.LibrivoxFallbackTaken}
	if err := writeLibrivoxWorklist(&b, []importer.LibrivoxWorklistEntry{entry}); err != nil {
		t.Fatal(err)
	}
	if want := `{"librivox_id":99001,"title":"Война и мир","authors":["Leo Tolstoy"],"reason":"fallback-slug-taken","candidates":[]}` + "\n"; b.String() != want {
		t.Errorf("worklist line = %q, want %q", b.String(), want)
	}
}

// --skipped writes the run's refusals keyed by project id, atomically, once the
// run completed.
func TestLibrivoxImportWritesTheSkipped(t *testing.T) {
	sum := importer.Summary{Skips: []importer.RowSkip{
		{LibrivoxID: 99002, Reason: importer.RefusalUnmappedLanguage},
		{LibrivoxID: 99503, Reason: importer.RefusalIdentityDuplicate},
	}}
	run := func(string, importer.LibrivoxIDs, importer.Options) (importer.Summary, error) { return sum, nil }
	path := filepath.Join(t.TempDir(), "skipped.ndjson")
	captureStdout(t, func() {
		if code := runLibrivoxImport([]string{"--from", librivoxFixture, "--ids", "1-", "--dry-run", "--skipped", path}, run); code != 0 {
			t.Errorf("exit = %d", code)
		}
	})
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"librivox_id":99002,"reason":"unmapped-language"}` + "\n" + `{"librivox_id":99503,"reason":"identity-duplicate"}` + "\n"
	if string(got) != want {
		t.Errorf("skipped = %q, want %q", got, want)
	}
	// A failed run writes no worklist.
	failed := filepath.Join(t.TempDir(), "skipped.ndjson")
	failing := func(string, importer.LibrivoxIDs, importer.Options) (importer.Summary, error) {
		return importer.Summary{}, os.ErrInvalid
	}
	captureStdout(t, func() {
		_ = runLibrivoxImport([]string{"--from", librivoxFixture, "--ids", "1-", "--skipped", failed}, failing)
	})
	if _, err := os.Stat(failed); !os.IsNotExist(err) {
		t.Errorf("a failed run left %s: %v", failed, err)
	}
}
