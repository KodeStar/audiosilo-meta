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
	// DELIBERATELY narrower than resolveWorkKey (worksdb.go): a retired slug is
	// refused here rather than resolved through the tombstone table, because
	// attaching a recording asserts "this production IS that book" - a claim a
	// mechanical re-key must not make on the contributor's behalf. A sidecar key
	// is only an address; a recording is a fact about the work.
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
	publishers := c.parsePublishers(s.get(fRecPublishers), s.get(fRecPublisher))
	if c.failed() {
		return
	}

	narratorSlugs := c.slugsFor(narratorNames, sourceRef)
	if c.failed() {
		return
	}

	// A recording with the same narrator set already present is a duplicate -
	// unless it is still a bulk-mirror seed, in which case this submission is the
	// first person to attest that narration and a maintainer applies it over the
	// seed (see dedupIdentifiers).
	want := importer.ToSet(narratorSlugs)
	for _, r := range work.Recordings {
		if importer.SameSet(importer.ToSet(r.Narrators), want) {
			ref := recRef{Work: workSlug, Rec: r.ID}
			c.failDuplicate(ref, "a recording with these narrators already exists at %s", c.recLocation(ref))
			return
		}
	}

	c.emitRecording(workSlug, work.Language, narratorSlugs, asins, isbns, publishers, s, sourceRef)
}
