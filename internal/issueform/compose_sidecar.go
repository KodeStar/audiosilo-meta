package issueform

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// Field labels for add-characters.yml / add-recaps.yml.
const (
	fSidecarCharactersFile = "The characters.json file"
	fSidecarRecapsFile     = "The recaps.json file"
	fSidecarLicense        = "License" // the CC BY-SA confirmation checkbox
)

// addSidecar places a community CC BY-SA sidecar (characters.json or
// recaps.json) for an existing work from an attached/pasted JSON file. It
// refuses to silently overwrite an existing sidecar (that needs a human) and
// leans on the schema validation in the post-write metacheck to enforce the
// license layer, length caps, and spoiler positions.
func (c *composer) addSidecar(s sections, kind model.Kind) {
	if !s.checked(fSidecarLicense) {
		c.fail(StatusInvalid, "the CC BY-SA 3.0 license checkbox is not ticked")
		return
	}

	workSlug, ok := resolveWorkRef(s.get(fWorkRef))
	if !ok {
		c.fail(StatusInvalid, "could not read a work slug from %q", s.get(fWorkRef))
		return
	}
	if _, exists := c.works[workSlug]; !exists {
		c.fail(StatusNeedsHuman, "work %q was not found; the sidecar's work must already be in the database", workSlug)
		return
	}

	fileName := "characters.json"
	attachLabel := fSidecarCharactersFile
	if kind == model.KindRecaps {
		fileName = "recaps.json"
		attachLabel = fSidecarRecapsFile
	}
	rel := path.Join("works", model.Shard(workSlug), workSlug, fileName)

	// Refuse to silently overwrite an existing sidecar.
	if _, err := os.Stat(filepath.Join(c.dataDir, filepath.FromSlash(rel))); err == nil {
		c.fail(StatusNeedsHuman, "a %s sidecar already exists at %s; replacing it needs a maintainer", fileName, "data/"+rel)
		return
	}

	raw, ok := c.attachmentBytes(s.get(attachLabel))
	if !ok {
		return
	}

	// Validate it is well-formed JSON and normalize the work backref so the
	// sidecar's parent-dir invariant holds regardless of what the file claimed.
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		c.fail(StatusInvalid, "the attached %s is not valid JSON: %v", fileName, err)
		return
	}
	obj["work"] = workSlug

	c.writeRaw(rel, obj)
}

// attachmentBytes resolves a sidecar/import attachment field to bytes: an
// uploaded file URL is fetched (HTTPS + host-pinned + size-capped), or pasted
// JSON is used inline. It sets a terminal status and reports ok=false on any
// failure.
func (c *composer) attachmentBytes(block string) ([]byte, bool) {
	url, inline, ok := extractAttachment(block)
	if !ok {
		c.fail(StatusInvalid, "no attached file or pasted JSON found")
		return nil, false
	}
	if inline != nil {
		return inline, true
	}
	data, err := c.fetch(url)
	if err != nil {
		c.fail(StatusInvalid, "could not fetch the attached file: %v", err)
		return nil, false
	}
	return data, true
}
