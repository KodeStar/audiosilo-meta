package check

// compose.go is the load over TWO data roots.
//
// The CC BY-SA community layer is moving to a repository of its own, so the
// database becomes a PAIR of trees: a core root (works, people, series, plus the
// slug tombstone table) and a community root (works-community alone). Each is
// checkable standalone through LoadProfile, and each deliberately stands down
// from the rules whose other side it cannot see - a works-community entry is
// keyed by a works-family slug, and a tree without the works cannot answer
// "does that work exist" (see LoadProfile's cross-family skip rule).
//
// This file is where those rules DO run: the release build, over both checkouts,
// which is the only place that holds the whole database at once. It is also the
// only place that can honestly resolve a RETIRED key, because the tombstone
// table lives in the core tree while the sidecar keyed by the slug it retires
// lives in the other one.

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// LoadComposed validates a core root and a community root and returns them as
// ONE result: the core catalogue carrying the community tree's sidecars, with
// every rule that needs both sides run over the pair. It is what the release
// build compiles its artifact from once the two trees live in two repositories
// (cmd/metabuild --community); a single root holding the whole database is still
// Load, byte for byte.
//
// The profiles are not the caller's to choose: coreDir is read as
// pack.ProfileCore and communityDir as pack.ProfileCommunity, because a compose
// of anything else is not a thing - two roots each holding works would be two
// databases, not one. That is also what makes a misdirected directory loud
// rather than silently empty: a community checkout handed as coreDir has no
// works root and a works-community root that is not in its profile, so every
// file in it is an unrecognized location by the accounting rule that was already
// total.
//
// THE CROSS-TREE RULES, and they are the whole reason this exists:
//
//   - EXISTENCE, a hard error. Every community entry must be keyed by a work the
//     core tree holds. A key that is neither live nor retired is a red release,
//     never a silently dropped sidecar - the CC BY-SA layer is the most expensive
//     data in the project and a build that quietly omits one is worse than a
//     build that stops.
//   - REDIRECT RESOLUTION. A key naming a slug the core tree has RETIRED
//     (data/redirects.json) is re-keyed onto the surviving slug for this build.
//     A core repair wave that merges two works lands in one repository and the
//     community re-key sweep lands in the other, so the two cannot be atomic;
//     resolving here is what keeps the window between them from losing a
//     sidecar. It is a build-time resolution only - nothing is written, and the
//     sweep is still the fix.
//   - COLLISION, a hard error. Two sidecars of the SAME KIND resolving onto one
//     work (a redirect landing on a work that already has one) is refused rather
//     than folded: which entry describes the surviving work is a human decision,
//     the same principle internal/repair's sidecar-member-collision refusal
//     rests on. DISJOINT members - a characters sidecar for the retired slug
//     beside a recaps sidecar for the survivor - simply meet on one work, which
//     is exactly what a composed entry is.
//   - THE POSITION-SCALE ADVISORY. checkSidecarPositionScale reads a work's
//     recordings to judge whether a sidecar's chapter positions are scaled to
//     something else. It is vacuous over a community root alone (no works, so no
//     floor to measure against) and runs here for real. It stays a WARNING: a
//     sidecar that genuinely covers only the opening of a long book is a
//     legitimate partial contribution, and a release must not turn on it.
//
// A RED SIDE STOPS THE CROSS-TREE PASS. If either root has problems of its own,
// they are returned and none of the above runs: a core tree that failed to load
// may be missing the very works the community keys name, and answering "3,000
// dangling sidecars" to "one pack file is malformed" would bury the fix. The
// catalogue is still composed and handed back, best-effort, exactly as a failed
// single-root Load hands back what it managed to read - but its sidecar keys are
// then as written, unresolved.
func LoadComposed(coreDir, communityDir string) Result {
	var listProbs []Problem
	coreLst, err := pack.ListProfile(coreDir, pack.ProfileCore)
	if err != nil {
		listProbs = append(listProbs, Problem{Path: coreDir, Msg: err.Error()})
	}
	comLst, err := pack.ListProfile(communityDir, pack.ProfileCommunity)
	if err != nil {
		listProbs = append(listProbs, Problem{Path: communityDir, Msg: err.Error()})
	}
	// Both roots are reported, not just the first: an operator pointing the build
	// at the wrong pair of checkouts should learn about both in one run.
	if len(listProbs) > 0 {
		sortProblems(listProbs)
		return Result{Problems: listProbs}
	}

	core, coreIdx := load(coreLst, nil)
	com, comIdx := load(comLst, nil)

	res := Result{
		Problems: concatProblems(core.Problems, com.Problems),
		Warnings: concatProblems(core.Warnings, com.Warnings),
		Catalog:  core.Catalog,
		Identity: core.Identity,
	}
	// A load that failed before a catalogue existed (a schema compile, an
	// unreadable family root) has nothing to compose; it has already reported why.
	if res.Catalog == nil || com.Catalog == nil {
		sortProblems(res.Problems)
		sortProblems(res.Warnings)
		return res
	}

	// THE COMPOSE. The core catalogue is the whole database except the sidecars,
	// and the community catalogue is the sidecars and nothing else - the two
	// profiles are disjoint by construction, so this is an assignment rather than
	// a merge, and the order within each list is the community listing's, which is
	// the order a single ProfileAll tree would have produced from the same files.
	res.Catalog.Characters = com.Catalog.Characters
	res.Catalog.Recaps = com.Catalog.Recaps

	if len(res.Problems) == 0 {
		// One index over both roots. A record is decoded by exactly one load, so
		// the two indexes are disjoint and merging them is a copy (see
		// pathIndex.merge).
		idx := coreIdx
		idx.merge(comIdx)
		resolveSidecarKeys(res.Catalog, idx, appendTo(&res.Problems))
		checkSidecarPositionScale(res.Catalog, idx, appendTo(&res.Warnings))
	}

	sortProblems(res.Problems)
	sortProblems(res.Warnings)
	return res
}

// concatProblems joins two loads' lists into a new slice. Explicitly a copy: the
// two inputs are each a load's own output and appending onto one of them in
// place would hand a caller a slice whose backing array another result also
// points into.
func concatProblems(a, b []Problem) []Problem {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := make([]Problem, 0, len(a)+len(b))
	out = append(out, a...)
	return append(out, b...)
}

// appendTo is the addFunc the cross-tree rules accumulate through. They are the
// loader's rules run after the loader is gone, so there is no l.add to take a
// method value of; the list they append to is the composed result's own.
func appendTo(dst *[]Problem) addFunc {
	return func(path, format string, args ...any) {
		*dst = append(*dst, Problem{Path: path, Msg: fmt.Sprintf(format, args...)})
	}
}

// sidecarRef is one community entry's member, as the resolution sees it: which
// kind it is, where it was read from, and a pointer to the work slug it is keyed
// by - the pointer, because resolving a retired key REWRITES that field, which
// is what makes the rest of the build (metabuild's work_id columns, the
// uniqueness the collision rule enforces) see the surviving slug.
type sidecarRef struct {
	kind string
	path string
	work *string
}

// sidecarRefs lists both member kinds as one sequence, in catalogue order.
// Characters first, then recaps, which is only a reporting order: every rule
// below groups or sorts before it says anything.
func sidecarRefs(cat *model.Catalog, idx *pathIndex) []sidecarRef {
	refs := make([]sidecarRef, 0, len(cat.Characters)+len(cat.Recaps))
	for _, c := range cat.Characters {
		refs = append(refs, sidecarRef{kind: "characters", path: idx.characters[c], work: &c.Work})
	}
	for _, rc := range cat.Recaps {
		refs = append(refs, sidecarRef{kind: "recaps", path: idx.recaps[rc], work: &rc.Work})
	}
	return refs
}

// resolveSidecarKeys is the EXISTENCE rule and the redirect resolution in one
// pass, because they are one question asked of one key: which live work does
// this sidecar describe?
//
// It is checkIntegrity's sidecar arm - the arm LoadProfile stands down over a
// community root - plus the tombstone hop that only a composed pair can take,
// plus the collision the hop can create. Written here rather than added to that
// arm because it is not the same rule: a single tree never re-keys anything, and
// a dangling key there is a defect of one tree rather than a disagreement
// between two.
func resolveSidecarKeys(cat *model.Catalog, idx *pathIndex, add addFunc) {
	live := make(map[string]bool, len(cat.Works))
	for _, w := range cat.Works {
		live[w.ID] = true
	}
	tomb := cat.Redirects[model.RedirectWorks]

	// Resolution and collision detection are TWO passes on purpose: a group is
	// judged by what its members resolved to, so which entry gets reported cannot
	// depend on which one the walk reached first. The group key is (kind, target),
	// joined with a NUL - neither a member name nor a slug can contain one, so the
	// join is injective and sorting the keys IS (kind, target) order.
	type resolved struct {
		sidecarRef
		from string
	}
	byTarget := map[string][]resolved{}

	for _, ref := range sidecarRefs(cat, idx) {
		from := *ref.work
		to, retired := tomb[from]
		var target string
		switch {
		case live[from]:
			target = from
		case retired && live[to]:
			target = to
			*ref.work = to
		default:
			// The retired-but-dead-target arm is unreachable over a green core -
			// checkRedirects refuses a target that is not a live id - and shares this
			// message rather than an unreachable one of its own.
			add(ref.path, "%s sidecar is keyed by work %q, which the core tree does not hold%s: "+
				"re-key the entry to a live work slug or withdraw it",
				ref.kind, from, danglingRedirectNote(retired, to))
			continue
		}
		key := ref.kind + "\x00" + target
		byTarget[key] = append(byTarget[key], resolved{sidecarRef: ref, from: from})
	}

	for _, key := range slices.Sorted(maps.Keys(byTarget)) {
		group := byTarget[key]
		if len(group) < 2 {
			continue
		}
		slices.SortFunc(group, func(a, b resolved) int { return strings.Compare(a.path, b.path) })
		// A collision needs a redirect to exist at all: two sidecars of one kind
		// keyed by the SAME live slug are the community tree's own duplicate, which
		// checkSidecarUniqueness has already reported there. The first member by
		// path is the incumbent every other one is reported against, so the report
		// is one line per surplus entry rather than a set with no anchor.
		keeper := group[0]
		for _, r := range group[1:] {
			add(r.path, "%s sidecar keyed by work %q resolves onto %q, which already has one (%s): "+
				"which of the two describes the surviving work is a human decision - fold them into one entry",
				r.kind, r.from, *r.work, keeper.path)
		}
	}
}

// danglingRedirectNote states, inside the existence message, that the key WAS
// retired but its target is not a live work either - the shape a core tree that
// validates cannot produce, kept because the alternative is an unreachable
// branch that says the same thing.
func danglingRedirectNote(retired bool, to string) string {
	if !retired {
		return ""
	}
	return " (the slug redirects to " + strconv.Quote(to) + ", which is not there either)"
}
