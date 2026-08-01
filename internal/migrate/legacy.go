package migrate

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// The pre-migration layout, parsed here as a STORAGE LOCATION - the only place
// left that reads it as one.
//
// This parsing is metamigrate's OWN on purpose. The migration is the last thing
// that opens a file-per-record tree, and the same change set deleted
// model.ParseLocation and pkg/check's legacy reader - so if this package leaned
// on either, retiring them would have meant retiring the tool that still has to
// read the old tree.
//
// Two other packages parse the same path SHAPE, and neither is a reader of it:
// internal/issueform, because a correction form's `record` field is a reference
// a submitter types (and every older issue and link spells it that way), and
// internal/testpack, because a fixture address reads better than a family plus
// two map keys. Three small parsers, three different jobs, no shared dependency
// on a layout that no longer exists.
//
// The shapes it recognizes, and nothing else:
//
//	works/<shard>/<slug>/work.json
//	works/<shard>/<slug>/characters.json
//	works/<shard>/<slug>/recaps.json
//	works/<shard>/<slug>/recordings/<rec-slug>.json
//	people/<shard>/<slug>.json
//	series/<shard>/<slug>.json

// kind is the entity a pre-migration file holds.
type kind int

const (
	kindWork kind = iota
	kindRecording
	kindCharacters
	kindRecaps
	kindPerson
	kindSeries
)

// loc is a parsed pre-migration path. The shard directory is deliberately not
// kept: the migration reads the tree, it does not validate the shard rule it is
// abolishing (metacheck validated it on every commit up to this one).
type loc struct {
	kind kind
	// slug is the entity's own slug: the recording slug for a recording, the
	// work slug for a work or one of its sidecars.
	slug string
	// workSlug is the parent work for a recording or a sidecar, empty otherwise.
	workSlug string
}

// parsePath interprets a data-relative, slash-separated path. ok is false for
// anything that is not one of the six recognized shapes.
func parsePath(rel string) (loc, bool) {
	parts := strings.Split(path.Clean(rel), "/")
	switch {
	case len(parts) == 4 && parts[0] == "works" && parts[3] == "work.json":
		return loc{kind: kindWork, slug: parts[2], workSlug: parts[2]}, true
	case len(parts) == 4 && parts[0] == "works" && parts[3] == "characters.json":
		return loc{kind: kindCharacters, slug: parts[2], workSlug: parts[2]}, true
	case len(parts) == 4 && parts[0] == "works" && parts[3] == "recaps.json":
		return loc{kind: kindRecaps, slug: parts[2], workSlug: parts[2]}, true
	case len(parts) == 5 && parts[0] == "works" && parts[3] == "recordings" && isJSON(parts[4]):
		return loc{kind: kindRecording, slug: trimJSON(parts[4]), workSlug: parts[2]}, true
	case len(parts) == 3 && parts[0] == "people" && isJSON(parts[2]):
		return loc{kind: kindPerson, slug: trimJSON(parts[2])}, true
	case len(parts) == 3 && parts[0] == "series" && isJSON(parts[2]):
		return loc{kind: kindSeries, slug: trimJSON(parts[2])}, true
	default:
		return loc{}, false
	}
}

func isJSON(name string) bool     { return len(name) > len(".json") && strings.HasSuffix(name, ".json") }
func trimJSON(name string) string { return name[:len(name)-len(".json")] }

// legacyWork is one work with everything that belonged to it: its own record,
// its recordings, and its two community sidecars.
type legacyWork struct {
	slug string
	work []byte
	recs map[string][]byte

	characters []byte
	recaps     []byte
}

// legacyTree is the whole pre-migration tree, in memory. The live tree is 48MB
// of JSON, so holding it is cheap - and holding it is what lets the conversion
// delete the old files BEFORE writing the new ones, which it must: a directory
// bound is a work slug, and a two-character work slug ("it") would otherwise
// collide with the shard directory of the same name.
type legacyTree struct {
	works  map[string]*legacyWork
	people map[string][]byte
	series map[string][]byte
	// files is every file read, data-relative and sorted: exactly what the
	// conversion deletes.
	files []string
}

// workSlugs returns the work slugs in sorted order.
func (t *legacyTree) workSlugs() []string { return sortedKeys(t.works) }

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// scan reads the whole pre-migration tree under dataDir.
//
// EVERY file has to land somewhere: a JSON file at a path it does not recognize
// is a hard error, and so is a file that is not JSON at all. The conversion
// deletes what it read and leaves nothing else behind, so a file it neither
// converts nor deletes would survive into the packed tree as debris that
// metacheck then reports forever - and one it converted from a shape it guessed
// at would be worse.
func scan(dataDir string) (*legacyTree, error) {
	t := &legacyTree{
		works:  map[string]*legacyWork{},
		people: map[string][]byte{},
		series: map[string][]byte{},
	}

	var unrecognized, foreign []string
	err := filepath.WalkDir(dataDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		relOS, rerr := filepath.Rel(dataDir, p)
		if rerr != nil {
			return rerr
		}
		rel := filepath.ToSlash(relOS)
		if !strings.HasSuffix(p, ".json") {
			foreign = append(foreign, rel)
			return nil
		}
		l, ok := parsePath(rel)
		if !ok {
			unrecognized = append(unrecognized, rel)
			return nil
		}
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		t.files = append(t.files, rel)
		return t.put(rel, l, raw)
	})
	if err != nil {
		return nil, err
	}
	if len(unrecognized) > 0 {
		sort.Strings(unrecognized)
		return nil, fmt.Errorf("%d file(s) are not part of the pre-migration layout and would be lost: %s",
			len(unrecognized), strings.Join(unrecognized, ", "))
	}
	if len(foreign) > 0 {
		sort.Strings(foreign)
		return nil, fmt.Errorf("%d file(s) under %s are not JSON and the data tree holds nothing else: %s "+
			"(move them out, then convert)", len(foreign), dataDir, strings.Join(foreign, ", "))
	}
	sort.Strings(t.files)

	for _, slug := range t.workSlugs() {
		w := t.works[slug]
		if w.work == nil {
			return nil, fmt.Errorf("works/%s: has recordings or sidecars but no work.json", slug)
		}
	}
	return t, nil
}

// put files one parsed record into the tree, refusing a second record for the
// same identity (which the old layout permitted across two shard directories).
func (t *legacyTree) put(rel string, l loc, raw []byte) error {
	switch l.kind {
	case kindPerson:
		if _, dup := t.people[l.slug]; dup {
			return fmt.Errorf("%s: a second person record for %q", rel, l.slug)
		}
		t.people[l.slug] = raw
	case kindSeries:
		if _, dup := t.series[l.slug]; dup {
			return fmt.Errorf("%s: a second series record for %q", rel, l.slug)
		}
		t.series[l.slug] = raw
	default:
		w := t.works[l.workSlug]
		if w == nil {
			w = &legacyWork{slug: l.workSlug, recs: map[string][]byte{}}
			t.works[l.workSlug] = w
		}
		switch l.kind {
		case kindWork:
			if w.work != nil {
				return fmt.Errorf("%s: a second work record for %q", rel, l.workSlug)
			}
			w.work = raw
		case kindRecording:
			if _, dup := w.recs[l.slug]; dup {
				return fmt.Errorf("%s: a second recording %q under work %q", rel, l.slug, l.workSlug)
			}
			w.recs[l.slug] = raw
		case kindCharacters:
			if w.characters != nil {
				return fmt.Errorf("%s: a second characters sidecar for %q", rel, l.workSlug)
			}
			w.characters = raw
		case kindRecaps:
			if w.recaps != nil {
				return fmt.Errorf("%s: a second recaps sidecar for %q", rel, l.workSlug)
			}
			w.recaps = raw
		}
	}
	return nil
}

// removeAll deletes every file the scan read, then prunes the directories they
// left empty. It is called only for an in-place conversion, after every pack's
// bytes are already rendered in memory.
func removeAll(dataDir string, files []string) error {
	for _, rel := range files {
		if err := os.Remove(filepath.Join(dataDir, filepath.FromSlash(rel))); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return pruneEmptyDirs(dataDir)
}

// pruneEmptyDirs removes every empty directory under root, deepest first. root
// itself is kept.
func pruneEmptyDirs(root string) error {
	var dirs []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && p != root {
			dirs = append(dirs, p)
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Deepest first, so a directory that only held empty directories also goes.
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	for _, d := range dirs {
		ents, err := os.ReadDir(d)
		if err != nil {
			return err
		}
		if len(ents) == 0 {
			if err := os.Remove(d); err != nil {
				return err
			}
		}
	}
	return nil
}
