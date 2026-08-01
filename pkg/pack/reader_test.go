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

// Cached and Hold are how a caller that does its own reading (pkg/check, which
// reports a read failure and a parse failure differently) joins the same
// sharing.
func TestReaderCachedAndHold(t *testing.T) {
	r := NewReader(t.TempDir())
	if _, ok := r.Cached("people/0.json"); ok {
		t.Fatal("Cached found an unread path")
	}
	held := NewFile()
	held.Set("ann-doe", []byte(`{"id":"ann-doe"}`))
	r.Hold("people/0.json", held)
	got, ok := r.Cached("people/0.json")
	if !ok || got != held {
		t.Fatal("Hold did not cache the file")
	}
	// A Read of a held path must not go to disk either - there is no file there
	// at all, so a read would have produced an empty pack.
	f, err := r.Read("people/0.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Get("ann-doe"); !ok {
		t.Fatal("Read ignored the held file")
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
