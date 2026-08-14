package audit

import (
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
// clusterWorks and clustersOf both come from detectWorkDup, which is why this
// detector runs after it: the inverse index is built there, where the clusters are
// emitted, rather than rebuilt here from the forward one.
func detectRefSidecar(ix *index, clusterWorks, clustersOf map[string][]string) *findings {
	f := &findings{class: ClassRefSidecar}

	for _, id := range ix.sidecarWorkIDs() {
		members := "members: " + strings.Join(ix.sidecars[id], ", ")
		w := ix.workByID[id]
		if w == nil {
			f.add(Finding{
				Subclass: sidecarOrphanWork,
				Key:      id,
				Propose: Proposal{
					Op: OpRepointSidecar, Target: id, Field: "work", From: id,
					Advisory: true,
					Reason: "restore the work or remove the sidecar: a works-community entry keyed by a slug no work holds is " +
						"unreachable through the API, which composes characters and recaps onto GET /works/{id}",
				},
				Notes: []string{members},
			})
			continue
		}
		clusters := sortedUnique(clustersOf[id])
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
		siblings = sortedUnique(siblings)

		fd := Finding{
			Subclass: sidecarInDupCluster,
			Key:      id,
			Works:    []WorkRef{ix.workBrief(w)},
			Propose: Proposal{
				Op: OpRepointSidecar, Target: id, Others: siblings,
				Advisory: true,
				Reason: "resolve the duplicate BEFORE touching the sidecar, and re-point it by hand: which work a spoiler-gated " +
					"description belongs to is not a mechanical decision",
			},
			Notes: []string{
				members,
				"duplicate cluster(s): " + truncateList(clusters, 4),
				"other works in those clusters: " + truncateList(siblings, 8),
			},
		}
		var alsoSidecar []string
		for _, other := range siblings {
			if ow := ix.workByID[other]; ow != nil {
				fd.Works = append(fd.Works, ix.workBrief(ow))
			}
			// A sibling that carries a sidecar of its own is the worst case: two
			// spoiler layers for one book, written independently.
			if ix.hasSidecar(other) {
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
