package audit

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/titlerule"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// W-DUP subclasses.
const (
	dupTitleAuthor    = "title-author"    // same cleaned title, same work identity
	dupVolumeConflict = "volume-conflict" // ...but two members state DIFFERENT volume numbers
	dupSeriesVolume   = "series-volume"   // a title's own "series + volume N" resolves to another member
)

// runtimeRatioVeto is how many times longer one member's longest recording may be
// than another's before the cluster stops being a merge proposal.
//
// It replaces a note that argued the WRONG WAY. The first draft reported a >10%
// runtime gap as "a different production, which is two recordings of one work" and
// let the merge stand - reasoning that holds for 576 vs 626 minutes and is absurd for
// 409 vs 3,843, where the long side is a complete-series collection. 1.5x is well
// clear of any real production difference (an abridgement against its unabridged
// twin is the widest legitimate case) and well under any collection ratio.
const runtimeRatioVeto = 1.5

// workKey is one clustering key a work contributes, with the derivation that
// produced it - which is the evidence a reviewer needs, since "cleaned against a
// series name embedded in the title" is a weaker claim than "cleaned against the
// series the work is a member of".
type workKey struct {
	key     string
	cleaned string
	series  string // the series NAME the title was cleaned against, or ""
	via     string
}

const (
	viaOwnSeries      = "own-series"
	viaEmbeddedSeries = "embedded-series"
	viaPlain          = "no-series"
)

// workKeys returns the cluster keys a work contributes: one for its title cleaned
// against the series it belongs to (or against nothing), plus - for a work with NO
// membership - one for its title cleaned against a series name the title itself
// spells out.
//
// The second key is what makes the calibration pair meet. "Hammered: The Iron
// Druid Chronicles, Book 3" is a member of nothing, so its own key is the whole
// decorated title; cleaned against the Iron Druid Chronicles name it embeds, it is
// "Hammered" - which is exactly the key of the work that IS #3 of that series.
//
// The key is the TITLE ONLY. Identity is decided pairwise inside the group (see
// identityClusters), because the identity rule matches NESTED author sets and an
// author-set component in the key can only express EQUAL ones - so 13 real pairs
// that the identity rule calls one work never met ("June's Wild Flight" against "The
// Last Kids on Earth: June's Wild Flight", whose author lists differ by a
// role-credited contributor). Grouping by title and asking the rule is what
// pkg/check's own advisory does.
func (ix *index) workKeys(w *model.Work) []workKey {
	d := ix.derived(w)
	keys := make([]workKey, 0, 2)
	// fold is the work's NORMALIZED IDENTITY key for this derivation
	// (titlerule.IdentityTitleKey, memoized in workDerived): the same rule pkg/check's
	// census and the two writers' duplicate guards key by, so a pair this class
	// clusters is a pair they would refuse to re-create.
	addKey := func(fold, cleaned, series, via string) {
		k := fold
		// A cleaned title that carries no identity of its own is what is left of an
		// omnibus or a box set once the series name comes off, and every such title
		// reduces to the same word - which is why the identity RULE refuses to key it at
		// all (a gate keying "Cars 2" and "Hawk 2" alike would refuse an unrelated
		// sequel). This class wants those groups anyway, because two records of one
		// omnibus are a real finding, so it keys them by the SERIES as well: that keeps
		// two different collections apart while letting two records of ONE collection
		// meet. The addition is W-DUP's, not the rule's - a REPORT can afford a group a
		// mechanical refusal cannot - and it is applied on CarriesIdentity alone, which
		// is the grouping this class was calibrated at (4,596 clusters), rather than on
		// the rule's answer, whose numeric exception is about what a gate may refuse.
		if !titlerule.CarriesIdentity(cleaned) {
			raw := titlerule.CompareKey(cleaned)
			if raw == "" {
				return
			}
			k = raw + "|" + d.seriesID
		}
		if k == "" {
			return
		}
		for _, prev := range keys {
			if prev.key == k {
				return
			}
		}
		keys = append(keys, workKey{key: k, cleaned: cleaned, series: series, via: via})
	}
	if !d.embedded {
		via := viaPlain
		if d.seriesName != "" {
			via = viaOwnSeries
		}
		addKey(d.wantKey, d.want, d.seriesName, via)
		return keys
	}
	// A series name the TITLE spells out is weaker evidence than a membership, so
	// the work contributes both keys: its title cleaned against nothing, and its
	// title cleaned against the name it embeds.
	addKey(d.plainKey, d.plain, "", viaPlain)
	addKey(d.wantKey, d.want, d.seriesName, viaEmbeddedSeries)
	return keys
}

// dupMember is one work inside a candidate cluster.
type dupMember struct {
	work *model.Work
	wk   workKey
}

// detectWorkDup finds the near-duplicate work clusters. It returns, for the
// sidecar detector, the works each emitted cluster holds AND the inverse - which
// clusters each work landed in - because REF-SIDECAR needs the inverse and
// rebuilding it there would be a second pass over the same data.
func detectWorkDup(ix *index) (f *findings, clusterWorks map[string][]string, clustersOf map[string][]string) {
	f = &findings{class: ClassWorkDup}
	clusterWorks = map[string][]string{}
	clustersOf = map[string][]string{}

	var all []dupMember
	for _, w := range ix.cat.Works {
		for _, wk := range ix.workKeys(w) {
			all = append(all, dupMember{work: w, wk: wk})
		}
	}
	groups, keys := groupBy(all, func(m dupMember) string { return m.wk.key })

	// Collect every candidate cluster first, then CLOSE them over shared works
	// before anything is proposed - see closeClusters.
	var candidates []dupCluster
	for _, key := range keys {
		for _, c := range identityClusters(ix, groups[key]) {
			c.key = key + "#" + c.members[0].work.ID
			candidates = append(candidates, c)
		}
	}
	for _, c := range closeClusters(candidates) {
		fd := dupFinding(ix, c)
		f.add(fd)
		ids := make([]string, 0, len(c.members))
		for _, m := range c.members {
			ids = append(ids, m.work.ID)
			clustersOf[m.work.ID] = append(clustersOf[m.work.ID], fd.Key)
		}
		clusterWorks[fd.Key] = sortedUnique(ids)
	}

	detectSeriesVolumeDup(ix, f, clusterWorks, clustersOf)
	return f, clusterWorks, clustersOf
}

// dupCluster is one candidate cluster: its members, its record key, and the notes
// its construction earned.
type dupCluster struct {
	key     string
	members []dupMember
	// mergedFrom names the keys a closure fused, when it fused any.
	mergedFrom []string
	// otherLangs are the languages the same title key held that this cluster is
	// not in - what the language rule split it away from.
	otherLangs []string
}

// identityClusters splits one title key's members into the groups the IDENTITY rule
// calls one work, and is where the two structural rules live.
//
// LANGUAGE: a cluster may hold at most one STATED language. pkg/check's
// languagesCompatible - an unknown language never separates - is the right pairwise
// rule, but applying it anchor-only lets an unknown-language work BRIDGE an English
// and a German work into one cluster, which is a merge across a translation. So the
// test is over the group: every stated language in it must be the same one.
//
// IDENTITY: check.IdentityEqualWorks, pairwise, seeded on each unclaimed member.
// This is where nested author sets are actually reached; the key can only express
// equal ones.
func identityClusters(ix *index, members []dupMember) []dupCluster {
	members = dedupeMembers(members)
	if len(members) < 2 {
		return nil
	}
	var out []dupCluster
	used := make([]bool, len(members))
	for i := range members {
		if used[i] {
			continue
		}
		group := []dupMember{members[i]}
		langs := statedLangs(members[i].work.Language)
		used[i] = true
		for j := i + 1; j < len(members); j++ {
			if used[j] {
				continue
			}
			cand := members[j].work
			// At most one stated language in the whole group, checked against what
			// the group already holds rather than against the anchor alone.
			if l := cand.Language; l != "" && len(langs) > 0 && !langs[l] {
				continue
			}
			if !check.IdentityEqualWorks(members[i].work, cand) {
				continue
			}
			group = append(group, members[j])
			used[j] = true
			if cand.Language != "" {
				langs = statedLangs(cand.Language)
			}
		}
		if len(group) < 2 {
			continue
		}
		c := dupCluster{members: group}
		// What this cluster was split away from, so the record says why the key
		// held more than the cluster does.
		mine := map[string]bool{}
		for _, m := range group {
			mine[m.work.Language] = true
		}
		for _, m := range members {
			if !mine[m.work.Language] {
				c.otherLangs = append(c.otherLangs, renderLang(m.work.Language))
			}
		}
		c.otherLangs = sortedUnique(c.otherLangs)
		out = append(out, c)
	}
	return out
}

func renderLang(l string) string {
	if l == "" {
		return "(unset)"
	}
	return l
}

func statedLangs(l string) map[string]bool {
	if l == "" {
		return nil
	}
	return map[string]bool{l: true}
}

// closeClusters transitively fuses candidate clusters that SHARE a work, so no work
// is ever named by two merge proposals.
//
// Without it the report contradicted itself: 27 overlapping pairs named different
// targets, 25 works were told to fold onto two different targets, and 9 were a target
// in one proposal and a loser in another. A repair pass applying them in file order
// would produce a different catalogue than one applying them in reverse, which is not
// a repair. Union-find over the shared works, then one record per closed component.
func closeClusters(cs []dupCluster) []dupCluster {
	parent := make([]int, len(cs))
	for i := range parent {
		parent[i] = i
	}
	find := func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra != rb {
			// Lower index wins, so the component's identity is deterministic.
			if rb < ra {
				ra, rb = rb, ra
			}
			parent[rb] = ra
		}
	}
	seen := map[string]int{}
	for i, c := range cs {
		for _, m := range c.members {
			if j, dup := seen[m.work.ID]; dup {
				union(i, j)
			} else {
				seen[m.work.ID] = i
			}
		}
	}
	byRoot := map[int][]int{}
	var roots []int
	for i := range cs {
		r := find(i)
		if _, ok := byRoot[r]; !ok {
			roots = append(roots, r)
		}
		byRoot[r] = append(byRoot[r], i)
	}
	sort.Ints(roots)

	out := make([]dupCluster, 0, len(roots))
	for _, r := range roots {
		idx := byRoot[r]
		if len(idx) == 1 {
			out = append(out, cs[idx[0]])
			continue
		}
		merged := dupCluster{key: cs[idx[0]].key}
		byID := map[string]dupMember{}
		for _, i := range idx {
			merged.mergedFrom = append(merged.mergedFrom, cs[i].key)
			for _, m := range cs[i].members {
				prev, dup := byID[m.work.ID]
				if !dup || (prev.wk.series == "" && m.wk.series != "") {
					byID[m.work.ID] = m
				}
			}
		}
		for _, m := range byID {
			merged.members = append(merged.members, m)
		}
		sort.Slice(merged.members, func(a, b int) bool {
			return merged.members[a].work.ID < merged.members[b].work.ID
		})
		merged.mergedFrom = sortedUnique(merged.mergedFrom)
		out = append(out, merged)
	}
	return out
}

// dedupeMembers keeps one entry per work id, preferring the derivation that used
// a series name (the more informative one), and returns them in id order.
func dedupeMembers(ms []dupMember) []dupMember {
	best := map[string]dupMember{}
	for _, m := range ms {
		prev, seen := best[m.work.ID]
		if !seen || (prev.wk.series == "" && m.wk.series != "") {
			best[m.work.ID] = m
		}
	}
	out := make([]dupMember, 0, len(best))
	for _, m := range best {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].work.ID < out[j].work.ID })
	return out
}

// dupFinding composes one cluster's record.
func dupFinding(ix *index, c dupCluster) Finding {
	members := c.members
	fd := Finding{Subclass: dupTitleAuthor, Key: c.key}
	for _, m := range members {
		fd.Works = append(fd.Works, ix.workRef(m.work, m.wk.cleaned))
	}
	fd.Notes = append(fd.Notes, dupViaNote(members))
	if len(c.otherLangs) > 0 {
		fd.Notes = append(fd.Notes, "language-split cluster: the same title key also holds works in "+
			truncateList(c.otherLangs, 8)+" - a translation is a different work and is never proposed for merge")
	}
	if len(c.mergedFrom) > 1 {
		fd.Notes = append(fd.Notes, "closed over "+truncateList(c.mergedFrom, 4)+
			": these keys shared a work, so they are one cluster and one proposal")
	}

	// A cluster whose members state DIFFERENT volume numbers in their own titles is
	// siblings of a multi-volume work, not duplicates of one. That gets its own
	// subclass, because the cluster is still worth reading.
	if vols, conflict := statedVolumes(ix, members); conflict {
		fd.Subclass = dupVolumeConflict
		fd.Propose = Proposal{
			Op:       OpReview,
			Advisory: true,
			Reason: "the titles state different volume numbers, so these are probably volumes of one multi-volume work rather than " +
				"duplicates: either model them as a series, or give them titles that differ",
		}
		fd.Notes = append(fd.Notes, "volume numbers stated in the titles: "+truncateList(vols, 8))
		return fd
	}

	canon := canonicalMember(ix, members)
	var others []string
	for _, m := range members {
		if m.work.ID != canon.work.ID {
			others = append(others, m.work.ID)
		}
	}
	fd.Propose = Proposal{
		Op:     OpMergeWorks,
		Target: canon.work.ID,
		Others: sortedUnique(others),
		Reason: "move the recordings, series memberships and sidecars onto the target",
	}
	// The vetoes. A cluster that trips any of them is still reported - a reviewer
	// wants to see it - but a mechanical pass must not apply it.
	if vetoes := mergeVetoes(ix, members, canon); len(vetoes) > 0 {
		fd.Propose.Advisory = true
		fd.Propose.Reason = "do not merge on this evidence: " + strings.Join(vetoes, "; ")
	}
	if n := sidecarCount(ix, members); n > 1 {
		fd.Notes = append(fd.Notes, fmt.Sprintf("%s in this cluster carry a works-community sidecar - see REF-SIDECAR", joinCount(n, "work")))
	}
	return fd
}

// mergeVetoes lists the reasons a cluster must not be merged mechanically, in a
// fixed order so a record's reason text is deterministic. Empty means the cluster
// passed everything.
//
// Every one of these was measured as a wrong proposal in the first draft, and every
// one asks a question a TITLE cannot answer - which is why the first draft, which
// only ever compared titles and author sets, got 25-60% of them wrong.
func mergeVetoes(ix *index, members []dupMember, canon dupMember) []string {
	var out []string
	if s, ok := vetoPositionConflict(ix, members); ok {
		out = append(out, s)
	}
	if s, ok := vetoDisjointSeries(ix, members); ok {
		out = append(out, s)
	}
	if s, ok := vetoCollectionOneSide(members); ok {
		out = append(out, s)
	}
	if s, ok := vetoRuntimeRatio(members); ok {
		out = append(out, s)
	}
	if s, ok := vetoDecoratedTarget(ix, members, canon); ok {
		out = append(out, s)
	}
	if s, ok := vetoUnaddressableTitle(members); ok {
		out = append(out, s)
	}
	return out
}

// vetoUnaddressableTitle: the cluster meets only on a comparison key that has no
// identity in it, and the members' cleaned titles are not even the same string.
//
// It was found by RUNNING the repair pass over the real tree, which is the only place it
// shows. The cluster key is titlerule.CompareKey, which folds away everything that is not
// ASCII alphanumeric, so two different Russian novels by one pair of authors - "Грани
// безумия. Том 1" and "Клинком и сердцем, Том 1" - both reduce to the key "1" and were
// proposed for merge with every other veto clear. The same fold is what makes an
// unaddressable name a refusal in the importer (getOrCreateSeries) and in the libex credit
// gate: a string this project's identity rules keep nothing of cannot be evidence that two
// records are one book.
//
// It is narrow, and measured over the 279k-work tree: it fires only when NO member's
// cleaned title CarriesIdentity and the cleaned titles differ as strings, which is 2 of
// the 1,846 non-advisory merge-works proposals - both of them the shape above. The
// legitimate members of the same regime ("1984" against "1984", "22/11/63" against
// "22/11/63", "1177 B.C" against "1177 B.C") state IDENTICAL cleaned titles and keep
// their merge.
func vetoUnaddressableTitle(members []dupMember) (string, bool) {
	for _, m := range members {
		if titlerule.CarriesIdentity(m.wk.cleaned) {
			return "", false
		}
	}
	first := members[0]
	for _, m := range members[1:] {
		if m.wk.cleaned != first.wk.cleaned {
			return fmt.Sprintf("%s cleans to %q and %s to %q, and neither names a book a rule can identify: they meet on a "+
				"comparison key that folds away everything non-ASCII, which is two different books meeting on a number",
				first.work.ID, first.wk.cleaned, m.work.ID, m.wk.cleaned), true
		}
	}
	return "", false
}

// vetoPositionConflict: two members hold DIFFERENT positions in one series, so the
// catalogue itself already says they are different volumes. Read from series_works
// rather than from the titles: 520 clusters (14%) are same-title distinct volumes
// whose titles state no number at all.
// The series ids are walked in SORTED order, not in map order. A pair can conflict
// in more than one series (a work in both "X" and "The Collected X"), and naming
// whichever one the map yielded first made the report differ between runs over an
// unchanged tree - the one property this package claims by construction.
func vetoPositionConflict(ix *index, members []dupMember) (string, bool) {
	spans := make([]map[string][2]float64, len(members))
	for i, m := range members {
		spans[i] = ix.positionSpans(m.work.ID)
	}
	for i := range members {
		ids := make([]string, 0, len(spans[i]))
		for sid := range spans[i] {
			ids = append(ids, sid)
		}
		sort.Strings(ids)
		for j := i + 1; j < len(members); j++ {
			for _, sid := range ids {
				a := spans[i][sid]
				b, both := spans[j][sid]
				if both && a != b {
					return fmt.Sprintf("%s and %s hold different positions in series %s (%s vs %s)",
						members[i].work.ID, members[j].work.ID, sid, renderSpan(a), renderSpan(b)), true
				}
			}
		}
	}
	return "", false
}

func renderSpan(s [2]float64) string {
	if s[0] == s[1] {
		return formatSeq(s[0])
	}
	return formatSeq(s[0]) + "-" + formatSeq(s[1])
}

// vetoDisjointSeries: both sides are modeled, and in ENTIRELY different series. Two
// records of one book do not sit in two disjoint series; a book and its companion,
// or two books sharing a title, do.
func vetoDisjointSeries(ix *index, members []dupMember) (string, bool) {
	var sets []map[string]bool
	var owners []string
	for _, m := range members {
		if s := ix.seriesIDs(m.work.ID); len(s) > 0 {
			sets = append(sets, s)
			owners = append(owners, m.work.ID)
		}
	}
	if len(sets) < 2 {
		return "", false
	}
	for i := range sets {
		for j := i + 1; j < len(sets); j++ {
			shared := false
			for sid := range sets[i] {
				if sets[j][sid] {
					shared = true
					break
				}
			}
			if !shared {
				return fmt.Sprintf("%s and %s are modeled in entirely different series (%s vs %s)",
					owners[i], owners[j], truncateList(sortedKeys(sets[i]), 3), truncateList(sortedKeys(sets[j]), 3)), true
			}
		}
	}
	return "", false
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// vetoCollectionOneSide: one member announces itself as a COLLECTION and another
// does not. A companion omnibus and the volume it collects are not two records of one
// book. The vocabulary is multilingual (titlerule.IsCollection) - the English-only
// test let three different Tao Wong series' omnibuses merge.
func vetoCollectionOneSide(members []dupMember) (string, bool) {
	var yes, no []string
	for _, m := range members {
		if titlerule.IsCollection(m.work.Title) {
			yes = append(yes, m.work.ID)
		} else {
			no = append(no, m.work.ID)
		}
	}
	if len(yes) == 0 || len(no) == 0 {
		return "", false
	}
	return fmt.Sprintf("%s announce a collection and %s do not: a companion collection is not a second record of the volume it collects",
		truncateList(yes, 3), truncateList(no, 3)), true
}

// vetoRuntimeRatio: one member's longest recording is more than runtimeRatioVeto
// times another's, which no two productions of one book are.
func vetoRuntimeRatio(members []dupMember) (string, bool) {
	type rt struct {
		id  string
		min int
	}
	var rts []rt
	for _, m := range members {
		if n := longestRuntime(m.work); n > 0 {
			rts = append(rts, rt{m.work.ID, n})
		}
	}
	if len(rts) < 2 {
		return "", false
	}
	lo, hi := rts[0], rts[0]
	for _, r := range rts[1:] {
		if r.min < lo.min {
			lo = r
		}
		if r.min > hi.min {
			hi = r
		}
	}
	if float64(hi.min) <= runtimeRatioVeto*float64(lo.min) {
		return "", false
	}
	return fmt.Sprintf("runtimes differ by %.1fx (%s at %d min vs %s at %d min): too far apart to be one book",
		float64(hi.min)/float64(lo.min), hi.id, hi.min, lo.id, lo.min), true
}

// vetoDecoratedTarget: the chosen target's title still carries decoration while a
// loser's does not. The ladder puts Decorations above Recordings precisely so this
// cannot normally happen; it still can when the decorated member is the MODELED one,
// and then which record should survive is a judgement.
func vetoDecoratedTarget(ix *index, members []dupMember, canon dupMember) (string, bool) {
	if len(ix.derived(canon.work).markers) == 0 {
		return "", false
	}
	for _, m := range members {
		if m.work.ID != canon.work.ID && len(ix.derived(m.work).markers) == 0 {
			return fmt.Sprintf("the target %s still carries a decorated title while %s does not: retitle first (see W-TITLE), "+
				"then decide which record survives", canon.work.ID, m.work.ID), true
		}
	}
	return "", false
}

// dupViaNote states how each member's title was cleaned, so a reviewer can see
// whether the cluster rests on a series MEMBERSHIP or on a series name the title
// merely spells out - which is the weaker of the two claims.
func dupViaNote(members []dupMember) string {
	vias := make([]string, 0, len(members))
	for _, m := range members {
		v := m.work.ID + ": " + m.wk.via
		if m.wk.series != "" {
			v += ` "` + m.wk.series + `"`
		}
		vias = append(vias, v)
	}
	return "cleaned via " + truncateList(sortedUnique(vias), 8)
}

// statedVolumes returns the volume numbers the cluster's own TITLES spell out, and
// whether two of them disagree. A member stating no number is not a disagreement:
// "Hammered" beside "Hammered: The Iron Druid Chronicles, Book 3" is the pair the
// class exists to find.
func statedVolumes(ix *index, members []dupMember) (vols []string, conflict bool) {
	byNum := map[string][]string{}
	for _, m := range members {
		d := ix.derived(m.work)
		if !d.hasSeq {
			continue
		}
		k := formatSeq(d.seq)
		byNum[k] = append(byNum[k], m.work.ID)
	}
	if len(byNum) < 2 {
		return nil, false
	}
	for k, ids := range byNum {
		vols = append(vols, k+" ("+truncateList(sortedUnique(ids), 3)+")")
	}
	return sortedUnique(vols), true
}

func sidecarCount(ix *index, members []dupMember) int {
	n := 0
	for _, m := range members {
		if ix.hasSidecar(m.work.ID) {
			n++
		}
	}
	return n
}

// canonicalMember picks the cluster member to keep, through titlerule's ladder -
// the same values and the same comparison the repair pass will read, so a report
// and a repair can never name different survivors.
func canonicalMember(ix *index, members []dupMember) dupMember {
	best := members[0]
	bestRank := ix.workRank(best.work)
	for _, m := range members[1:] {
		if r := ix.workRank(m.work); r.Better(bestRank) {
			best, bestRank = m, r
		}
	}
	return best
}

// workRank fills titlerule's ranking evidence for one work.
func (ix *index) workRank(w *model.Work) titlerule.WorkRank {
	return titlerule.WorkRank{
		InSeries:    len(ix.memberships[w.ID]) > 0,
		Decorations: len(ix.derived(w).markers),
		TitleLen:    len(w.Title),
		Recordings:  len(w.Recordings),
		HasSidecar:  ix.hasSidecar(w.ID),
		ID:          w.ID,
	}
}

// detectSeriesVolumeDup is the second duplicate shape: a work that belongs to no
// series, whose TITLE states a series and a volume number, where that series already
// holds a different work at that position.
//
// ADVISORY, wholesale. A full census put it at 34% wrong with eight unsafe merges,
// and the cause is not a missing guard: BareSeq cannot tell a series position from a
// PART, an EPISODE, a SEASON or a collection number, so GraphicAudio's "Part 2 of 2"
// folds onto book 2, a French "tome N, episode M" onto tome N, and a sub-series'
// "Book N" onto the parent series' slot. Telling them apart needs title agreement
// plus publisher and runtime coherence - a later, human-assisted pass. Until then the
// class is a reading list, and the evidence a reviewer needs (publisher, release
// dates, runtimes) is on every record.
func detectSeriesVolumeDup(ix *index, f *findings, clusterWorks, clustersOf map[string][]string) {
	for _, w := range ix.cat.Works {
		if len(ix.memberships[w.ID]) > 0 {
			continue
		}
		d := ix.derived(w)
		if !d.embedded || !d.hasSeq {
			continue
		}
		other := ix.positions[d.seriesID][formatSeq(d.seq)]
		if other == "" || other == w.ID {
			continue
		}
		ow := ix.workByID[other]
		if ow == nil {
			continue // a dangling membership; S-INTEGRITY reports it
		}
		if !languagesCompatible(ow.Language, w.Language) || !check.IdentityEqualWorks(ow, w) {
			continue
		}
		if sharesKey(clustersOf[w.ID], clustersOf[other]) {
			continue // already clustered by title and identity
		}
		reason := fmt.Sprintf("%s states %q volume %s in its title and %s already occupies that position, but a title's number "+
			"is as often a part, an episode, a season or a collection index - confirm the titles agree and the publisher and "+
			"runtime are coherent before merging", w.ID, d.seriesName, formatSeq(d.seq), other)
		fd := Finding{
			Subclass: dupSeriesVolume,
			Key:      pairKey(w.ID, other),
			Works: []WorkRef{
				ix.workRef(w, d.want),
				ix.workRef(ow, ix.derived(ow).want),
			},
			Propose: Proposal{
				Op:       OpReview,
				Target:   other,
				Others:   []string{w.ID},
				Series:   d.seriesID,
				Advisory: true,
				Reason:   reason,
			},
		}
		sort.Slice(fd.Works, func(i, j int) bool { return fd.Works[i].ID < fd.Works[j].ID })
		if s := ix.seriesByID[d.seriesID]; s != nil {
			fd.Series = []SeriesRef{ix.seriesRef(s)}
		}
		f.add(fd)
		clusterWorks[fd.Key] = sortedUnique([]string{w.ID, other})
		clustersOf[w.ID] = append(clustersOf[w.ID], fd.Key)
		clustersOf[other] = append(clustersOf[other], fd.Key)
	}
}

// sharesKey reports whether two works landed in a common emitted cluster.
func sharesKey(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}
