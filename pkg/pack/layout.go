package pack

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
)

// Layout is the storage layout a family root is in.
type Layout int

const (
	// LayoutAbsent means the family root is missing or holds no JSON file.
	LayoutAbsent Layout = iota
	// LayoutPack means the root holds pack files.
	LayoutPack
	// LayoutLegacy means the root holds the file-per-entity tree
	// (works/<shard>/<slug>/work.json and friends).
	LayoutLegacy
)

func (l Layout) String() string {
	switch l {
	case LayoutPack:
		return "pack"
	case LayoutLegacy:
		return "legacy"
	default:
		return "absent"
	}
}

// DetectLayout reports which layout family f's root under dataDir is in. The
// discriminator is the pack wrapper: a pack file's top level is an "entries"
// object, which no entity record can carry (every entity schema is
// additionalProperties:false and none defines "entries"). File depth cannot
// decide it on its own, because a people pack that has gained a directory level
// sits at the same depth as a legacy people record.
//
// ANY file that reads as a pack makes the family pack layout; only a root where
// no file does at all is legacy. One stray or unreadable file must not be able
// to flip a converted family back - the readers refuse a legacy family outright
// now, so a misdetection would hide every record in it, while a stray file in a
// pack family is reported as exactly that (metacheck's unrecognized location,
// metafmt's salvage). The scan stops at the first pack, so the normal case costs
// one file read.
func DetectLayout(dataDir string, f Family) (Layout, error) {
	files, err := jsonFilesUnder(dataDir, f.Root())
	if err != nil {
		return LayoutAbsent, err
	}
	return detectLayoutIn(dataDir, files)
}

// detectLayoutIn decides a family's layout from its files, however they were
// listed - a walk of the family root, or a whole-tree Listing. rels must already
// be narrowed to the JSON files pkg/pack reads (see Listing.packFiles), so the
// two routes see the same set and cannot disagree.
func detectLayoutIn(dataDir string, rels []string) (Layout, error) {
	if len(rels) == 0 {
		return LayoutAbsent, nil
	}
	for _, rel := range rels {
		raw, err := os.ReadFile(filepath.Join(dataDir, filepath.FromSlash(rel)))
		if err != nil {
			if os.IsNotExist(err) {
				continue // raced with a delete: it identifies nothing either way
			}
			return LayoutAbsent, err
		}
		if hasEntriesKey(json.NewDecoder(bytes.NewReader(raw))) {
			return LayoutPack, nil
		}
	}
	return LayoutLegacy, nil
}

// hasEntriesKey reports whether the next JSON value is an object with a
// top-level "entries" member. It reads keys only - the values are skipped - so
// identifying the layout costs a token scan of one object rather than decoding
// a whole pack. A file it cannot read identifies nothing and reads as legacy,
// where the per-file reader reports the JSON error against the file itself.
func hasEntriesKey(dec *json.Decoder) bool {
	tok, err := dec.Token()
	if err != nil {
		return false
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return false
	}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return false
		}
		if key, _ := kt.(string); key == "entries" {
			return true
		}
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return false
		}
	}
	return false
}

// Detect reports the layout of every family under dataDir.
//
// It takes ONE walk of the tree, not one per family: a caller that already
// holds a Listing asks it directly (Listing.Layouts), and this is that, over a
// walk taken for the occasion.
func Detect(dataDir string) (map[Family]Layout, error) {
	l, err := listFor(dataDir)
	if err != nil {
		return nil, err
	}
	return l.Layouts()
}
