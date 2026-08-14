package audit

import (
	"fmt"
	"sort"

	"github.com/kodestar/audiosilo-meta/internal/titlerule"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// W-DUP subclasses.
const (
	dupTitleAuthor    = "title-author"    // same cleaned title + the same identity authors
	dupVolumeConflict = "volume-conflict" // ...but two members state DIFFERENT volume numbers
	dupSeriesVolume   = "series-volume"   // a title's own "series + volume N" resolves to another member
)

// runtimeGapFrac is the fraction two runtimes may differ by before the audit
// calls them different PRODUCTIONS. It mirrors the importer's ASIN-merge guard
// (>10% is a genuinely different recording), and it is deliberately a NOTE here,
// not an exclusion: two productions of one book are two recordings of one work,
// which is the model, so a runtime gap never stops a merge proposal.
const runtimeGapFrac = 0.10

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
// The AUTHOR half of the key is identityKey, not the raw author list: two records
// of one book routinely disagree about whether a contributor is an author or a role
// credit, and the identity rule (which the pairwise sameIdentity test then
// confirms) is what says they are one work anyway.
func (ix *index) workKeys(w *model.Work) []workKey {
	ak := identityKey(w)
	d := ix.derived(w)
	keys := make([]workKey, 0, 2)
	addKey := func(cleaned, series, via string) {
		fold := titlerule.CompareKey(cleaned)
		if fold == "" {
			return
		}
		k := fold + "|" + ak
		// A cleaned title that carries no identity of its own is what is left of
		// an omnibus or a box set once the series name comes off, and every such
		// title by one author reduces to the same word. Keying those by the SERIES
		// too is what keeps two different collections apart while still letting
		// two records of ONE collection meet - which is precisely the pair the
		// genre-subtitle variant produces.
		if !titlerule.CarriesIdentity(cleaned) {
			k += "|" + d.seriesID
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
		addKey(d.want, d.seriesName, via)
		return keys
	}
	// A series name the TITLE spells out is weaker evidence than a membership, so
	// the work contributes both keys: its title cleaned against nothing, and its
	// title cleaned against the name it embeds.
	addKey(d.plain, "", viaPlain)
	addKey(d.want, d.seriesName, viaEmbeddedSeries)
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

	for _, key := range keys {
		for _, cluster := range identityClusters(groups[key]) {
			fd := dupFinding(ix, key, cluster)
			f.add(fd)
			ids := make([]string, 0, len(cluster.members))
			for _, m := range cluster.members {
				ids = append(ids, m.work.ID)
				clustersOf[m.work.ID] = append(clustersOf[m.work.ID], fd.Key)
			}
			clusterWorks[fd.Key] = sortedUnique(ids)
		}
	}

	detectSeriesVolumeDup(ix, f, clusterWorks, clustersOf)
	return f, clusterWorks, clustersOf
}

// dupCluster is one emitted cluster: its members plus what the split that produced
// it left behind, which the record states.
type dupCluster struct {
	members []dupMember
	// otherLangs are the languages the same key held that this cluster is not in.
	otherLangs []string
	// suffix distinguishes two clusters off one key.
	suffix string
}

// identityClusters splits one key's members into the clusters that may actually be
// proposed for merge, and is where both correctness rules live.
//
// LANGUAGE first: a translation is a different work whatever its title cleans to,
// so members are partitioned into language-COMPATIBLE groups - and "compatible" is
// pkg/check's rule, under which an unknown language never separates anything. A
// work missing its language therefore joins the group it is compatible with rather
// than forming a bucket of its own, which is what a plain equality did; that record
// is the one most likely to have a twin.
//
// IDENTITY second: the key groups works whose reduced author sets are EQUAL, but
// the identity rule also matches nested sets, so within a language group the
// pairwise rule decides. Members are gathered greedily around the first member that
// matches, which is transitive enough in practice (the sets are nested by
// construction) and never puts two works in one cluster unless the rule says so for
// the seed.
func identityClusters(members []dupMember) []dupCluster {
	members = dedupeMembers(members)
	if len(members) < 2 {
		return nil
	}

	// Every distinct language the key holds, for the note.
	var langs []string
	for _, m := range members {
		langs = append(langs, m.work.Language)
	}
	langs = sortedUniqueAllowEmpty(langs)

	var out []dupCluster
	used := make([]bool, len(members))
	for i := range members {
		if used[i] {
			continue
		}
		group := []dupMember{members[i]}
		used[i] = true
		for j := i + 1; j < len(members); j++ {
			if used[j] {
				continue
			}
			if !languagesCompatible(members[i].work.Language, members[j].work.Language) {
				continue
			}
			if !sameIdentity(members[i].work, members[j].work) {
				continue
			}
			group = append(group, members[j])
			used[j] = true
		}
		if len(group) < 2 {
			continue
		}
		c := dupCluster{members: group}
		// What this cluster was split away from: the languages the key held that
		// none of its own members are in.
		mine := map[string]bool{}
		for _, m := range group {
			mine[m.work.Language] = true
		}
		for _, l := range langs {
			if !mine[l] {
				c.otherLangs = append(c.otherLangs, renderLang(l))
			}
		}
		// One key can yield several clusters, so each needs its own record KEY.
		// The lowest member id is the stable, data-derived suffix.
		c.suffix = group[0].work.ID
		out = append(out, c)
	}
	return out
}

// sortedUniqueAllowEmpty is sortedUnique that KEEPS the empty string as a distinct
// value. "no language stated" is a real state the language note has to be able to
// name, and sortedUnique drops it by design everywhere else.
func sortedUniqueAllowEmpty(ss []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func renderLang(l string) string {
	if l == "" {
		return "(unset)"
	}
	return l
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
func dupFinding(ix *index, key string, c dupCluster) Finding {
	members := c.members
	fd := Finding{Subclass: dupTitleAuthor, Key: key + "#" + c.suffix}
	for _, m := range members {
		fd.Works = append(fd.Works, ix.workRef(m.work, m.wk.cleaned))
	}
	fd.Notes = append(fd.Notes, dupViaNote(members))
	if len(c.otherLangs) > 0 {
		fd.Notes = append(fd.Notes, "language-split cluster: the key also holds works in "+
			truncateList(c.otherLangs, 8)+
			" - a translation is a different work and is never proposed for merge")
	}

	// A cluster whose members state DIFFERENT volume numbers in their own titles
	// is siblings of a multi-volume work, not duplicates of one - the shape three
	// volumes of Gibbon take beside the complete edition. pkg/match's own
	// discipline is that two explicit numbers that disagree disqualify a match
	// outright (BareSeq is the only number it will veto on), so the cluster is
	// still REPORTED - identical cleaned titles across four works is a real
	// modeling question - but under its own subclass, with no merge proposed.
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
	if note, ok := runtimeGapNote(members); ok {
		fd.Notes = append(fd.Notes, note)
	}
	if n := sidecarCount(ix, members); n > 1 {
		fd.Notes = append(fd.Notes, fmt.Sprintf("%s in this cluster carry a works-community sidecar - see REF-SIDECAR", joinCount(n, "work")))
	}
	return fd
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

// runtimeGapNote reports a >10% runtime gap between the ONLY recordings of a
// two-member cluster: evidence of a different production, NOT of a different
// work, so it is stated and the merge proposal stands.
func runtimeGapNote(members []dupMember) (string, bool) {
	if len(members) != 2 {
		return "", false
	}
	a, b := members[0].work, members[1].work
	if len(a.Recordings) != 1 || len(b.Recordings) != 1 {
		return "", false
	}
	ra, rb := a.Recordings[0].RuntimeMin, b.Recordings[0].RuntimeMin
	if ra <= 0 || rb <= 0 {
		return "", false
	}
	lo, hi := ra, rb
	if lo > hi {
		lo, hi = hi, lo
	}
	gap := float64(hi-lo) / float64(hi)
	if gap <= runtimeGapFrac {
		return "", false
	}
	return fmt.Sprintf("runtime gap %.0f%% between the only recordings (%d vs %d min): a different PRODUCTION, "+
		"which is two recordings of one work - the title and authors still agree", gap*100, ra, rb), true
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
		HasSidecar:  ix.hasSidecar(w.ID),
		Recordings:  len(w.Recordings),
		Decorations: len(ix.derived(w).markers),
		TitleLen:    len(w.Title),
		ID:          w.ID,
	}
}

// detectSeriesVolumeDup is the second duplicate shape: a work that belongs to no
// series, whose TITLE states a series and a volume number, where that series
// already holds a different work at that position. It catches the pair whose
// residual titles disagree - the retailer's decorated title against the modeled
// volume - which the title-author key cannot see.
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
		if !languagesCompatible(ow.Language, w.Language) || !sameIdentity(ow, w) {
			continue
		}
		if sharesKey(clustersOf[w.ID], clustersOf[other]) {
			continue // already clustered by title and identity
		}
		fd := Finding{
			Subclass: dupSeriesVolume,
			Key:      pairKey(w.ID, other),
			Works: []WorkRef{
				ix.workRef(w, d.want),
				ix.workRef(ow, ix.derived(ow).want),
			},
			Propose: Proposal{
				Op:     OpMergeWorks,
				Target: other,
				Others: []string{w.ID},
				Series: d.seriesID,
				Reason: fmt.Sprintf("%s states %q volume %s in its title, which %s already occupies",
					w.ID, d.seriesName, formatSeq(d.seq), other),
			},
			Notes: []string{
				"the series-less work names the series in its title; the other work is the modeled member at that position",
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
