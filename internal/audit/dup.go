package audit

import (
	"fmt"
	"sort"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// W-DUP subclasses.
const (
	dupTitleAuthor    = "title-author"    // same cleaned title + same author set
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
func (ix *index) workKeys(w *model.Work) []workKey {
	ak := authorKey(w.Authors)
	keys := make([]workKey, 0, 2)
	d := ix.derived(w)
	addKey := func(cleaned, series, via string) {
		fold := titleCompareKey(cleaned)
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
		if !titleCarriesIdentity(cleaned) {
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
	// title cleaned against the name it embeds. The second is what makes
	// "Hammered: The Iron Druid Chronicles, Book 3" meet the work that IS #3.
	addKey(auditCleanTitle(w.Title, ""), "", viaPlain)
	addKey(d.want, d.seriesName, viaEmbeddedSeries)
	return keys
}

// dupMember is one work inside a candidate cluster.
type dupMember struct {
	work *model.Work
	wk   workKey
}

// detectWorkDup finds the near-duplicate work clusters and returns, for the
// sidecar detector, the works each emitted cluster holds.
func detectWorkDup(ix *index) (*findings, map[string][]string) {
	f := &findings{class: ClassWorkDup}
	// keysOf remembers which emitted clusters a work landed in, so the
	// series-volume pass does not re-report a pair the title-author pass already
	// clustered. Recording the KEYS rather than the pairs keeps a big cluster from
	// costing its square.
	keysOf := map[string][]string{}
	// clusterWorks maps an emitted cluster key to its work ids, for REF-SIDECAR.
	clusterWorks := map[string][]string{}

	groups := map[string][]dupMember{}
	var order []string
	for _, w := range ix.cat.Works {
		for _, wk := range ix.workKeys(w) {
			if _, seen := groups[wk.key]; !seen {
				order = append(order, wk.key)
			}
			groups[wk.key] = append(groups[wk.key], dupMember{work: w, wk: wk})
		}
	}
	sort.Strings(order)

	for _, key := range order {
		members := dedupeMembers(groups[key])
		if len(members) < 2 {
			continue
		}
		// Never propose merging works in different languages: a translation is a
		// different work, whatever its title cleans to. The group is split, and
		// each surviving cluster says what it was split away from.
		byLang := map[string][]dupMember{}
		var langs []string
		for _, m := range members {
			l := m.work.Language
			if _, seen := byLang[l]; !seen {
				langs = append(langs, l)
			}
			byLang[l] = append(byLang[l], m)
		}
		sort.Strings(langs)
		for _, lang := range langs {
			sub := byLang[lang]
			if len(sub) < 2 {
				continue
			}
			// The language rides in the cluster key, so two language-split
			// clusters off one title/author key are two records with two keys
			// rather than two records claiming one.
			fd := dupFinding(ix, key+"#"+lang, sub, langs)
			f.add(fd)
			ids := make([]string, 0, len(sub))
			for _, m := range sub {
				ids = append(ids, m.work.ID)
				keysOf[m.work.ID] = append(keysOf[m.work.ID], fd.Key)
			}
			clusterWorks[fd.Key] = sortedUnique(ids)
		}
	}

	detectSeriesVolumeDup(ix, f, keysOf, clusterWorks)
	return f, clusterWorks
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

// dupFinding composes one title-author cluster's record.
func dupFinding(ix *index, key string, members []dupMember, allLangs []string) Finding {
	fd := Finding{Subclass: dupTitleAuthor, Key: key}
	for _, m := range members {
		fd.Works = append(fd.Works, ix.workRef(m.work, m.wk.cleaned))
	}

	// A cluster whose members state DIFFERENT volume numbers in their own titles
	// is siblings of a multi-volume work, not duplicates of one - the shape three
	// volumes of Gibbon take beside the complete edition. pkg/match's own
	// discipline is that two explicit numbers that disagree disqualify a match
	// outright (bareSeq is the only number it will veto on), so the cluster is
	// still REPORTED - identical cleaned titles across four works is a real
	// modeling question - but under its own subclass, with no merge proposed and
	// no canonical named.
	if vols, conflict := statedVolumes(ix, members); conflict {
		fd.Subclass = dupVolumeConflict
		fd.Action = "do NOT merge on this evidence: the titles state different volume numbers, so these are probably volumes of one " +
			"multi-volume work rather than duplicates. Either model them as a series, or give them titles that differ."
		fd.Notes = append(fd.Notes, "volume numbers stated in the titles: "+truncateList(vols, 8))
		fd.Notes = append(fd.Notes, dupViaNote(members))
		return fd
	}

	canon := canonicalMember(ix, members)
	fd.Canonical = canon.work.ID
	var others []string
	for _, m := range members {
		if m.work.ID != canon.work.ID {
			others = append(others, m.work.ID)
		}
	}
	fd.Action = fmt.Sprintf("review as one work: move the recordings, series memberships and sidecars of %s onto %s",
		truncateList(others, 8), canon.work.ID)

	fd.Notes = append(fd.Notes, dupViaNote(members))

	if len(allLangs) > 1 {
		fd.Notes = append(fd.Notes, "language-split cluster: the key also holds works in "+
			truncateList(otherThan(allLangs, members[0].work.Language), 8)+
			" - a translation is a different work and is never proposed for merge")
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
	seen := map[string][]string{}
	for _, m := range members {
		seq, ok := bareSeq(m.work.Title, ix.derived(m.work).seriesName)
		if !ok {
			continue
		}
		k := seqKey(seq)
		seen[k] = append(seen[k], m.work.ID)
	}
	if len(seen) < 2 {
		return nil, false
	}
	for k, ids := range seen {
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
		if ix.sidecarWorks[m.work.ID] {
			n++
		}
	}
	return n
}

// otherThan returns every element of ss that is not want, "" rendered as
// "(unset)" so a note never reads as a gap in itself.
func otherThan(ss []string, want string) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if s == want {
			continue
		}
		if s == "" {
			s = "(unset)"
		}
		out = append(out, s)
	}
	return sortedUnique(out)
}

// canonicalMember picks the cluster member to keep: the one that is already
// modeled (a series membership, then a sidecar), then the one carrying the most
// recordings, then the one whose title carries the least retailer decoration,
// then the shortest title, then the lowest id. Every tiebreaker is data, so the
// choice is the same on every run.
func canonicalMember(ix *index, members []dupMember) dupMember {
	best := members[0]
	bestScore := canonScore(ix, best)
	for _, m := range members[1:] {
		s := canonScore(ix, m)
		if s.better(bestScore) {
			best, bestScore = m, s
		}
	}
	return best
}

type canonRank struct {
	inSeries    bool
	hasSidecar  bool
	recordings  int
	decorations int
	titleLen    int
	id          string
}

func canonScore(ix *index, m dupMember) canonRank {
	return canonRank{
		inSeries:    len(ix.memberships[m.work.ID]) > 0,
		hasSidecar:  ix.sidecarWorks[m.work.ID],
		recordings:  len(m.work.Recordings),
		decorations: len(ix.derived(m.work).markers),
		titleLen:    len(m.work.Title),
		id:          m.work.ID,
	}
}

func (r canonRank) better(o canonRank) bool {
	if r.inSeries != o.inSeries {
		return r.inSeries
	}
	if r.hasSidecar != o.hasSidecar {
		return r.hasSidecar
	}
	if r.recordings != o.recordings {
		return r.recordings > o.recordings
	}
	if r.decorations != o.decorations {
		return r.decorations < o.decorations
	}
	if r.titleLen != o.titleLen {
		return r.titleLen < o.titleLen
	}
	return r.id < o.id
}

// detectSeriesVolumeDup is the second duplicate shape: a work that belongs to no
// series, whose TITLE states a series and a volume number, where that series
// already holds a different work at that position. It catches the pair whose
// residual titles disagree - the retailer's decorated title against the modeled
// volume - which the title-author key cannot see.
func detectSeriesVolumeDup(ix *index, f *findings, keysOf map[string][]string, clusterWorks map[string][]string) {
	for _, w := range ix.cat.Works {
		if len(ix.memberships[w.ID]) > 0 {
			continue
		}
		d := ix.derived(w)
		if !d.embedded {
			continue
		}
		form, sid := d.seriesName, d.seriesID
		seq, hasSeq := bareSeq(w.Title, form)
		if !hasSeq {
			continue
		}
		other := ix.positions[sid][seqKey(seq)]
		if other == "" || other == w.ID {
			continue
		}
		ow := ix.workByID[other]
		if ow == nil {
			continue // a dangling membership; S-INTEGRITY reports it
		}
		if authorKey(ow.Authors) != authorKey(w.Authors) || ow.Language != w.Language {
			continue
		}
		if sharesKey(keysOf[w.ID], keysOf[other]) {
			continue // already clustered by title and author
		}
		s := ix.seriesByID[sid]
		fd := Finding{
			Subclass: dupSeriesVolume,
			Key:      pairKey(w.ID, other),
			Works: []WorkRef{
				ix.workRef(w, auditCleanTitle(w.Title, form)),
				ix.workRef(ow, auditCleanTitle(ow.Title, form)),
			},
			Canonical: other,
			Action: fmt.Sprintf("review as one work: %s states %q volume %s in its title, which %s already occupies",
				w.ID, form, seqKey(seq), other),
			Notes: []string{
				"the series-less work names the series in its title; the other work is the modeled member at that position",
			},
		}
		sort.Slice(fd.Works, func(i, j int) bool { return fd.Works[i].ID < fd.Works[j].ID })
		if s != nil {
			fd.Series = []SeriesRef{ix.seriesRef(s)}
		}
		f.add(fd)
		clusterWorks[fd.Key] = sortedUnique([]string{w.ID, other})
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
