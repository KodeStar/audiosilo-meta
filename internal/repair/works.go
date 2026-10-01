package repair

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/kodestar/audiosilo-meta/internal/audit"
	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/internal/rawentry"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// works.go applies W-DUP's merge-works proposal: the cluster's losers fold onto the
// canonical work and their slugs are tombstoned.
//
// WHAT A MERGE PRESERVES, stated exactly, because the first version of this comment
// claimed more than the code does. A merge loses no RECORD and no SET-VALUED fact: every
// recording, every identifier, every provenance entry, every genre, every credit and every
// works-community sidecar member on either side is present afterwards, and each is unioned
// on the very key pkg/check enforces uniqueness with.
//
// A POSITIONAL fact - one where the two records state the same field differently and only
// one value can survive - is CHOSEN, and every choice is reported. The canonical record's
// own scalars win (it is the record a human reviewed as the survivor) and the loser fills
// only what the canonical does not state; a chapter LIST is chosen by length rather than by
// which side happened to be the keeper, because a longer list is strictly more evidence
// about the same production. Every value dropped that way is named in the applied record's
// notes, which is the audit trail for a pass that deletes records: a measured 386-merge wave
// discarded 48 cover URLs, 39 publishers, 39 release dates, 21 runtimes and 40 chapter lists
// with no note at all, 4 of those lists richer than the one kept.
//
// THREE THINGS REFUSE THE WHOLE CLUSTER, and each is checked here independently of
// the audit's own veto (the audit reads the tree at load; this reads the plan, which
// earlier proposals in the same run have already changed):
//
//   - both sides carry the same works-community MEMBER (both have characters, or
//     both have recaps, or both have a description). Which community-authored text
//     belongs to the surviving work is not a mechanical decision, and the
//     alternative - dropping or overwriting one - would destroy the most expensive
//     data in the repository. The rule is written over the member NAMES the entries
//     hold, so it covered the description member the day the model gained it.
//   - the cluster holds two different positions for one book in one series. The
//     catalogue itself is then saying they are different volumes.
//   - a record the proposal names was retired by an earlier proposal in this run.
//
// A merge that would leave a translation_of link breaking pkg/check's rules - a two-hop
// chain, a translation in its original's language - refuses it too (CatTranslationLink):
// see links.go, which also re-points the links OTHER works hold onto a loser, so none is
// left naming a retired slug.
//
// A recording-key collision refuses NOTHING: two colliding recordings either merge
// (same narrators, no contradicting runtime) or the mover is re-keyed through the
// project's own numbered-slug chain and moved intact. Both outcomes keep every
// recording.
//
// The same-production evidence a colliding recording key is judged by is the
// IMPORTER's, called rather than restated (see rawentry.SameProduction).

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
	retiring := importer.ToSet(sorted)
	if err := t.rewriteMemberships(target, sorted, retiring); err != nil {
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
	// translation_of: the losers' links union onto the survivor and every work naming a
	// loser is re-pointed (links.go), so no link is left naming a retired slug.
	if err := t.relinkTranslations(pack.FamilyWorks, target, merged, sorted, loserEntries, retiring); err != nil {
		return err
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
	// The staged merge is held to pkg/check's link rules (links.go): a re-point can chain
	// a translation, and a survivor's language is what its new links are judged in.
	return t.refuseLinkFaults(target)
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
	t.noteLost(mergeWorkFields(merged, lw), loser)
	return nil
}

// noteLost names every value a merge chose away, one indented line each, against the
// record it came from - the one note format every merge op writes its audit trail in.
func (t *txn) noteLost(lost []mergedFacts, from string) {
	for _, f := range lost {
		t.note("  %s: kept %s, dropped %s from %s", f.field, quoteFact(f.kept), quoteFact(f.dropped), from)
	}
}

// factValue renders a raw member for a note: the VALUE of a JSON string, the bytes of
// anything else. Without it a dropped QID reads as `"\"Q222\""` - the note is for a human.
func factValue(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

// quoteFact renders a value for a note, shortened so one dropped description cannot make a
// record unreadable. A value that is already a count ("42 chapters") is left alone.
func quoteFact(v string) string {
	const max = 80
	if len(v) > max {
		return fmt.Sprintf("%q... (%d bytes)", v[:max], len(v))
	}
	return fmt.Sprintf("%q", v)
}

// moveRecording places one of a loser's recordings under the merged work: merged
// into a colliding sibling when they are the same production, else moved intact -
// under a new key when the old one is taken.
func (t *txn) moveRecording(target, loser, key string, recs map[string]entry, rec entry) error {
	move, ok := rawentry.MoveRecording(recs, rec, target, key, []string{key}, func() (string, bool) {
		return freeRecordingKey(recs, key)
	})
	if !ok {
		return refusef(CatRecordingKey,
			"recording %q of %s cannot be moved onto %s: the key is taken and no numbered variant is free within %d tries",
			key, loser, target, maxRecordingRekeys)
	}
	switch {
	case move.Merged:
		t.note("merged recording %s/%s into %s/%s (same production: %s)", loser, key, target, key, move.Why)
		for _, f := range move.Lost {
			t.noteLost([]mergedFacts{{field: f.Field, kept: f.Kept, dropped: f.Dropped}}, loser+"/"+key)
		}
	case move.ID != key:
		t.note("re-keyed recording %s/%s as %s/%s and moved it intact (%s)", loser, key, target, move.ID, move.Why)
	default:
		t.note("moved recording %s/%s to %s/%s", loser, key, target, key)
	}
	return nil
}

type mergedFacts struct {
	field   string
	kept    string
	dropped string
}

func sameRawMember(a, b entry, field string) bool { return string(a[field]) == string(b[field]) }

// mergeWorkFields folds a loser's work-level facts into the merged work.
//
// authors are UNIONED rather than kept: the identity rule that clustered these two
// records (check.IdentityEqualWorks) matches NESTED author sets, so a loser may
// credit somebody the target does not - the shape that put "June's Wild Flight"
// beside "The Last Kids on Earth: June's Wild Flight" - and dropping them would lose
// a credit the catalogue holds. The target's own order leads, since it is a billing
// order.
func mergeWorkFields(merged, lw entry) []mergedFacts {
	rawentry.SetListOrDrop(merged, "authors", rawentry.AppendUnique(merged.Strs("authors"), lw.Strs("authors")))
	rawentry.SetListOrDrop(merged, "genres", rawentry.UnionGenres(merged.Strs("genres"), lw.Strs("genres")))
	rawentry.SetListOrDrop(merged, "credits", rawentry.UnionCredits(merged.Credits(), lw.Credits()))
	merged.Set("sources", rawentry.UnionSources(merged.Sources(), lw.Sources()))
	lost := fillStrings(merged, lw, "subtitle", "language", "first_published", "description")
	return append(lost, fillXref(merged, lw)...)
}

// fillStrings is merge-works' rule for string scalars, field by field: a value only
// `from` states is filled into `into`, and a value both state DIFFERENTLY (compared
// exactly) stays as `into` has it and is returned, for the note naming what was chosen
// away. Nothing is chosen when `from` states nothing or the two agree.
func fillStrings(into, from entry, fields ...string) []mergedFacts {
	var lost []mergedFacts
	for _, field := range fields {
		if rawentry.FillAbsentString(into, from, field) {
			continue // the keeper stated nothing, so nothing was chosen away
		}
		if v := from.Str(field); v != "" && v != into.Str(field) {
			lost = append(lost, mergedFacts{field: field, kept: into.Str(field), dropped: v})
		}
	}
	return lost
}

// fillXref fills the merged work's cross-references from a loser's, member by member
// and only where the merged work states nothing, with the print-ISBN list unioned. A
// recorded value is never replaced: two records of one book that disagree about a
// QID are a fact somebody has to look at, not one to overwrite.
func fillXref(merged, lw entry) []mergedFacts {
	src, ok := lw["xref"]
	if !ok {
		return nil
	}
	from, err := rawentry.Decode(src)
	if err != nil {
		return nil
	}
	into := entry{}
	if cur, ok := merged["xref"]; ok {
		if into, err = rawentry.Decode(cur); err != nil {
			return nil
		}
	}
	var lost []mergedFacts
	for _, k := range rawentry.SortedKeys(from) {
		if k == "isbn" {
			rawentry.SetListOrDrop(into, "isbn", rawentry.AppendUnique(into.Strs("isbn"), from.Strs("isbn")))
			continue
		}
		if !into.Has(k) {
			into.SetRaw(k, from[k])
			continue
		}
		// Two records disagreeing about a QID is a fact somebody has to look at, and the
		// surviving one is the reviewed record's - but the other must not vanish unrecorded.
		if !sameRawMember(into, from, k) {
			lost = append(lost, mergedFacts{field: "xref." + k, kept: factValue(into[k]), dropped: factValue(from[k])})
		}
	}
	if len(into) == 0 {
		merged.Drop("xref")
		return lost
	}
	merged.SetRaw("xref", into.MustRaw())
	return lost
}

// mergeSidecars moves the cluster's works-community entries onto the target,
// merging DISJOINT members and refusing a member both sides hold.
//
// It is also the ONE place a merge can learn that it must not proceed at all. The
// collision refusal below is the human-decision guard over the CC BY-SA layer, and
// after the community-repo split this repository cannot answer the question it is
// made of - so a run that cannot see the sidecars refuses the merge outright
// rather than merging blind (see sidecarSource). Nothing is inferred from silence.
func (t *txn) mergeSidecars(target string, losers []string) error {
	if t.community.sidecar() == sidecarUnknown {
		return refusef(CatCommunityRequired,
			"this tree does not hold the works-community family, so whether %s or any of %s carries a characters, recaps or "+
				"description sidecar cannot be answered here - and folding two works that both carry one loses "+
				"community-authored CC BY-SA content. Re-run with --community <community-checkout>/data "+
				"(KodeStar/audiosilo-meta-community) so the collision check can see them",
			target, joinList(losers))
	}
	type holder struct {
		slug string
		e    entry
	}
	var holders []holder
	for _, slug := range audit.Cluster(target, losers) {
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
					"%s and %s both carry a %q sidecar for this book: a %s member is community-authored CC BY-SA content, "+
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
	// Staged in both modes, written in one - view.queue owns that decision and
	// its rationale.
	t.community.put(target, merged)
	for _, h := range holders {
		if h.slug != target {
			t.community.remove(h.slug)
		}
	}
	if len(moved) > 0 {
		if t.community.sidecar() == sidecarReadOnly {
			// Said plainly, because the operator has to know a follow-up exists: the
			// members stay where they are, keyed by a slug this merge retires, and
			// they reach the surviving work through the tombstone - at build time via
			// check.LoadComposed's re-key (which warns on every ride), durably via the
			// community repository's own re-key sweep.
			t.note("works-community sidecar member(s) for %s stay in the community repository and ride the slug redirect until "+
				"its re-key sweep lands: %s", target, joinList(moved))
		} else {
			t.note("moved works-community sidecar member(s) onto %s: %s", target, joinList(moved))
		}
	}
	return nil
}

// rewriteMemberships re-points every series membership naming a loser at the target,
// dedupes the memberships that then say the same thing, and refuses a series where
// the cluster holds two different positions.
func (t *txn) rewriteMemberships(target string, losers []string, loser map[string]bool) error {
	for _, sid := range t.p.seriesNaming(audit.Cluster(target, losers)...) {
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
			if importer.SameSlot(keptPos, sw.Position) {
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
// accepts the value, else the raw string. It is importer.SameSlot in the shape a lookup
// needs - the same rule, because a map is how both merge paths ask "is this slot taken",
// and a raw-string key let "3" and "03" through as two slots. pkg/check compares the
// strings too, so such a tree stays GREEN with two works at one place in the order.
func slotKey(pos string) string {
	span, ok := importer.PositionSpan(pos)
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
