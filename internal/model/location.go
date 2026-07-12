package model

import (
	"path"
	"regexp"
)

// slugPattern is the canonical slug regex used for every id across the dataset.
var slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// MaxSlugLen is the maximum allowed slug length.
const MaxSlugLen = 100

// ValidSlug reports whether s is a well-formed slug.
func ValidSlug(s string) bool {
	return len(s) <= MaxSlugLen && slugPattern.MatchString(s)
}

// Shard returns the directory shard for a slug: its first two characters (or
// the whole slug when it is shorter than two characters).
func Shard(slug string) string {
	if len(slug) < 2 {
		return slug
	}
	return slug[:2]
}

// Location is the parsed meaning of a data-tree file path.
type Location struct {
	Kind Kind
	// Slug is the entity's own slug (work slug for a work, recording slug for a
	// recording, etc).
	Slug string
	// WorkSlug is the parent work's slug for a recording; empty otherwise.
	WorkSlug string
	// Shard is the shard directory that appeared in the path.
	Shard string
}

// ParseLocation interprets a data-tree-relative, slash-separated path (e.g.
// "works/ha/harry-potter/work.json") and reports the entity it should hold.
// ok is false when the path does not match any recognized location pattern.
func ParseLocation(rel string) (Location, bool) {
	parts := splitClean(rel)
	switch {
	case len(parts) == 4 && parts[0] == "works" && parts[3] == "work.json":
		// works/<shard>/<workslug>/work.json
		return Location{Kind: KindWork, Slug: parts[2], Shard: parts[1]}, true
	case len(parts) == 4 && parts[0] == "works" && parts[3] == "characters.json":
		// works/<shard>/<workslug>/characters.json (per-work sidecar)
		return Location{Kind: KindCharacters, Slug: parts[2], WorkSlug: parts[2], Shard: parts[1]}, true
	case len(parts) == 4 && parts[0] == "works" && parts[3] == "recaps.json":
		// works/<shard>/<workslug>/recaps.json (per-work sidecar)
		return Location{Kind: KindRecaps, Slug: parts[2], WorkSlug: parts[2], Shard: parts[1]}, true
	case len(parts) == 5 && parts[0] == "works" && parts[3] == "recordings" && hasJSONExt(parts[4]):
		// works/<shard>/<workslug>/recordings/<recslug>.json
		return Location{
			Kind:     KindRecording,
			Slug:     trimJSONExt(parts[4]),
			WorkSlug: parts[2],
			Shard:    parts[1],
		}, true
	case len(parts) == 3 && parts[0] == "people" && hasJSONExt(parts[2]):
		// people/<shard>/<slug>.json
		return Location{Kind: KindPerson, Slug: trimJSONExt(parts[2]), Shard: parts[1]}, true
	case len(parts) == 3 && parts[0] == "series" && hasJSONExt(parts[2]):
		// series/<shard>/<slug>.json
		return Location{Kind: KindSeries, Slug: trimJSONExt(parts[2]), Shard: parts[1]}, true
	default:
		return Location{}, false
	}
}

func splitClean(rel string) []string {
	rel = path.Clean(rel)
	var out []string
	start := 0
	for i := 0; i <= len(rel); i++ {
		if i == len(rel) || rel[i] == '/' {
			if i > start {
				out = append(out, rel[start:i])
			}
			start = i + 1
		}
	}
	return out
}

func hasJSONExt(name string) bool {
	return len(name) > len(".json") && path.Ext(name) == ".json"
}

func trimJSONExt(name string) string {
	return name[:len(name)-len(".json")]
}
