package pack

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
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
func DetectLayout(dataDir string, f Family) (Layout, error) {
	files, err := jsonFilesUnder(dataDir, f.Root())
	if err != nil {
		return LayoutAbsent, err
	}
	if len(files) == 0 {
		return LayoutAbsent, nil
	}
	sort.Strings(files)
	raw, err := os.ReadFile(filepath.Join(dataDir, filepath.FromSlash(files[0])))
	if err != nil {
		return LayoutAbsent, err
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		// An unparseable file identifies nothing. Call it legacy so the
		// per-file reader reports the JSON error against the file itself.
		return LayoutLegacy, nil
	}
	if _, ok := top["entries"]; ok {
		return LayoutPack, nil
	}
	return LayoutLegacy, nil
}

// Detect reports the layout of every family under dataDir.
func Detect(dataDir string) (map[Family]Layout, error) {
	out := make(map[Family]Layout, len(defs))
	for _, d := range Families() {
		l, err := DetectLayout(dataDir, d.Family)
		if err != nil {
			return nil, err
		}
		out[d.Family] = l
	}
	return out, nil
}
