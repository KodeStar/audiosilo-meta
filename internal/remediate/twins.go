package remediate

import (
	"sort"
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

// cohortWork is one work in the post-merge view the twin pass reads: either a
// work this run composed or a dramatized cohort work it left alone.
type cohortWork struct {
	work obj
}

// planTwins finds cohort works sharing an ASIN and folds each cluster into one.
// It returns the works it changed, the ones it removed, and the rewrites a
// series repair has to apply.
func planTwins(view map[string]cohortWork) (changed map[string]obj, merges []twinMerge, refusals []Refusal) {
	changed = map[string]obj{}
	byASIN := map[string][]string{}
	for _, slug := range sortedKeys(view) {
		_, rec, ok := soleRecording(view[slug].work)
		if !ok {
			continue
		}
		for _, a := range rec.asins() {
			byASIN[a.ASIN] = append(byASIN[a.ASIN], slug)
		}
	}

	// Union the colliding slugs into clusters, so three works sharing two
	// identifiers are one merge rather than two conflicting ones.
	parent := map[string]string{}
	var find func(string) string
	find = func(s string) string {
		p, ok := parent[s]
		if !ok || p == s {
			return s
		}
		r := find(p)
		parent[s] = r
		return r
	}
	union := func(a, b string) {
		ra, rb := find(a), find(b)
		if ra == rb {
			return
		}
		if rb < ra {
			ra, rb = rb, ra
		}
		parent[rb] = ra
	}
	var shared []string
	for _, asin := range sortedKeys(byASIN) {
		slugs := byASIN[asin]
		if len(slugs) < 2 {
			continue
		}
		for _, s := range slugs {
			if _, ok := parent[s]; !ok {
				parent[s] = s
			}
		}
		for i := 1; i < len(slugs); i++ {
			union(slugs[0], slugs[i])
		}
		shared = append(shared, asin)
	}
	// The identifier a cluster is REPORTED by, resolved after the unions: a
	// root recorded while clustering can stop being the root one union later.
	witness := map[string]string{}
	for _, asin := range shared {
		root := find(byASIN[asin][0])
		if _, ok := witness[root]; !ok {
			witness[root] = asin
		}
	}

	clusters := map[string][]string{}
	for _, s := range sortedKeys(parent) {
		r := find(s)
		clusters[r] = append(clusters[r], s)
	}

	for _, root := range sortedKeys(clusters) {
		members := clusters[root]
		sort.Strings(members)
		if len(members) < 2 {
			continue
		}
		if r, ok := twinsAgree(view, members); !ok {
			refusals = append(refusals, r)
			continue
		}
		survivor := preferredTwin(members)
		dst := view[survivor].work.clone()
		var absorbed []string
		for _, m := range members {
			if m == survivor {
				continue
			}
			if err := foldWork(dst, view[m].work, survivor); err != nil {
				refusals = append(refusals, Refusal{Category: catTwinDisagreement, Subject: survivor,
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
		merges = append(merges, twinMerge{Survivor: survivor, Absorbed: absorbed, ASIN: witness[root]})
	}
	return changed, merges, refusals
}

// twinsAgree checks that a cluster really is one product recorded twice.
func twinsAgree(view map[string]cohortWork, members []string) (Refusal, bool) {
	var authors string
	for i, m := range members {
		w := view[m].work
		if !isGraphicAudio(w) || !isDramatized(w.str("title")) {
			return Refusal{Category: catTwinDisagreement, Subject: m,
				Reason:  "works share an ASIN but are not both GraphicAudio dramatizations",
				Entries: members}, false
		}
		if _, _, ok := soleRecording(w); !ok {
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

// preferredTwin picks the surviving slug: the shortest, ties broken
// lexicographically. The shortest is the one without the series-name tail a
// second import welded on ("dawnshard-dramatized-adaptation" over
// "dawnshard-dramatized-adaptation-the-stormlight-archive").
func preferredTwin(members []string) string {
	best := members[0]
	for _, m := range members[1:] {
		if len(m) < len(best) || (len(m) == len(best) && m < best) {
			best = m
		}
	}
	return best
}

// foldWork folds src's facts into dst, which keeps its own identity and wins
// every scalar it states.
func foldWork(dst, src obj, dstSlug string) error {
	if err := dst.set("genres", unionGenres(dst.strs("genres"), src.strs("genres"))); err != nil {
		return err
	}
	if len(dst.strs("genres")) == 0 {
		dst.drop("genres")
	}
	setOrDropCredits(dst, unionCredits(dst.credits(), src.credits()))
	if err := dst.set("sources", unionSources(dst.sources(), src.sources())); err != nil {
		return err
	}
	setStringOrDrop(dst, "added_at", earlierStamp(dst.str("added_at"), src.str("added_at")))
	fillXref(dst, src)

	dstKey, dstRec, ok := soleRecording(dst)
	if !ok {
		return errNoSoleRecording
	}
	_, srcRec, ok := soleRecording(src)
	if !ok {
		return errNoSoleRecording
	}
	rec := dstRec.clone()
	if err := rec.set("work", dstSlug); err != nil {
		return err
	}
	setStringsOrDrop(rec, "narrators", appendUnique(rec.strs("narrators"), srcRec.strs("narrators")))
	setASINsOrDrop(rec, unionASINs(rec.asins(), srcRec.asins()))
	setISBNsOrDrop(rec, unionISBNs(rec.isbns(), srcRec.isbns()))
	if err := rec.set("sources", unionSources(rec.sources(), srcRec.sources())); err != nil {
		return err
	}
	setStringOrDrop(rec, "release_date", earlierDate(rec.str("release_date"), srcRec.str("release_date")))
	setStringOrDrop(rec, "added_at", earlierStamp(rec.str("added_at"), srcRec.str("added_at")))
	for _, k := range []string{"cover_url", "publisher"} {
		if rec.str(k) == "" {
			setStringOrDrop(rec, k, srcRec.str(k))
		}
	}
	if !rec.has("runtime_min") {
		if v, ok := srcRec.intAt("runtime_min"); ok {
			if err := rec.set("runtime_min", v); err != nil {
				return err
			}
		}
	}
	if !rec.has("chapters") {
		if raw, ok := srcRec["chapters"]; ok {
			rec.setRaw("chapters", raw)
		}
	}
	if !rec.has("abridged") {
		if raw, ok := srcRec["abridged"]; ok {
			rec.setRaw("abridged", raw)
		}
	}
	return dst.setRecordings(map[string]obj{dstKey: rec})
}
