package issueform

import (
	"bytes"
	"encoding/json"

	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// Field labels for add-characters.yml / add-recaps.yml.
const (
	fSidecarCharactersFile = "The characters.json file"
	fSidecarRecapsFile     = "The recaps.json file"
	fSidecarLicense        = "License" // the CC BY-SA confirmation checkbox
)

// sidecarNames is one sidecar kind's works-community member name and the file
// name the issue form talks about - named once, shared by the table below and
// failMemberTaken's signature.
type sidecarNames struct{ member, fileName string }

// sidecarMembers names the works-community entry member each sidecar kind
// occupies, alongside the file name the issue form talks about.
var sidecarMembers = map[model.Kind]sidecarNames{
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
	// summary is an existing recaps member holding ONLY a whole-book summary
	// (in_short/ending, no chaptered list), which a submitted recap list fills
	// rather than replaces (mergeIntoSummary).
	var summary map[string]any
	if !found {
		entry = map[string]any{}
	} else if member, taken := entry[names.member]; taken {
		// Only this member blocks: the sibling sidecar sharing the entry is
		// exactly what the read-modify-write below preserves.
		if summary = summaryOnly(kind, member); summary == nil {
			c.failMemberTaken(names, workSlug)
			return
		}
	}
	// AND under every RETIRED spelling of this work, which is the state a core
	// merge actually leaves behind: the merge retires the work slug in one
	// repository, and the community entry keeps its old key until the re-key sweep
	// lands in the other. Probing only the survivor sees no member and composes a
	// COMPETING one of the same kind for the same book - two characters members
	// that the release build's collision rule then refuses, or worse, that a
	// maintainer folds by hand having lost which was which. Which entry describes
	// the work is a maintainer's call (internal/repair's sidecar-member-collision
	// principle), so this is needs-human rather than a merge.
	aliases, ok := c.retiredKeysFor(workSlug)
	if !ok {
		return
	}
	for _, alias := range aliases {
		// entryRaw hands back a nil map when the entry is absent, so the member
		// probe alone decides - same shape as the survivor's probe above.
		old, _, ok := c.entryRaw(pack.FamilyWorksCommunity, alias)
		if !ok {
			return
		}
		if _, taken := old[names.member]; taken {
			c.failMemberTaken(names, alias)
			return
		}
	}

	raw, ok := c.attachmentBytes(s.raw(attachLabel), maxAttachmentBytes)
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
	if summary != nil {
		obj = c.mergeIntoSummary(summary, obj)
	}

	entry[names.member] = obj
	c.putEntry(pack.FamilyWorksCommunity, workSlug, entry)
}

// failMemberTaken is the verdict for a work that already carries a sidecar of the
// kind being submitted, at whichever KEY it is stored under - the survivor's, or
// the retired spelling a core merge left it at. One message for both, because the
// answer is the same either way and the location it names is what tells a
// maintainer which of the two they are looking at.
func (c *composer) failMemberTaken(names sidecarNames, at string) {
	c.fail(StatusNeedsHuman, "a %s sidecar already exists at %s; replacing it needs a maintainer",
		names.fileName, c.entryLocation(pack.FamilyWorksCommunity, at, "")+": "+names.member)
}

// attachmentBytes resolves a sidecar/import attachment field to bytes: an
// uploaded file URL is fetched (HTTPS + host-pinned + capped at maxBytes), or
// pasted JSON is used inline - GitHub's own limit on an issue body bounds that.
// It sets a terminal status and reports ok=false on any failure.
func (c *composer) attachmentBytes(block string, maxBytes int64) ([]byte, bool) {
	url, inline, ok := extractAttachment(block)
	if !ok {
		c.fail(StatusInvalid, "no attached file or pasted JSON found")
		return nil, false
	}
	if inline != nil {
		return inline, true
	}
	data, err := c.fetch(url, maxBytes)
	if err != nil {
		c.fail(StatusInvalid, "could not fetch the attached file: %v", err)
		return nil, false
	}
	return data, true
}

// recapSummaryFields are the recaps member's two whole-book summary fields.
var recapSummaryFields = []string{"in_short", "ending"}

// summaryOnly returns the existing member as a map when it is a RECAPS member
// holding a whole-book summary and no chaptered recap list, else nil. That
// member is not a sidecar the submission would replace: the list it lacks is
// exactly what a recaps submission brings, so the two are merged.
func summaryOnly(kind model.Kind, member any) map[string]any {
	if kind != model.KindRecaps {
		return nil
	}
	m, ok := member.(map[string]any)
	if !ok {
		return nil
	}
	if v, present := m["recaps"]; present {
		if list, isList := v.([]any); !isList || len(list) > 0 {
			return nil
		}
	}
	for _, f := range recapSummaryFields {
		if _, ok := m[f]; ok {
			return m
		}
	}
	return nil
}

// mergeIntoSummary fills a summary-only recaps member with a submitted recap
// list. The community's summary is KEPT: a submission's own in_short/ending is
// dropped when the member already states that field (an AudioSilo Bridge strips
// them before sending in that case; a hand submission that still carries a
// different one is told so). Sources are unioned, existing first, because the
// summary's attribution is a CC BY-SA obligation the merge must not lose.
func (c *composer) mergeIntoSummary(existing, submitted map[string]any) map[string]any {
	for _, f := range recapSummaryFields {
		old, ok := existing[f]
		if !ok {
			continue
		}
		if mine, sent := submitted[f]; sent && !sameJSON(mine, old) {
			c.note("kept the community's existing %s for this work; the submitted one was not used", f)
		}
		submitted[f] = old
	}
	oldSources, _ := existing["sources"].([]any)
	newSources, _ := submitted["sources"].([]any)
	merged := append([]any{}, oldSources...)
	for _, src := range newSources {
		dup := false
		for _, have := range merged {
			if sameJSON(src, have) {
				dup = true
				break
			}
		}
		if !dup {
			merged = append(merged, src)
		}
	}
	if len(merged) > 0 {
		submitted["sources"] = merged
	}
	c.note("added the recap list to the work's existing summary-only recaps, keeping its summary")
	return submitted
}

// sameJSON compares two decoded JSON values by their encoding.
func sameJSON(a, b any) bool {
	x, errA := json.Marshal(a)
	y, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(x, y)
}
