package issueform

import (
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// Field labels for add-characters.yml / add-recaps.yml.
const (
	fSidecarCharactersFile = "The characters.json file"
	fSidecarRecapsFile     = "The recaps.json file"
	fSidecarLicense        = "License" // the CC BY-SA confirmation checkbox
)

// sidecarMembers names the works-community entry member each sidecar kind
// occupies, alongside the file name the issue form talks about.
var sidecarMembers = map[model.Kind]struct{ member, fileName string }{
	model.KindCharacters: {member: "characters", fileName: "characters.json"},
	model.KindRecaps:     {member: "recaps", fileName: "recaps.json"},
}

// addSidecar places a community CC BY-SA sidecar (characters or recaps) for an
// existing work from an attached/pasted JSON file. Both sidecars share ONE
// works-community entry keyed by the work slug, so the write is a
// read-modify-write: adding recaps to a work that already has characters
// preserves the characters member. It refuses to silently overwrite the member
// it is placing (that needs a human) and leans on the schema validation in the
// post-write metacheck to enforce the license layer, length caps, and spoiler
// positions.
func (c *composer) addSidecar(s sections, kind model.Kind) {
	if !s.checked(fSidecarLicense) {
		c.fail(StatusInvalid, "the "+licenseBySALabel+" license checkbox is not ticked")
		return
	}

	workSlug, ok := resolveWorkRef(s.get(fWorkRef))
	if !ok {
		c.fail(StatusInvalid, "could not read a work slug from %q", s.get(fWorkRef))
		return
	}
	// The submitted reference names a book; resolveWorkKey turns it into the slug
	// this entry is KEYED by, which is not always the same string - a slug a core
	// merge has retired composes under its survivor. Everything below (the
	// read-modify-write, the work backref, the entry key) uses the resolved one,
	// so a sidecar is never written at a tombstoned address.
	workSlug, ok = c.resolveWorkKey(workSlug)
	if !ok {
		return
	}

	names := sidecarMembers[kind]
	attachLabel := fSidecarCharactersFile
	if kind == model.KindRecaps {
		attachLabel = fSidecarRecapsFile
	}

	entry, found, ok := c.entryRaw(pack.FamilyWorksCommunity, workSlug)
	if !ok {
		return
	}
	if !found {
		entry = map[string]any{}
	} else if _, taken := entry[names.member]; taken {
		// Only this member blocks: the sibling sidecar sharing the entry is
		// exactly what the read-modify-write below preserves.
		c.fail(StatusNeedsHuman, "a %s sidecar already exists at %s; replacing it needs a maintainer",
			names.fileName, c.entryLocation(pack.FamilyWorksCommunity, workSlug, "")+": "+names.member)
		return
	}

	raw, ok := c.attachmentBytes(s.get(attachLabel))
	if !ok {
		return
	}

	// Validate it is well-formed JSON and normalize the work backref so the
	// sidecar's entry-key invariant holds regardless of what the file claimed.
	obj, err := pack.DecodeEntry(raw)
	if err != nil {
		c.fail(StatusInvalid, "the attached %s is not valid JSON: %v", names.fileName, err)
		return
	}
	obj["work"] = workSlug

	entry[names.member] = obj
	c.putEntry(pack.FamilyWorksCommunity, workSlug, entry)
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
