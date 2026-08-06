package remediate

import (
	"slices"
	"strings"
)

// twins.go merges same-ASIN twins WITHIN the dramatized cohort: two works that
// carry one identifier are one product, however they were titled.
//
// It is deliberately not a catalogue-wide ASIN dedupe. ASIN uniqueness is a
// metacheck rule the whole tree already satisfies, so a collision can only
// appear where this run has just unioned identifiers - and bounding the merge
// to the cohort keeps it from becoming an unrelated, unreviewed change to the
// rest of the catalogue.

// twinMerge is one twin collapse, for the report.
type twinMerge struct {
	Survivor string
	Absorbed []string
	ASIN     string
}

// planTwins finds cohort works sharing an ASIN and folds each cluster into one.
// It returns the works it changed and the merges it performed; the caller turns
// those into rewrites and deletions.
func planTwins(view map[string]obj) (changed map[string]obj, merges []twinMerge, refusals []Refusal) {
	changed = map[string]obj{}

	// Clusters, built in sorted order so the result never depends on map order:
	// each identifier joins the works that carry it, and a work already in a
	// cluster pulls its whole cluster in with it. Three works sharing two
	// identifiers are therefore ONE merge rather than two conflicting ones.
	clusterOf := map[string]int{} // work slug -> cluster index
	var members [][]string        // cluster index -> work slugs, in first-seen order
	var witness []string          // cluster index -> the identifier that formed it

	byASIN := map[string][]string{}
	for _, slug := range sortedKeys(view) {
		_, rec, ok := soleRecordingOf(view[slug])
		if !ok {
			continue
		}
		for _, a := range rec.asins() {
			byASIN[a.ASIN] = append(byASIN[a.ASIN], slug)
		}
	}
	for _, asin := range sortedKeys(byASIN) {
		slugs := byASIN[asin]
		if len(slugs) < 2 {
			continue
		}
		into := -1
		for _, s := range slugs {
			if c, ok := clusterOf[s]; ok {
				into = c
				break
			}
		}
		if into < 0 {
			into = len(members)
			members = append(members, nil)
			witness = append(witness, asin)
		}
		for _, s := range slugs {
			switch c, ok := clusterOf[s]; {
			case !ok:
				clusterOf[s] = into
				members[into] = append(members[into], s)
			case c != into:
				// Absorb the other cluster: one identifier has just tied them
				// together.
				for _, m := range members[c] {
					clusterOf[m] = into
					members[into] = append(members[into], m)
				}
				members[c] = nil
			}
		}
	}

	for i, cluster := range members {
		if len(cluster) < 2 {
			continue
		}
		slices.Sort(cluster)
		if r, ok := twinsAgree(view, cluster); !ok {
			refusals = append(refusals, r)
			continue
		}
		survivor := preferredTwin(cluster)
		dst := view[survivor].clone()
		var absorbed []string
		for _, m := range cluster {
			if m == survivor {
				continue
			}
			if err := foldWork(dst, view[m], survivor); err != nil {
				refusals = append(refusals, Refusal{Category: catInternal, Subject: survivor,
					Reason: "the twin could not be folded in: " + err.Error(), Entries: []string{m}})
				absorbed = nil
				break
			}
			absorbed = append(absorbed, m)
		}
		if len(absorbed) == 0 {
			continue
		}
		changed[survivor] = dst
		merges = append(merges, twinMerge{Survivor: survivor, Absorbed: absorbed, ASIN: witness[i]})
	}
	return changed, merges, refusals
}

// soleRecordingOf returns a work's one recording, for the post-merge view (a
// composed work is not in the scan index, so candidate.soleRecording cannot
// answer for it).
func soleRecordingOf(w obj) (key string, rec obj, ok bool) {
	recs, err := w.recordings()
	if err != nil || len(recs) != 1 {
		return "", nil, false
	}
	for k, v := range recs {
		return k, v, true
	}
	return "", nil, false
}

// twinsAgree checks that a cluster really is one product recorded twice.
func twinsAgree(view map[string]obj, members []string) (Refusal, bool) {
	var authors string
	for i, m := range members {
		w := view[m]
		if !isDramatized(w.str("title")) || !worksGraphicAudio(w) {
			return Refusal{Category: catTwinDisagreement, Subject: m,
				Reason:  "works share an ASIN but are not both GraphicAudio dramatizations",
				Entries: members}, false
		}
		if _, _, ok := soleRecordingOf(w); !ok {
			return Refusal{Category: catMultiRecording, Subject: m,
				Reason:  "works share an ASIN but one does not carry exactly one recording",
				Entries: members}, false
		}
		key := authorsKey(w.strs("authors"))
		if i == 0 {
			authors = key
			continue
		}
		if key != authors {
			return Refusal{Category: catTwinDisagreement, Subject: m,
				Reason:  "works share an ASIN but state different authors",
				Entries: members}, false
		}
	}
	return Refusal{}, true
}

// worksGraphicAudio reports whether any of a work's recordings names
// GraphicAudio as its publisher.
func worksGraphicAudio(w obj) bool {
	recs, err := w.recordings()
	if err != nil {
		return false
	}
	for _, rec := range recs {
		if graphicAudioPublishers[rec.str("publisher")] {
			return true
		}
	}
	return false
}

// preferredTwin picks the surviving slug: the shortest, ties broken
// lexicographically. The shortest is the one without the series-name tail a
// second import welded on ("dawnshard-dramatized-adaptation" over
// "dawnshard-dramatized-adaptation-the-stormlight-archive").
func preferredTwin(members []string) string {
	return slices.MinFunc(members, func(a, b string) int {
		if len(a) != len(b) {
			return len(a) - len(b)
		}
		return strings.Compare(a, b)
	})
}

// foldWork folds src's facts into dst, which keeps its own identity and wins
// every scalar it states.
func foldWork(dst, src obj, dstSlug string) error {
	setListOrDrop(dst, "genres", unionGenres(dst.strs("genres"), src.strs("genres")))
	setListOrDrop(dst, "credits", unionCredits(dst.credits(), src.credits()))
	dst.set("sources", unionSources(dst.sources(), src.sources()))
	setStringOrDrop(dst, "added_at", earlierStamp(dst.str("added_at"), src.str("added_at")))
	fillXref(dst, src)

	dstKey, dstRec, ok := soleRecordingOf(dst)
	if !ok {
		return errNoSoleRecording
	}
	_, srcRec, ok := soleRecordingOf(src)
	if !ok {
		return errNoSoleRecording
	}
	rec := dstRec.clone()
	rec.set("work", dstSlug)
	setListOrDrop(rec, "narrators", appendUnique(rec.strs("narrators"), srcRec.strs("narrators")))
	setListOrDrop(rec, "asin", unionASINs(rec.asins(), srcRec.asins()))
	setListOrDrop(rec, "isbn", unionISBNs(rec.isbns(), srcRec.isbns()))
	rec.set("sources", unionSources(rec.sources(), srcRec.sources()))
	setStringOrDrop(rec, "release_date", earlierDate(rec.str("release_date"), srcRec.str("release_date")))
	setStringOrDrop(rec, "added_at", earlierStamp(rec.str("added_at"), srcRec.str("added_at")))
	for _, k := range []string{"cover_url", "publisher"} {
		if rec.str(k) == "" {
			setStringOrDrop(rec, k, srcRec.str(k))
		}
	}
	for _, k := range []string{"runtime_min", "chapters", "abridged"} {
		if !rec.has(k) {
			if raw, ok := srcRec[k]; ok {
				rec.setRaw(k, raw)
			}
		}
	}
	return dst.setRecordings(map[string]obj{dstKey: rec})
}
