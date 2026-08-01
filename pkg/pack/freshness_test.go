package pack

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// freshness_test.go is about the difference between what the store WALKED and
// what is on disk NOW.
//
// The store keeps its Open-time walk, which is a fine basis for reading (nothing
// reaches disk before Flush) and a wrong one for deciding what to write. A
// survey decides what gets relocated, rewritten and deleted, so it re-scans; a
// file that appeared since must be seen, and one that went away must not be
// surveyed as an empty pack. And a Flush - including one that fails part-way,
// having already committed some families - has to give the walk up.

// A file that appears after Open is content the tree holds, and Pending/Heal are
// what account for it. Answering from the Open-time walk reported the family
// clean and left the file sitting there.
func TestSurveySeesAFileCreatedAfterOpen(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"people/0.json": packOfEntries([2]string{"ann-doe", "a"})})
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Something drops a file into the family after the store walked it.
	writeFiles(t, dir, map[string]string{"people/notes/aside.json": packOfEntries([2]string{"zoe-poe", "z"})})

	p, err := s.Pending(FamilyPeople)
	if err != nil {
		t.Fatal(err)
	}
	if p.Empty() {
		t.Fatal("the family reports clean: the file created after Open is invisible")
	}
	if _, err := s.Heal(FamilyPeople); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := entriesOf(t, dir, FamilyPeople); !strings.Contains(strings.Join(got, ","), "zoe-poe") {
		t.Errorf("healed tree holds %v, want the salvaged entry", got)
	}
	if left, err := os.Stat(filepath.Join(dir, "people", "notes", "aside.json")); err == nil {
		t.Errorf("the salvaged file is still there (%d bytes)", left.Size())
	}
	if p := pendingOf(t, dir, FamilyPeople); !p.Empty() {
		t.Errorf("tree is not well-formed after heal+flush:\n%s", strings.Join(p.Lines(), "\n"))
	}
}

// The other direction: a pack DELETED after Open must not be surveyed at all.
// Read makes an empty pack stand in for a file that is not there, so surveying
// the stale walk classified the gone file as a real, empty pack - something to
// report and delete rather than something that was never there.
func TestSurveyIgnoresAPackDeletedAfterOpen(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"people/0.json":        packOfEntries([2]string{"ann-doe", "a"}),
		"people/mary-doe.json": packOfEntries([2]string{"mary-doe", "m"}),
	})
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "people", "mary-doe.json")); err != nil {
		t.Fatal(err)
	}
	p, err := s.Pending(FamilyPeople)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Empty() {
		t.Errorf("a pack that is no longer there is still being surveyed:\n%s", strings.Join(p.Lines(), "\n"))
	}
}

// A Flush that fails part-way has still written something, so the walk and the
// parses it invalidates are no less stale than a successful flush's. Returning
// early on the failing family used to keep both.
func TestFailedFlushStillGivesUpTheWalkAndTheParses(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("needs a non-root uid: the failure is injected with file permissions")
	}
	dir := t.TempDir()
	// people flushes before series; make series unwritable so the flush fails
	// only once people has been committed.
	writeFiles(t, dir, map[string]string{
		"people/0.json": packOfEntries([2]string{"ann-doe", "a"}),
		"series/0.json": packOfEntries([2]string{"s-one", "m"}),
	})
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Get(FamilyPeople, "ann-doe"); err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(FamilyPeople, "bob-roe", json.RawMessage(entry("bob-roe", "b"))); err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(FamilySeries, "s-two", json.RawMessage(entry("s-two", "m"))); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, "series", "0.json"), 0o444); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Flush(); err == nil {
		t.Fatal("expected the flush to fail on series")
	}
	if err := os.Chmod(filepath.Join(dir, "series", "0.json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s.Listing() != nil {
		t.Error("a failed flush kept the walk it had already invalidated")
	}
	if _, ok := s.Reader().Cached("people/0.json"); ok {
		t.Error("a failed flush kept a parse of a file it had just rewritten")
	}
}

// The sharpest case for the fresh scan: a partial flush SPLITS a pack, so the
// tree now holds a file that no walk taken before the flush could name. A survey
// answering from the Open-time walk saw one pack where there were two and planned
// against that - and everything downstream of a survey (placement, relocation,
// deletion) is only as true as the file list it started from.
func TestSurveyAfterAPartialFlushSeesEveryPackOnDisk(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("needs a non-root uid: the failure is injected with file permissions")
	}
	dir := t.TempDir()
	// One people pack well over the hard cap, so touching it makes Flush split.
	var people []string
	for c := 'a'; c <= 'z'; c++ {
		for i := range 2 {
			people = append(people, fmt.Sprintf("p-%c%d", c, i))
		}
	}
	sort.Strings(people)
	writeFiles(t, dir, map[string]string{
		"people/0.json": fatPack(people, 20_000),
		"series/0.json": packOfEntries([2]string{"s-one", "m"}),
	})
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(FamilyPeople, "p-a0", json.RawMessage(fatEntry("p-a0", 20_000))); err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(FamilySeries, "s-two", json.RawMessage(entry("s-two", "m"))); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, "series", "0.json"), 0o444); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Flush(); err == nil {
		t.Fatal("expected the flush to fail on series")
	}
	if err := os.Chmod(filepath.Join(dir, "series", "0.json"), 0o644); err != nil {
		t.Fatal(err)
	}
	onDisk, err := jsonFilesUnder(dir, "people")
	if err != nil {
		t.Fatal(err)
	}
	if len(onDisk) < 2 {
		t.Fatalf("people did not split, the case proves nothing: %v", onDisk)
	}

	fs, err := s.survey(defs[FamilyPeople])
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	for _, ref := range fs.tree.Packs() {
		seen = append(seen, ref.Path())
	}
	if !slices.Equal(seen, onDisk) {
		t.Errorf("survey sees %v, the tree holds %v: it is planning against a walk the flush invalidated", seen, onDisk)
	}

	if _, err := s.Heal(FamilyPeople); err != nil {
		t.Fatal(err)
	}
	// A slug living in the pack the split created.
	if err := s.Upsert(FamilyPeople, "p-n0", json.RawMessage(fatEntry("p-n0", 10))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Flush(); err != nil {
		t.Fatal(err)
	}

	homes := map[string][]string{}
	rels, err := jsonFilesUnder(dir, "people")
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range rels {
		raw, rerr := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if rerr != nil {
			t.Fatal(rerr)
		}
		file, perr := Parse(raw)
		if perr != nil {
			t.Fatalf("%s: %v", rel, perr)
		}
		for _, slug := range file.Slugs() {
			homes[slug] = append(homes[slug], rel)
		}
	}
	var dup []string
	for slug, packs := range homes {
		if len(packs) > 1 {
			dup = append(dup, fmt.Sprintf("%s -> %v", slug, packs))
		}
	}
	sort.Strings(dup)
	if len(dup) > 0 {
		t.Errorf("%d slugs live in two packs at once, e.g. %s", len(dup), dup[0])
	}
	if p := pendingOf(t, dir, FamilyPeople); !p.Empty() {
		t.Errorf("tree is not well-formed after heal+flush:\n%s", strings.Join(p.Lines(), "\n"))
	}
}

// fatEntry renders an entry of roughly n bytes, for a pack that has to be big
// enough to split.
func fatEntry(slug string, n int) string {
	return `{"id":"` + slug + `","name":"` + strings.Repeat("x", n) + `"}`
}

// fatPack renders a pack of fatEntry entries.
func fatPack(slugs []string, n int) string {
	var b strings.Builder
	b.WriteString(`{"entries":{`)
	for i, s := range slugs {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`"` + s + `":` + fatEntry(s, n))
	}
	b.WriteString(`}}`)
	return b.String()
}
