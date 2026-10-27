package main

import (
	"net/http"
	"net/http/httptest"
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
