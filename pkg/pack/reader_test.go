package pack

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A Reader's whole purpose is that a pack is read once, so every case here is
// about what happens the SECOND time a path is asked for - and about the store
// sharing that with anything else reading the same tree.

// removing the file after a read is the proof that the second answer did not
// come from disk.
func TestReaderReadsOnce(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"people/0.json": packOf1("ann-doe")})
	r := NewReader(dir)
	if _, err := r.Read("people/0.json"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "people", "0.json")); err != nil {
		t.Fatal(err)
	}
	f, err := r.Read("people/0.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Get("ann-doe"); !ok {
		t.Fatal("the second read went to disk")
	}
	r.Drop()
	f, err = r.Read("people/0.json")
	if err != nil {
		t.Fatal(err)
	}
	if f.Len() != 0 {
		t.Fatal("Drop left the pack cached")
	}
}

// A file that does not exist reads as an empty pack, which is what lets a first
// write into a family that has none work without ceremony.
func TestReaderMissingFileIsEmpty(t *testing.T) {
	r := NewReader(t.TempDir())
	f, err := r.Read("people/0.json")
	if err != nil {
		t.Fatal(err)
	}
	if f.Len() != 0 {
		t.Fatalf("missing file read as %d entries", f.Len())
	}
}

// A file that is not a pack is an error naming it, not an empty read.
func TestReaderParseErrorNamesThePath(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"people/0.json": `{"entries": {`})
	_, err := NewReader(dir).Read("people/0.json")
	if err == nil || !strings.HasPrefix(err.Error(), "people/0.json: ") {
		t.Fatalf("err = %v, want one naming people/0.json", err)
	}
	if errors.Is(err, fs.ErrNotExist) {
		t.Fatal("a parse failure must not read as a missing file")
	}
}

// Cached is how a caller that does its own reading (pkg/check, which reports a
// read failure and a parse failure differently) still answers from a pack the
// store has parsed. It answers for a path that was READ, and for nothing else.
func TestReaderCachedOnlyAnswersForAReadPack(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"people/0.json": packOf1("ann-doe")})
	r := NewReader(dir)
	if _, ok := r.Cached("people/0.json"); ok {
		t.Fatal("Cached found an unread path")
	}
	if _, err := r.Read("people/0.json"); err != nil {
		t.Fatal(err)
	}
	got, ok := r.Cached("people/0.json")
	if !ok {
		t.Fatal("Cached did not answer for a pack the reader had read")
	}
	if _, has := got.Get("ann-doe"); !has {
		t.Fatal("Cached answered with the wrong pack")
	}
	r.Drop()
	if _, ok := r.Cached("people/0.json"); ok {
		t.Fatal("Drop left the pack cached")
	}
}

// The empty pack Read stands in for a MISSING file is not a parse of anything,
// and Cached must not offer it as one: to pkg/check a listed pack that is not
// there is a read failure to report, not an empty pack to validate.
func TestReaderCachedRefusesTheMissingFileStandIn(t *testing.T) {
	r := NewReader(t.TempDir())
	f, err := r.Read("people/0.json")
	if err != nil {
		t.Fatal(err)
	}
	if f.Len() != 0 {
		t.Fatal("missing file did not read as an empty pack")
	}
	if _, ok := r.Cached("people/0.json"); ok {
		t.Fatal("Cached handed out the stand-in for a file that is not there")
	}
	// A real empty pack, by contrast, IS a parse and is cached as one.
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"series/0.json": `{"entries": {}}`})
	r2 := NewReader(dir)
	if _, err := r2.Read("series/0.json"); err != nil {
		t.Fatal(err)
	}
	if _, ok := r2.Cached("series/0.json"); !ok {
		t.Fatal("Cached refused a real, empty pack")
	}
}

// The store reads through its Reader, so a pack put there before the store
// looks is the pack the store sees - which is what makes the shared read
// possible in both directions.
func TestStoreReadsThroughItsReader(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"people/0.json": packOf1("ann-doe")})
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Get(FamilyPeople, "ann-doe"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Reader().Cached("people/0.json"); !ok {
		t.Fatal("a store read left nothing in its reader")
	}
	if err := os.Remove(filepath.Join(dir, "people", "0.json")); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.Get(FamilyPeople, "ann-doe"); err != nil || !ok {
		t.Fatalf("the store re-read a pack it had already parsed (ok=%v err=%v)", ok, err)
	}
}

// A Flush changes the files, so everything read from them before it is stale:
// the parse cache is dropped and the walk is given up.
func TestFlushDropsTheReaderAndListing(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"people/0.json": packOf1("ann-doe")})
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Listing() == nil {
		t.Fatal("Open kept no listing")
	}
	if err := s.Upsert(FamilyPeople, "bob-roe", []byte(`{"id":"bob-roe"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if s.Listing() != nil {
		t.Error("Flush kept a listing that no longer describes the tree")
	}
	if _, ok := s.Reader().Cached("people/0.json"); ok {
		t.Error("Flush kept a parse of a file it had just rewritten")
	}
	// And the store still works, off disk: the flushed entry reads back.
	if _, ok, err := s.Get(FamilyPeople, "bob-roe"); err != nil || !ok {
		t.Fatalf("post-flush read (ok=%v err=%v)", ok, err)
	}
}
