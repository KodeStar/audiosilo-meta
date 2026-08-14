package repair

import (
	"fmt"
	"strconv"

	"github.com/kodestar/audiosilo-meta/internal/audit"
	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/internal/rawentry"
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
//
// The same-production evidence a colliding recording key is judged by is the
// IMPORTER's, called rather than restated (see sameProduction).

// maxRecordingRekeys bounds the numbered-slug walk for a colliding recording key. A
// work with this many recordings of one name is a data problem, not a merge to press on
// with.
const maxRecordingRekeys = 100

// mergeWorks plans one cluster's merge into t.
func (rn *runner) mergeWorks(t *txn, fd audit.Finding) error {
	target := fd.Propose.Target
	tw, sorted, loserEntries, err := t.loadCluster(pack.FamilyWorks, "work", fd.Propose)
	if err != nil {
		return err
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

	merged := tw.Clone()
	recs, err := merged.Recordings()
	if err != nil {
		return fmt.Errorf("work %q: %w", target, err)
	}
	for i, slug := range sorted {
		if err := t.foldWork(target, slug, merged, recs, loserEntries[i]); err != nil {
			return err
		}
	}
	if err := merged.SetRecordings(recs); err != nil {
		return fmt.Errorf("work %q: %w", target, err)
	}
	t.works.put(target, merged)

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

// foldWork folds one loser into the merged work entry and its recordings map.
func (t *txn) foldWork(target, loser string, merged entry, recs map[string]entry, lw entry) error {
	lrecs, err := lw.Recordings()
	if err != nil {
		return fmt.Errorf("work %q: %w", loser, err)
	}
	for _, key := range rawentry.SortedKeys(lrecs) {
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
	moved := rec.Clone()
	moved.Set("work", target)

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
		return refusef(CatRecordingKey,
			"recording %q of %s cannot be moved onto %s: the key is taken and no numbered variant is free within %d tries",
			key, loser, target, maxRecordingRekeys)
	}
	moved.Set("id", newKey)
	recs[newKey] = moved
	t.note("re-keyed recording %s/%s as %s/%s and moved it intact (%s)", loser, key, target, newKey, why)
	return nil
}

// sameProduction reports whether two recordings that collide on one key are the same
// production, and says why in either direction.
//
// EVERY RULE HERE IS THE IMPORTER'S, called rather than restated. That is not tidiness:
// this pass asks the very question the ASIN-merge guard asks, and the first draft
// restated both halves and got both boundaries wrong - "the larger is at most 1.1x the
// smaller" instead of within-10%-of-the-larger, and an abridged refusal that needed
// BOTH sides to state the flag, where the importer reads an ABSENT flag as unabridged
// so an abridgement never folds into an unstated recording. Where the evidence does not
// agree the mover is re-keyed, which loses nothing, so the conservative branch is also
// the cheap one.
func sameProduction(a, b entry) (string, bool) {
	na, nb := a.Strs("narrators"), b.Strs("narrators")
	if !importer.SameSet(importer.ToSet(na), importer.ToSet(nb)) {
		return fmt.Sprintf("narrators differ: [%s] vs [%s]", joinList(na), joinList(nb)), false
	}
	ra, _ := a.IntAt("runtime_min")
	rb, _ := b.IntAt("runtime_min")
	if !importer.RuntimesCompatible(ra, rb) {
		return fmt.Sprintf("runtimes %d and %d min are more than 10%% apart", ra, rb), false
	}
	if importer.AbridgedConflict(a.BoolPtr("abridged"), b.BoolPtr("abridged")) {
		return "the abridged flags disagree (an absent flag reads as unabridged, so an abridgement never folds into one)", false
	}
	return "same narrators" + runtimeEvidence(ra, rb), true
}

// runtimeEvidence renders what the runtimes contributed to a sameProduction answer, so
// a merge note says whether they agreed or were simply unstated. A runtime of 0 or less
// is "unknown", which is the same reading RuntimesCompatible gives it.
func runtimeEvidence(ra, rb int) string {
	switch {
	case ra > 0 && rb > 0:
		return fmt.Sprintf(", runtimes %d and %d min within 10%%", ra, rb)
	case ra > 0 || rb > 0:
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
	out := keep.Clone()
	rawentry.SetListOrDrop(out, "asin", rawentry.UnionASINs(out.ASINs(), mover.ASINs()))
	rawentry.SetListOrDrop(out, "isbn", rawentry.UnionISBNs(out.ISBNs(), mover.ISBNs()))
	out.Set("sources", rawentry.UnionSources(out.Sources(), mover.Sources()))
	rawentry.SetListOrDrop(out, "narrators", rawentry.AppendUnique(out.Strs("narrators"), mover.Strs("narrators")))
	for _, field := range []string{"release_date", "publisher", "cover_url", "language"} {
		rawentry.FillAbsentString(out, mover, field)
	}
	// The tri-state and byte-exact members: an absent abridged is "unknown" and a
	// false one is a statement, and a chapter list is a timeline whose numbers must
	// survive exactly as they were written.
	for _, field := range []string{"abridged", "runtime_min", "chapters", "publishers"} {
		rawentry.FillAbsentRaw(out, mover, field)
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
	rawentry.SetListOrDrop(merged, "authors", rawentry.AppendUnique(merged.Strs("authors"), lw.Strs("authors")))
	rawentry.SetListOrDrop(merged, "genres", rawentry.UnionGenres(merged.Strs("genres"), lw.Strs("genres")))
	rawentry.SetListOrDrop(merged, "credits", rawentry.UnionCredits(merged.Credits(), lw.Credits()))
	merged.Set("sources", rawentry.UnionSources(merged.Sources(), lw.Sources()))
	for _, field := range []string{"subtitle", "language", "first_published", "description"} {
		rawentry.FillAbsentString(merged, lw, field)
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
	from, err := rawentry.Decode(src)
	if err != nil {
		return
	}
	into := entry{}
	if cur, ok := merged["xref"]; ok {
		if into, err = rawentry.Decode(cur); err != nil {
			return
		}
	}
	for _, k := range rawentry.SortedKeys(from) {
		if k == "isbn" {
			rawentry.SetListOrDrop(into, "isbn", rawentry.AppendUnique(into.Strs("isbn"), from.Strs("isbn")))
			continue
		}
		if !into.Has(k) {
			into.SetRaw(k, from[k])
		}
	}
	if len(into) == 0 {
		merged.Drop("xref")
		return
	}
	merged.SetRaw("xref", into.MustRaw())
}

// mergeSidecars moves the cluster's works-community entries onto the target,
// merging DISJOINT members and refusing a member both sides hold.
func (t *txn) mergeSidecars(target string, losers []string) error {
	type holder struct {
		slug string
		e    entry
	}
	var holders []holder
	for _, slug := range cluster(target, losers) {
		e, ok, err := t.community.get(slug)
		if err != nil {
			return err
		}
		if ok {
			holders = append(holders, holder{slug: slug, e: e})
		}
	}
	// Nothing to move when the target is the only holder (or there is none): staging its
	// own entry back unchanged would queue a rewrite of a works-community pack per merge,
	// and those are the packs the sidecar layer lives in.
	if len(holders) == 0 || (len(holders) == 1 && holders[0].slug == target) {
		return nil
	}
	merged := entry{}
	owner := map[string]string{}
	var moved []string
	for _, h := range holders {
		for _, name := range rawentry.SortedKeys(h.e) {
			if prev, dup := owner[name]; dup {
				return refusef(CatSidecarCollision,
					"%s and %s both carry a %q sidecar for this book: a spoiler-gated %s is community-authored CC BY-SA content, "+
						"so which one describes the surviving work is a human decision - merge the two by hand first",
					prev, h.slug, name, name)
			}
			owner[name] = h.slug
			member, err := rawentry.Decode(h.e[name])
			if err != nil {
				return fmt.Errorf("works-community entry %q member %q: %w", h.slug, name, err)
			}
			m := member.Clone()
			m.Set("work", target)
			merged.SetRaw(name, m.MustRaw())
			if h.slug != target {
				moved = append(moved, name+" from "+h.slug)
			}
		}
	}
	t.community.put(target, merged)
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
	for _, sid := range t.p.seriesNaming(cluster(target, losers)...) {
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
		for _, sw := range se.SeriesWorks() {
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
			if titlerule.SameSlot(keptPos, sw.Position) {
				changed = true // two memberships saying the same thing collapse to one
				continue
			}
			return refusef(CatPositionConflict,
				"series %s holds %s at position %q and %s at position %q: the catalogue itself says these are different volumes",
				sid, keptFrom, keptPos, sw.Work, sw.Position)
		}
		if err := refuseDuplicatePositions(sid, out); err != nil {
			return err
		}
		if changed {
			t.setSeries(sid, se.Clone(), out)
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
		key := slotKey(sw.Position)
		if prev, dup := seen[key]; dup {
			return refusef(CatPositionConflict,
				"the rewritten series %s would hold two works at position %q (%s and %s)", seriesID, sw.Position, prev, sw.Work)
		}
		seen[key] = sw.Work
	}
	return nil
}

// slotKey is a position's SLOT identity as a MAP KEY: the canonical span when the grammar
// accepts the value, else the raw string. It is titlerule.SameSlot in the shape a lookup
// needs - the same rule, because a map is how both merge paths ask "is this slot taken",
// and a raw-string key let "3" and "03" through as two slots. pkg/check compares the
// strings too, so such a tree stays GREEN with two works at one place in the order.
func slotKey(pos string) string {
	span, ok := titlerule.PositionSpan(pos)
	if !ok {
		return "raw:" + pos
	}
	return strconv.FormatFloat(span[0], 'f', -1, 64) + "-" + strconv.FormatFloat(span[1], 'f', -1, 64)
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
