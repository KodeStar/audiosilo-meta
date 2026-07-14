package issueform

import "github.com/kodestar/audiosilo-meta/internal/importer"

// Field labels unique to add-recording.yml (the rec_* fields and Sources/CC0 are
// shared with add-work). Mirrors .github/ISSUE_TEMPLATE/add-recording.yml.
const fWorkRef = "The work"

// addRecording composes a new recording for an existing work.
func (c *composer) addRecording(s sections) {
	if !s.checked(fCC0) {
		c.fail(StatusInvalid, "the CC0 public-domain dedication checkbox is not ticked")
		return
	}

	ref := s.get(fWorkRef)
	if ref == "" {
		c.fail(StatusInvalid, "The work reference is required")
		return
	}
	workSlug, ok := resolveWorkRef(ref)
	if !ok {
		c.fail(StatusInvalid, "could not read a work slug from %q", ref)
		return
	}
	work, exists := c.works[workSlug]
	if !exists {
		c.fail(StatusNeedsHuman, "work %q was not found; submit an Add a work form first (or a maintainer will link the correct work)", workSlug)
		return
	}

	narratorNames := splitNames(s.get(fRecNarrators))
	if len(narratorNames) == 0 {
		c.fail(StatusInvalid, "at least one Narrator is required")
		return
	}
	sourceRef := s.get(fSources)
	if sourceRef == "" {
		c.fail(StatusInvalid, "Sources is required for provenance")
		return
	}

	asins := c.parseASINs(s.get(fRecASINs))
	isbns := c.parseISBNs(s.get(fRecISBNs))
	if c.dedupIdentifiers(asins, isbns, "") {
		return
	}

	narratorSlugs := c.slugsFor(narratorNames, sourceRef)
	if c.status == StatusInvalid {
		return
	}

	// A recording with the same narrator set already present is a duplicate.
	want := importer.ToSet(narratorSlugs)
	for _, r := range work.Recordings {
		if importer.SameSet(importer.ToSet(r.Narrators), want) {
			c.fail(StatusDuplicate, "a recording with these narrators already exists at %s", "data/"+recordingPath(workSlug, r.ID))
			return
		}
	}

	c.emitRecording(workSlug, work.Language, narratorSlugs, asins, isbns, s, sourceRef)
}
