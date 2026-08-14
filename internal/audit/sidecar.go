package audit

import (
	"sort"
	"strings"
)

// REF-SIDECAR subclasses.
const (
	sidecarInDupCluster = "work-in-duplicate-cluster"
	sidecarOrphanWork   = "orphan-work"
)

// detectRefSidecar reports the works-community entries whose work reference is a
// hazard.
//
// The first subclass is the reason this class exists. A characters or recaps
// member is keyed by a WORK slug, and its whole contract is that the spoilers it
// carries are bounded by which book the listener is in. If that work turns out to
// be one of two records for one book, the sidecar is attached to whichever of them
// the authoring pass happened to see - so a consumer reading the OTHER record
// shows nothing, and a repair pass that merges the wrong direction moves a
// spoiler-gated description onto a different edition's timeline. Neither is
// something a mechanical rule may decide, so every one of these is flagged for a
// human before any W-DUP merge is applied.
//
// clusterWorks is the work-id set of each cluster W-DUP emitted, which is why this
// detector runs after it rather than beside it.
func detectRefSidecar(ix *index, clusterWorks map[string][]string) *findings {
	f := &findings{class: ClassRefSidecar}

	// The clusters each work belongs to, so a sidecar can name them.
	clustersOf := map[string][]string{}
	keys := make([]string, 0, len(clusterWorks))
	for k := range clusterWorks {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, id := range clusterWorks[k] {
			clustersOf[id] = append(clustersOf[id], k)
		}
	}

	// A sidecar work slug is reported once however many members its entry has.
	for _, id := range sortedUnique(sidecarWorkIDs(ix)) {
		w := ix.workByID[id]
		if w == nil {
			f.add(Finding{
				Subclass: sidecarOrphanWork,
				Key:      id,
				Field:    "work",
				Have:     id,
				Action: "restore the work or remove the sidecar: a works-community entry keyed by a slug no work holds is unreachable " +
					"through the API, which composes characters and recaps onto GET /works/{id}",
				Notes: []string{"members: " + strings.Join(sidecarMembers(ix, id), ", ")},
			})
			continue
		}
		clusters := clustersOf[id]
		if len(clusters) == 0 {
			continue
		}
		var siblings []string
		for _, k := range clusters {
			for _, other := range clusterWorks[k] {
				if other != id {
					siblings = append(siblings, other)
				}
			}
		}
		fd := Finding{
			Subclass: sidecarInDupCluster,
			Key:      id,
			Works:    []WorkRef{ix.workBrief(w)},
			Action: "resolve the duplicate BEFORE touching the sidecar, and re-point it by hand: which work a spoiler-gated " +
				"description belongs to is not a mechanical decision",
			Notes: []string{
				"members: " + strings.Join(sidecarMembers(ix, id), ", "),
				"duplicate cluster(s): " + truncateList(clusters, 4),
				"other works in those clusters: " + truncateList(sortedUnique(siblings), 8),
			},
		}
		for _, other := range sortedUnique(siblings) {
			if ow := ix.workByID[other]; ow != nil {
				fd.Works = append(fd.Works, ix.workBrief(ow))
			}
		}
		// A sibling that carries a sidecar of its own is the worst case: two
		// spoiler layers for one book, written independently.
		var alsoSidecar []string
		for _, other := range sortedUnique(siblings) {
			if ix.sidecarWorks[other] {
				alsoSidecar = append(alsoSidecar, other)
			}
		}
		if len(alsoSidecar) > 0 {
			fd.Notes = append(fd.Notes, "these cluster siblings ALSO carry a sidecar: "+truncateList(alsoSidecar, 8))
		}
		f.add(fd)
	}
	return f
}

// sidecarWorkIDs is every work slug the works-community family keys an entry by.
func sidecarWorkIDs(ix *index) []string {
	out := make([]string, 0, len(ix.sidecarWorks))
	for _, c := range ix.cat.Characters {
		out = append(out, c.Work)
	}
	for _, r := range ix.cat.Recaps {
		out = append(out, r.Work)
	}
	return out
}

// sidecarMembers names which of the two members a work's sidecar entry holds.
func sidecarMembers(ix *index, workID string) []string {
	var out []string
	for _, c := range ix.cat.Characters {
		if c.Work == workID {
			out = append(out, "characters")
			break
		}
	}
	for _, r := range ix.cat.Recaps {
		if r.Work == workID {
			out = append(out, "recaps")
			break
		}
	}
	return out
}
