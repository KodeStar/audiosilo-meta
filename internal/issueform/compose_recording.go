package issueform

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
	for _, a := range asins {
		if p, ok := c.asinRec[a.ASIN]; ok {
			c.fail(StatusDuplicate, "ASIN %s already exists (duplicate of %s)", a.ASIN, "data/"+p)
			return
		}
	}
	for _, isbn := range isbns {
		if p, ok := c.isbnRec[isbn]; ok {
			c.fail(StatusDuplicate, "ISBN %s already exists (duplicate of %s)", isbn, "data/"+p)
			return
		}
	}

	narratorSlugs := c.slugsFor(narratorNames, sourceRef)
	if c.status == StatusInvalid {
		return
	}

	// A recording with the same narrator set already present is a duplicate.
	want := narratorSet(narratorSlugs)
	for _, r := range work.Recordings {
		if sameStringSet(narratorSet(r.Narrators), want) {
			c.fail(StatusDuplicate, "a recording with these narrators already exists at %s", "data/"+recordingPath(workSlug, r.ID))
			return
		}
	}

	c.emitRecording(workSlug, work.Language, narratorSlugs, asins, isbns, s, sourceRef)
}

func narratorSet(ns []string) map[string]bool {
	m := make(map[string]bool, len(ns))
	for _, n := range ns {
		m[n] = true
	}
	return m
}

func sameStringSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}
