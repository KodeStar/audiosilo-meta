package pack

import (
	"bytes"
	"encoding/json"
	"os"
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
	sample, err := firstJSONUnder(dataDir, f.Root())
	if err != nil {
		return LayoutAbsent, err
	}
	if sample == "" {
		return LayoutAbsent, nil
	}
	raw, err := os.ReadFile(sample)
	if err != nil {
		return LayoutAbsent, err
	}
	if hasEntriesKey(json.NewDecoder(bytes.NewReader(raw))) {
		return LayoutPack, nil
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
