package model

import "regexp"

// slugPattern is the canonical slug regex used for every id across the dataset.
var slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// MaxSlugLen is the maximum allowed slug length.
const MaxSlugLen = 100

// ValidSlug reports whether s is a well-formed slug.
func ValidSlug(s string) bool {
	return len(s) <= MaxSlugLen && slugPattern.MatchString(s)
}

// PackLocation addresses one entity inside a pack file. A pack holds many
// entities, so a path alone does not identify one and the entry key has to
// travel with it - which is why the file-path addressing the file-per-entity
// tree used (Shard, ParseLocation) is gone rather than adapted. It is a plain
// addressing value with no bound math of its own - pkg/pack owns that - so
// every walker and rule can report against it without importing the storage
// layer.
type PackLocation struct {
	// Kind is the entity the entry holds.
	Kind Kind
	// Family is the pack family's root directory name ("works",
	// "works-community", "people", "series").
	Family string
	// Pack is the pack file's data-relative, slash-separated path.
	Pack string
	// Slug is the entry key: the work, person, or series slug. For a
	// characters/recaps sidecar and for a recording it is the parent work's
	// slug, since that is what the entry is keyed by.
	Slug string
	// RecSlug is the recording's key within its work entry's recordings map.
	// It is empty unless Kind is KindRecording.
	RecSlug string
}

// String renders the location the way a problem report names it:
// "works/0/0.json: entry harry-potter" or "... : entry harry-potter: recording
// stephen-fry".
func (l PackLocation) String() string {
	s := l.Pack + ": entry " + l.Slug
	if l.RecSlug != "" {
		s += ": recording " + l.RecSlug
	}
	return s
}
