package repair

import (
	"fmt"
	"sort"

	"github.com/kodestar/audiosilo-meta/internal/audit"
	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/internal/titlerule"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// works.go applies W-DUP's merge-works proposal: the cluster's losers fold onto the
// canonical work and their slugs are tombstoned.
//
// UNION-PRESERVING is the rule the whole file is written to. A merge may not lose a
// fact: every recording, every identifier, every provenance entry, every genre,
// every credit and every works-community sidecar member on either side is present
// afterwards. Where two records state the SAME field differently the canonical one
// wins and the loser fills only what the canonical does not state - so a merge adds
// information and never overwrites it.
//
// THREE THINGS REFUSE THE WHOLE CLUSTER, and each is checked here independently of
// the audit's own veto (the audit reads the tree at load; this reads the plan, which
// earlier proposals in the same run have already changed):
//
//   - both sides carry the same works-community MEMBER (both have characters, or
//     both have recaps). Which spoiler-gated description belongs to the surviving
//     work is not a mechanical decision, and the alternative - dropping or
//     overwriting one - would destroy the most expensive data in the repository.
//   - the cluster holds two different positions for one book in one series. The
//     catalogue itself is then saying they are different volumes.
//   - a record the proposal names was retired by an earlier proposal in this run.
//
// A recording-key collision refuses NOTHING: two colliding recordings either merge
// (same narrators, no contradicting runtime) or the mover is re-keyed through the
// project's own numbered-slug chain and moved intact. Both outcomes keep every
// recording.
const (
	// runtimeTolerance is how far two runtimes may differ and still be one
	// production. It is the importer's own ASIN-merge guard restated: a >10% gap
	// between two KNOWN runtimes is a genuinely different production. An unstated
	// runtime never disqualifies, exactly as it does not there.
	runtimeTolerance = 1.1
	// maxRecordingRekeys bounds the numbered-slug walk for a colliding recording
	// key. A work with this many recordings of one name is a data problem, not a
	// merge to press on with.
	maxRecordingRekeys = 100
)

// mergeWorks plans one cluster's merge into t.
func (rn *runner) mergeWorks(t *txn, fd audit.Finding) error {
	target, losers := fd.Propose.Target, fd.Propose.Others
	if target == "" || len(losers) == 0 {
		return refusef(catMalformed, "the proposal names no target or no other members")
	}
	for _, slug := range append([]string{target}, losers...) {
		if by, ok := t.p.retiredBy(pack.FamilyWorks, slug); ok {
			return refusef(catRetired, "work %q was retired by an earlier proposal in this run (%s)", slug, by)
		}
	}
	if err := vetoUnaddressableTitles(fd); err != nil {
		return err
	}
	tw, ok, err := t.works.get(target)
	if err != nil {
		return err
	}
	if !ok {
		return refusef(catMissing, "the target work %q is not in the tree", target)
	}
	sorted := append([]string{}, losers...)
	sort.Strings(sorted)
	loserEntries := make([]entry, 0, len(sorted))
	for _, slug := range sorted {
		if slug == target {
			return refusef(catMalformed, "the proposal names %q as both the target and a loser", slug)
		}
		e, ok, err := t.works.get(slug)
		if err != nil {
			return err
		}
		if !ok {
			return refusef(catMissing, "the work %q the proposal folds onto %q is not in the tree", slug, target)
		}
		loserEntries = append(loserEntries, e)
	}

	// The sidecars first: a cluster that cannot take them is refused before
	// anything else is composed, so the report says the one thing that matters
	// about it rather than the first field that happened to differ.
	if err := t.mergeSidecars(target, sorted); err != nil {
		return err
	}
	if err := t.rewriteMemberships(target, sorted); err != nil {
		return err
	}

	merged := tw.clone()
	recs, err := merged.recordings()
	if err != nil {
		return fmt.Errorf("work %q: %w", target, err)
	}
	for i, slug := range sorted {
		if err := t.foldWork(target, slug, merged, recs, loserEntries[i]); err != nil {
			return err
		}
	}
	if err := merged.setRecordings(recs); err != nil {
		return fmt.Errorf("work %q: %w", target, err)
	}
	t.works.set(target, merged)

	for _, slug := range sorted {
		t.works.remove(slug)
		t.retire(pack.FamilyWorks, slug)
		// The slug keeps resolving: it is public API (a meta.audiosilo.app URL, a
		// books.work_id in every audiosilo-sidecars install, a contributed
		// sidecar's work reference), so retiring it without a tombstone is the one
		// thing pkg/redirects exists to prevent.
		t.redirect(model.RedirectWorks, slug, target)
	}
	t.note("retired %d work slug(s) with a redirect onto %s: %s", len(sorted), target, joinList(sorted))
	return nil
}

// vetoUnaddressableTitles refuses a cluster whose members meet only on a comparison
// key that has no identity in it.
//
// It is the one veto this pass adds to the audit's own, and it was found by running
// the pass over the real tree. The audit clusters on titlerule.CompareKey, which folds
// away everything that is not ASCII alphanumeric - so two different Russian novels by
// one pair of authors, "Грани безумия. Том 1" and "Клинком и сердцем, Том 1", both
// reduce to the key "1" and were proposed for merge, non-advisory. The same fold is
// what makes an unaddressable name a refusal in the importer (getOrCreateSeries) and
// in the libex credit gate: a string this project's identity rules keep nothing of
// cannot be evidence that two records are one book.
//
// It is narrow on purpose, and measured: it fires only when NO member's cleaned title
// carries an identity of its own (titlerule.CarriesIdentity, the audit's own
// predicate) AND the cleaned titles are not the same string. Over the 1,846
// non-advisory merge-works proposals of the 279k-work tree it refuses exactly 2, both
// of them the shape above; the legitimate members of that regime ("1984" against
// "1984", "22/11/63" against "22/11/63", "1177 B.C" against "1177 B.C") state
// identical cleaned titles and are untouched. The audit is the better long-term home
// for it - a proposal this pass must not apply should not be non-advisory - which is
// why the refusal names the finding rather than the tree.
func vetoUnaddressableTitles(fd audit.Finding) error {
	if len(fd.Works) < 2 {
		return nil // the fresh audit always cites a cluster's members; nothing to judge without them
	}
	for _, w := range fd.Works {
		if titlerule.CarriesIdentity(w.Cleaned) {
			return nil
		}
	}
	first := fd.Works[0]
	for _, w := range fd.Works[1:] {
		if w.Cleaned != first.Cleaned {
			return refusef(catUnaddressableTitle,
				"%s states the cleaned title %q and %s states %q: neither names a book a rule can identify (both reduce to the same "+
					"comparison key only because the key folds away everything that is not ASCII), so this is two different books "+
					"meeting on a number",
				first.ID, first.Cleaned, w.ID, w.Cleaned)
		}
	}
	return nil
}

// foldWork folds one loser into the merged work entry and its recordings map.
func (t *txn) foldWork(target, loser string, merged entry, recs map[string]entry, lw entry) error {
	lrecs, err := lw.recordings()
	if err != nil {
		return fmt.Errorf("work %q: %w", loser, err)
	}
	for _, key := range sortedKeys(lrecs) {
		if err := t.moveRecording(target, loser, key, recs, lrecs[key]); err != nil {
			return err
		}
	}
	mergeWorkFields(merged, lw)
	return nil
}

// moveRecording places one of a loser's recordings under the merged work: merged
// into a colliding sibling when they are the same production, else moved intact -
// under a new key when the old one is taken.
func (t *txn) moveRecording(target, loser, key string, recs map[string]entry, rec entry) error {
	moved := rec.clone()
	moved.set("work", target)

	sibling, taken := recs[key]
	if !taken {
		recs[key] = moved
		t.note("moved recording %s/%s to %s/%s", loser, key, target, key)
		return nil
	}
	why, same := sameProduction(sibling, moved)
	if same {
		recs[key] = mergeRecordings(sibling, moved)
		t.note("merged recording %s/%s into %s/%s (same production: %s)", loser, key, target, key, why)
		return nil
	}
	newKey, ok := freeRecordingKey(recs, key)
	if !ok {
		return refusef(catRecordingKey,
			"recording %q of %s cannot be moved onto %s: the key is taken and no numbered variant is free within %d tries",
			key, loser, target, maxRecordingRekeys)
	}
	moved.set("id", newKey)
	recs[newKey] = moved
	t.note("re-keyed recording %s/%s as %s/%s and moved it intact (%s)", loser, key, target, newKey, why)
	return nil
}

// sameProduction reports whether two recordings that collide on one key are the
// same production, and says why in either direction.
//
// The bar is the importer's, not a new one: the same narrators, no >10% gap between
// two KNOWN runtimes, and no contradiction between two STATED abridged flags. An
// unstated fact never merges two records on its own and never keeps them apart
// either - it is unstated. Where the evidence does not agree the mover is re-keyed,
// which loses nothing, so the conservative branch is also the cheap one.
func sameProduction(a, b entry) (string, bool) {
	na, nb := a.strs("narrators"), b.strs("narrators")
	if !sameStringSet(na, nb) {
		return fmt.Sprintf("narrators differ: [%s] vs [%s]", joinList(na), joinList(nb)), false
	}
	ra, oka := a.intAt("runtime_min")
	rb, okb := b.intAt("runtime_min")
	if oka && okb && ra > 0 && rb > 0 {
		lo, hi := ra, rb
		if lo > hi {
			lo, hi = hi, lo
		}
		if float64(hi) > runtimeTolerance*float64(lo) {
			return fmt.Sprintf("runtimes %d and %d min differ by more than 10%%", ra, rb), false
		}
	}
	if va, oka := a.boolAt("abridged"); oka {
		if vb, okb := b.boolAt("abridged"); okb && va != vb {
			return "one states abridged and the other states unabridged", false
		}
	}
	return "same narrators" + runtimeEvidence(oka, okb, ra, rb), true
}

// runtimeEvidence renders what the runtimes contributed to a sameProduction answer,
// so a merge note says whether the runtimes agreed or were simply unstated.
func runtimeEvidence(oka, okb bool, ra, rb int) string {
	switch {
	case oka && okb:
		return fmt.Sprintf(", runtimes %d and %d min within 10%%", ra, rb)
	case oka || okb:
		return ", one runtime stated and the other unstated"
	default:
		return ", no runtime stated on either"
	}
}

// mergeRecordings folds mover into keep: identifiers and provenance unioned, every
// field the keeper does not state filled from the mover, and nothing the keeper
// states touched.
//
// added_at is deliberately NOT among the filled fields, on either record kind. It
// records when THIS record entered the database; the mover's own date belongs to a
// record that no longer exists, and the provenance that dates it survives in the
// unioned sources[], which is what metabuild falls back to when added_at is absent.
func mergeRecordings(keep, mover entry) entry {
	out := keep.clone()
	setListOrDrop(out, "asin", unionASINs(out.asins(), mover.asins()))
	setListOrDrop(out, "isbn", unionISBNs(out.isbns(), mover.isbns()))
	out.set("sources", unionSources(out.sources(), mover.sources()))
	setListOrDrop(out, "narrators", appendUnique(out.strs("narrators"), mover.strs("narrators")))
	for _, field := range []string{"release_date", "publisher", "cover_url", "language"} {
		fillAbsentString(out, mover, field)
	}
	// The tri-state and byte-exact members: an absent abridged is "unknown" and a
	// false one is a statement, and a chapter list is a timeline whose numbers must
	// survive exactly as they were written.
	for _, field := range []string{"abridged", "runtime_min", "chapters", "publishers"} {
		fillAbsentRaw(out, mover, field)
	}
	return out
}

// mergeWorkFields folds a loser's work-level facts into the merged work.
//
// authors are UNIONED rather than kept: the identity rule that clustered these two
// records (check.IdentityEqualWorks) matches NESTED author sets, so a loser may
// credit somebody the target does not - the shape that put "June's Wild Flight"
// beside "The Last Kids on Earth: June's Wild Flight" - and dropping them would lose
// a credit the catalogue holds. The target's own order leads, since it is a billing
// order.
func mergeWorkFields(merged, lw entry) {
	setListOrDrop(merged, "authors", appendUnique(merged.strs("authors"), lw.strs("authors")))
	setListOrDrop(merged, "genres", unionGenres(merged.strs("genres"), lw.strs("genres")))
	setListOrDrop(merged, "credits", unionCredits(merged.credits(), lw.credits()))
	merged.set("sources", unionSources(merged.sources(), lw.sources()))
	for _, field := range []string{"subtitle", "language", "first_published", "description"} {
		fillAbsentString(merged, lw, field)
	}
	fillXref(merged, lw)
}

// fillXref fills the merged work's cross-references from a loser's, member by member
// and only where the merged work states nothing, with the print-ISBN list unioned. A
// recorded value is never replaced: two records of one book that disagree about a
// QID are a fact somebody has to look at, not one to overwrite.
func fillXref(merged, lw entry) {
	src, ok := lw["xref"]
	if !ok {
		return
	}
	from, err := decodeEntry(src)
	if err != nil {
		return
	}
	into := entry{}
	if cur, ok := merged["xref"]; ok {
		if into, err = decodeEntry(cur); err != nil {
			return
		}
	}
	for _, k := range sortedKeys(from) {
		if k == "isbn" {
			setListOrDrop(into, "isbn", appendUnique(into.strs("isbn"), from.strs("isbn")))
			continue
		}
		if !into.has(k) {
			into.setRaw(k, from[k])
		}
	}
	if len(into) == 0 {
		merged.drop("xref")
		return
	}
	merged.setRaw("xref", mustRaw(into))
}

// mustRaw renders an entry this package composed. A value it built out of members it
// just decoded cannot fail to marshal, and threading an error here would give every
// caller a refusal branch that no data can reach.
func mustRaw(e entry) []byte {
	raw, err := e.raw()
	if err != nil {
		panic(fmt.Sprintf("repair: composed object does not marshal: %v", err))
	}
	return raw
}

// mergeSidecars moves the cluster's works-community entries onto the target,
// merging DISJOINT members and refusing a member both sides hold.
func (t *txn) mergeSidecars(target string, losers []string) error {
	type holder struct {
		slug string
		e    entry
	}
	var holders []holder
	for _, slug := range append([]string{target}, losers...) {
		e, ok, err := t.community.get(slug)
		if err != nil {
			return err
		}
		if ok {
			holders = append(holders, holder{slug: slug, e: e})
		}
	}
	if len(holders) == 0 {
		return nil
	}
	merged := entry{}
	owner := map[string]string{}
	var moved []string
	for _, h := range holders {
		for _, name := range sortedKeys(h.e) {
			if prev, dup := owner[name]; dup {
				return refusef(catSidecarCollision,
					"%s and %s both carry a %q sidecar for this book: a spoiler-gated %s is community-authored CC BY-SA content, "+
						"so which one describes the surviving work is a human decision - merge the two by hand first",
					prev, h.slug, name, name)
			}
			owner[name] = h.slug
			member, err := decodeEntry(h.e[name])
			if err != nil {
				return fmt.Errorf("works-community entry %q member %q: %w", h.slug, name, err)
			}
			m := member.clone()
			m.set("work", target)
			merged.setRaw(name, mustRaw(m))
			if h.slug != target {
				moved = append(moved, name+" from "+h.slug)
			}
		}
	}
	t.community.set(target, merged)
	for _, h := range holders {
		if h.slug != target {
			t.community.remove(h.slug)
		}
	}
	if len(moved) > 0 {
		t.note("moved works-community sidecar member(s) onto %s: %s", target, joinList(moved))
	}
	return nil
}

// rewriteMemberships re-points every series membership naming a loser at the target,
// dedupes the memberships that then say the same thing, and refuses a series where
// the cluster holds two different positions.
func (t *txn) rewriteMemberships(target string, losers []string) error {
	loser := make(map[string]bool, len(losers))
	for _, l := range losers {
		loser[l] = true
	}
	for _, sid := range t.p.seriesNaming(append([]string{target}, losers...)...) {
		se, ok, err := t.series.get(sid)
		if err != nil {
			return err
		}
		if !ok {
			continue // a dangling membership: pkg/check reports it, and Run refuses a tree it reports on
		}
		var out []model.SeriesWork
		keptPos, keptFrom := "", ""
		changed := false
		for _, sw := range se.seriesWorks() {
			if sw.Work != target && !loser[sw.Work] {
				out = append(out, sw)
				continue
			}
			if keptFrom == "" {
				keptPos, keptFrom = sw.Position, sw.Work
				out = append(out, model.SeriesWork{Work: target, Position: sw.Position})
				changed = changed || sw.Work != target
				continue
			}
			if samePosition(keptPos, sw.Position) {
				changed = true // two memberships saying the same thing collapse to one
				continue
			}
			return refusef(catPositionConflict,
				"series %s holds %s at position %q and %s at position %q: the catalogue itself says these are different volumes",
				sid, keptFrom, keptPos, sw.Work, sw.Position)
		}
		if err := refuseDuplicatePositions(sid, out); err != nil {
			return err
		}
		if changed {
			t.setSeries(sid, se.clone(), out)
			t.note("re-pointed series %s onto %s at position %q", sid, target, keptPos)
		}
	}
	return nil
}

// refuseDuplicatePositions is the backstop under the position rewrite: pkg/check
// refuses two works at one position, so a rewritten list that holds one would fail
// the post-write gate after the tree had been written. The pre-state is
// metacheck-green (Run refuses otherwise), so this is only reachable from a bug in
// the rewrite - which is exactly why it is a refusal and not a comment.
func refuseDuplicatePositions(seriesID string, works []model.SeriesWork) error {
	seen := make(map[string]string, len(works))
	for _, sw := range works {
		if prev, dup := seen[sw.Position]; dup {
			return refusef(catPositionConflict,
				"the rewritten series %s would hold two works at position %q (%s and %s)", seriesID, sw.Position, prev, sw.Work)
		}
		seen[sw.Position] = sw.Work
	}
	return nil
}

// samePosition reports whether two position strings name the same slot. Equal
// strings always do; otherwise they are compared as the audit's own veto compares
// them, through the position grammar (importer.NormalizeSequence for acceptance,
// model.ParsePositionRange for the span), so "02" and "2" are one slot while "2",
// "2.5" and "1-3.5" stay three.
func samePosition(a, b string) bool {
	if a == b {
		return true
	}
	sa, oka := positionSpan(a)
	sb, okb := positionSpan(b)
	return oka && okb && sa == sb
}

func positionSpan(pos string) ([2]float64, bool) {
	norm, ok := importer.NormalizeSequence(pos)
	if !ok {
		return [2]float64{}, false
	}
	lo, hi, ok := model.ParsePositionRange(norm)
	if !ok {
		return [2]float64{}, false
	}
	return [2]float64{lo, hi}, true
}

// freeRecordingKey walks the project's numbered-slug chain for a colliding
// recording key and returns the first free one. It is importer.NumberedSlugAt, the
// one implementation of that formula, so a recording this pass re-keys sits where
// the importer's own collision chain would have put it.
func freeRecordingKey(recs map[string]entry, base string) (string, bool) {
	for i := 1; i <= maxRecordingRekeys; i++ {
		key := importer.NumberedSlugAt(base, i)
		if _, taken := recs[key]; taken || !model.ValidSlug(key) {
			continue
		}
		return key, true
	}
	return "", false
}

// sameStringSet compares two lists as sets, which is what a narrator comparison is:
// the order two importers wrote a cast in is not a fact about the production.
func sameStringSet(a, b []string) bool {
	sa, sb := sortedSet(a), sortedSet(b)
	if len(sa) != len(sb) {
		return false
	}
	for i := range sa {
		if sa[i] != sb[i] {
			return false
		}
	}
	return true
}

func sortedSet(ss []string) []string {
	seen := make(map[string]bool, len(ss))
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
