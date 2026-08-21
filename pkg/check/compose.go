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
// This file is where those rules run over the pair. Two of them are the whole
// database's rules asked where the whole database is - checkIntegrity's sidecar
// arm and the position-scale advisory both run in any ProfileAll load. The other
// two exist ONLY here, because they are about a disagreement between two
// repositories: resolving a key the core has retired, and refusing the collision
// that resolution can create.

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strconv"

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
// THE CROSS-TREE RULES:
//
//   - EXISTENCE, a hard error. Every community entry must be keyed by a work the
//     core tree holds. A key that is neither live nor retired is a red release,
//     never a silently dropped sidecar - the CC BY-SA layer is the most expensive
//     data in the project and a build that quietly omits one is worse than a
//     build that stops. This is checkIntegrity's sidecar arm, which any ProfileAll
//     load already runs; only the tombstone hop below is new.
//   - REDIRECT RESOLUTION, compose-only. A key naming a slug the core tree has
//     RETIRED (data/redirects.json) is re-keyed onto the surviving slug for this
//     build, and WARNED about (AdvisoryRetiredSidecarKey). A core repair wave
//     that merges two works lands in one repository and the community re-key
//     sweep lands in the other, so the two cannot be atomic; resolving here is
//     what keeps the window between them from losing a sidecar. The warning is
//     what keeps that window VISIBLE: a redirect ridden silently becomes a
//     permanent dependency nobody can date. It is a build-time resolution only -
//     nothing is written, and the sweep is still the fix.
//   - COLLISION, a hard error, compose-only. Two sidecars of the SAME KIND
//     resolving onto one work (a redirect landing on a work that already has one)
//     is refused rather than folded: which entry describes the surviving work is
//     a human decision, the same principle internal/repair's
//     sidecar-member-collision refusal rests on. DISJOINT members - a characters
//     sidecar for the retired slug beside a recaps sidecar for the survivor -
//     simply meet on one work, which is exactly what a composed entry is.
//   - THE POSITION-SCALE ADVISORY. checkSidecarPositionScale reads a work's
//     recordings to judge whether a sidecar's chapter positions are scaled to
//     something else. It is vacuous over a community root alone (no works, so no
//     floor to measure against) and runs here for real, as it does in a ProfileAll
//     load. It stays a WARNING: a sidecar that genuinely covers only the opening
//     of a long book is a legitimate partial contribution.
//
// EVERY RULE RUNS, RED OR NOT. A problem in either root does not suppress the
// cross-tree pass, exactly as a malformed people pack in a single tree has never
// suppressed checkIntegrity's dangling-sidecar reports: one run reports
// everything it can see, and a failed works pack producing a wave of dangling
// keys beside it is the same noise class the single tree already has.
//
// KEYS ARE REWRITTEN ONLY ON A GREEN PASS. The resolution is computed, judged and
// reported first; the catalogue's sidecar keys are actually re-pointed only when
// the composed result ends with zero problems. So a red result hands back the
// catalogue with its keys AS WRITTEN - the state the tree is really in - and the
// only place the resolution appears is in the warnings and problems that
// describe it. Nothing is lost: the artifact is built only from an OK result.
//
// EQUIVALENCE WITH A SINGLE TREE IS CONDITIONAL, and the condition is the
// tombstone window. For a pair whose community keys are ALL LIVE, this produces
// exactly the catalogue - and so exactly the artifact - that one ProfileAll tree
// holding the same records produces; that is the property the repository split
// rests on and internal/build's TestComposedArtifactEqualsSingleTree pins. During
// a window where a core merge has landed and the community re-key sweep has not,
// the pair is deliberately MORE PERMISSIVE than a single tree: a single tree
// refuses a sidecar keyed by a retired slug outright (checkIntegrity: the work
// does not exist), while this resolves it and builds. The
// AdvisoryRetiredSidecarKey warning is the visible marker of that state, and the
// sweep is what ends it.
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

	// The core load's PATH INDEX is dropped: every cross-tree rule reports against
	// a community-side path, and a ProfileCore load cannot have populated the
	// characters/recaps maps that would be asked for one. Holding it would pin the
	// core index - a map over every work, recording, person and series - alive for
	// the whole of the community load, for nothing.
	core, _ := load(coreLst, nil)
	com, comIdx := load(comLst, nil)
	// Every path the community half will be reported against - the load's own
	// problems, and every path a cross-tree rule reads out of the index, whether it
	// reports AT that path or names it inside a message - is attributed at the
	// source, so no rule below has to know the marker exists.
	comIdx.prefix(communityMarker)

	res := Result{
		Problems: slices.Concat(core.Problems, fromCommunity(com.Problems)),
		Warnings: slices.Concat(core.Warnings, fromCommunity(com.Warnings)),
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

	// AN EMPTY COMMUNITY SIDE IS A HARD ERROR, and it is checked before the
	// cross-tree rules because a tree with nothing in it satisfies every one of
	// them. pack.ListProfile tolerates a missing family root by design - an absent
	// family is writable-empty, which is right for a store - so a directory that
	// simply is not a community checkout composes clean and ships an artifact with
	// the whole CC BY-SA layer dropped. That is the quiet omission the existence
	// rule exists to prevent, reachable by pointing the flag at the community
	// repository's ROOT rather than at its data/ subdirectory. Asking for the layer
	// and silently getting none is never what the caller meant; not asking (no
	// --community) is still how a core-only artifact is built.
	if len(res.Catalog.Characters) == 0 && len(res.Catalog.Recaps) == 0 {
		res.Problems = append(res.Problems, Problem{
			Path: communityDir,
			Msg: "holds no works-community entries: composing it would ship an artifact with no " +
				"community layer at all. Point --community at the community checkout's data/ " +
				"directory, or omit the flag to build the core alone",
		})
	}

	// The cross-tree rules report against COMMUNITY paths - a ProfileCore load
	// cannot even populate the sidecar maps a problem here would be looked up in -
	// and comIdx has already attributed every one of them.
	add, warn := appendTo(&res.Problems), appendTo(&res.Warnings)

	rewrites := resolveSidecarKeys(res.Catalog, comIdx, add, warn)
	if len(res.Problems) == 0 {
		for _, rw := range rewrites {
			*rw.work = rw.to
		}
	}
	// After the rewrites, so a resolved sidecar is measured against the work it
	// actually describes. On a red pass the keys stand as written and a redirected
	// sidecar simply finds no work to measure against, which is the advisory's own
	// "nothing to compare" path - and the build has already failed.
	checkSidecarPositionScale(res.Catalog, comIdx, warn)

	sortProblems(res.Problems)
	sortProblems(res.Warnings)
	return res
}

// communityMarker prefixes the Path of everything reported against the COMMUNITY
// root, so one composed list of root-relative paths still says which checkout
// each line belongs to. Two paths are spellable by both roots - redirects.json
// and anything under works-community/ - so without it an operator (or the
// automation routing a failure to a repository) has to guess.
//
// Composed mode ONLY. A single-root load's paths are untouched, byte for byte,
// because there is no second root for them to be confused with.
const communityMarker = "community: "

// fromCommunity re-points a community load's problems at the community root.
func fromCommunity(ps []Problem) []Problem {
	if len(ps) == 0 {
		return nil
	}
	out := make([]Problem, len(ps))
	for i, p := range ps {
		out[i] = Problem{Path: communityMarker + p.Path, Msg: p.Msg}
	}
	return out
}

// appendTo is the addFunc the cross-tree rules accumulate through. They are the
// loader's rules run after the loader is gone, so there is no l.add to take a
// method value of; the list they append to is the composed result's own.
func appendTo(dst *[]Problem) addFunc {
	return func(path, format string, args ...any) {
		*dst = append(*dst, Problem{Path: path, Msg: fmt.Sprintf(format, args...)})
	}
}

// rekey is one pending sidecar key rewrite: the record's own work field, and the
// surviving slug it resolved onto. Pending because the rewrite is applied only on
// a green pass - see LoadComposed.
type rekey struct {
	work *string
	to   string
}

// sidecarTarget is the group key the collision rule judges by: one member kind
// on one surviving work. A comparable STRUCT rather than a joined string -
// injective and ordered by construction, with no separator to argue about.
type sidecarTarget struct{ kind, work string }

// resolvedSidecar is one member whose key resolved: where it came from, and the
// live work it landed on. from == to for a key that was live as written.
type resolvedSidecar struct {
	sidecarRef
	from string
	to   string
}

// ridesRedirect reports whether this member reached its work through the slug
// tombstone table rather than by naming it. It is an int rather than a bool
// because it is a SORT rung - 0 (the incumbent) before 1 (a rider) - and Go has
// no ordering on bools.
func (r resolvedSidecar) ridesRedirect() int {
	if r.from == r.to {
		return 0
	}
	return 1
}

// resolveSidecarKeys is the EXISTENCE rule, the redirect resolution and the
// collision the resolution can create - one pass, because they are one question
// asked of one key: which live work does this sidecar describe?
//
// It REPORTS but does not rewrite: the rewrites it decided on come back for the
// caller to apply once the whole composed result is known to be green. A rule
// that mutated as it went would leave a red result holding keys that are neither
// what the tree says nor what a green build would have produced.
func resolveSidecarKeys(cat *model.Catalog, idx *pathIndex, add, warn addFunc) []rekey {
	live := idSet(cat.Works, func(w *model.Work) string { return w.ID })
	tomb := cat.Redirects[model.RedirectWorks]

	// Resolution and collision detection are TWO passes on purpose: a group is
	// judged by what its members resolved to, so which entry gets reported cannot
	// depend on which one the walk reached first.
	byTarget := map[sidecarTarget][]resolvedSidecar{}

	for _, ref := range sidecarRefs(cat, idx) {
		from := *ref.work
		to, retired := tomb[from]
		switch {
		case live[from]:
			to = from
		case retired && live[to]:
			// A ridden redirect is never silent: the sweep dependency has to be
			// datable from a build log, or it becomes permanent by default.
			warn(ref.path, "%s sidecar is keyed by the retired work slug %q, resolved onto %q "+
				"for this build: the community re-key sweep is pending",
				ref.kind, from, to)
		default:
			// The retired-but-dead-target arm is reachable exactly when the core tree
			// is itself red about that redirect (checkRedirects refuses a target that
			// is not a live id) - and the cross-tree pass runs red or not, so it shares
			// this message rather than needing one of its own.
			add(ref.path, "%s sidecar is keyed by work %q, which the core tree does not hold%s: "+
				"re-key the entry to a live work slug or withdraw it",
				ref.kind, from, danglingRedirectNote(retired, to))
			continue
		}
		k := sidecarTarget{kind: ref.kind, work: to}
		byTarget[k] = append(byTarget[k], resolvedSidecar{sidecarRef: ref, from: from, to: to})
	}

	var rewrites []rekey
	for _, k := range sortedTargets(byTarget) {
		group := byTarget[k]
		// THE KEEPER IS THE INCUMBENT: an entry whose key was ALREADY the live
		// target beats every entry that only reached it by riding a redirect. Path
		// order alone put a rider first often enough, and the message then told the
		// operator to fold away the entry that was correctly keyed all along.
		// Riders (and, impossibly in a root checkSidecarUniqueness passed, several
		// incumbents) fall back to path order, so the report is deterministic
		// either way.
		slices.SortFunc(group, func(a, b resolvedSidecar) int {
			return cmp.Or(cmp.Compare(a.ridesRedirect(), b.ridesRedirect()), cmp.Compare(a.path, b.path))
		})
		if len(group) > 1 {
			// A collision needs a redirect to exist at all: two sidecars of one kind
			// keyed by the SAME live slug are the community tree's own duplicate, which
			// checkSidecarUniqueness has already reported there. Every surplus entry is
			// reported against the keeper, so the report is one line per entry to fix
			// rather than a set with no anchor.
			for _, r := range group[1:] {
				add(r.path, "%s sidecar keyed by work %q resolves onto %q, which already has one (%s): "+
					"which of the two describes the surviving work is a human decision - fold them into one entry",
					r.kind, r.from, r.to, group[0].path)
			}
		}
		for _, r := range group {
			if r.from != r.to {
				rewrites = append(rewrites, rekey{work: r.work, to: r.to})
			}
		}
	}
	return rewrites
}

// sortedTargets returns the group keys in (kind, work) order, so a report over
// them is deterministic.
func sortedTargets[V any](m map[sidecarTarget][]V) []sidecarTarget {
	keys := slices.Collect(maps.Keys(m))
	slices.SortFunc(keys, func(a, b sidecarTarget) int {
		return cmp.Or(cmp.Compare(a.kind, b.kind), cmp.Compare(a.work, b.work))
	})
	return keys
}

// danglingRedirectNote states, inside the existence message, that the key WAS
// retired but its target is not a live work either. That is a core tree the
// redirect rules have already gone red about, which the cross-tree pass now sees
// because it runs whether or not a side is red.
func danglingRedirectNote(retired bool, to string) string {
	if !retired {
		return ""
	}
	return " (the slug redirects to " + strconv.Quote(to) + ", which is not there either)"
}
