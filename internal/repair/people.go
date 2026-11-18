package repair

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"

	"github.com/kodestar/audiosilo-meta/internal/audit"
	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/internal/rawentry"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// people.go applies merge-people: one person's two (or more) records fold onto the
// target, every credit naming a loser is re-pointed at the target, and the losers'
// slugs are tombstoned in the people namespace. No detector proposes it (P-DUP is
// advisory throughout); a reviewed assertion in internal/audit/reviewed.json sources
// it, so a person fold is always a human's decision about identity.
//
// WHAT MOVES. A person is named by slug in six places, pkg/check's forEachPersonRef
// enumeration: a work's authors and credits, a recording's narrators, credits and
// chapter readers, and a series' authors. Each list
// re-points the loser to the target and drops the repeat that leaves (a work two
// records of one author were both credited on lists them once, in the first-seen
// order), and a credit list is re-sorted into its canonical order. Nothing else
// names a person: the community layer names works, never people.
//
// WHAT THE RECORD KEEPS. The target's own statements win, as a work merge's do, and
// a loser fills only what the target does not state: sort_name, kind, description,
// the life years, the xref members. Every value chosen away is named in the notes.
// sources union. A LibriVox id is identity, so a loser's id the target does not
// state becomes the target's own, and one it states differently joins its alias list
// (model.Person.LibrivoxAuthorAliases): the assertion is the evidenced decision the
// alias rule asks for, and dropping the id would let the next LibriVox import mint
// the loser again.
//
// The referencing records are found through the plan's person index
// (plan.personIndex), which is the catalogue at load with every earlier fold of the
// run folded into it; a work or series an earlier proposal retired is read at its
// survivor.

// mergePeople plans one person fold into t.
func (rn *runner) mergePeople(t *txn, fd audit.Finding) error {
	target := fd.Propose.Target
	te, losers, loserEntries, err := t.loadCluster(pack.FamilyPeople, "person", fd.Propose)
	if err != nil {
		return err
	}
	merged := te.Clone()
	for i, slug := range losers {
		mergePersonFields(t, merged, loserEntries[i], slug)
	}
	t.people.put(target, merged)
	retiring := importer.ToSet(losers)

	works, series := t.p.personIndex().recordsNaming(losers, t.p.redirects)
	var reWorks, reRecs, reSeries int
	for _, slug := range works {
		e, ok, err := t.works.get(slug)
		if err != nil {
			return err
		}
		if !ok {
			continue // an earlier proposal of the run deleted it; nothing names the person there now
		}
		next := e.Clone()
		changed := repointPeople(next, "authors", retiring, target)
		recs, err := next.Recordings()
		if err != nil {
			return fmt.Errorf("work %q: %w", slug, err)
		}
		for _, key := range rawentry.SortedKeys(recs) {
			rec := recs[key].Clone()
			changedRec := repointPeople(rec, "narrators", retiring, target)
			readers, err := repointChapterReaders(rec, retiring, target)
			if err != nil {
				return fmt.Errorf("work %q recording %q: %w", slug, key, err)
			}
			if changedRec {
				dropped, err := dropSoleNarratorReaders(rec)
				if err != nil {
					return fmt.Errorf("work %q recording %q: %w", slug, key, err)
				}
				if dropped {
					t.note("  dropped the chapter readers of %s/%s: its narrators folded into one, who reads every chapter", slug, key)
				}
			}
			if changedRec || readers {
				recs[key] = rec
				reRecs++
				changed = true
			}
		}
		if !changed {
			continue
		}
		if err := next.SetRecordings(recs); err != nil {
			return fmt.Errorf("work %q: %w", slug, err)
		}
		t.works.put(slug, next)
		reWorks++
	}
	for _, slug := range series {
		e, ok, err := t.series.get(slug)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		next := e.Clone()
		if repointList(next, "authors", retiring, target) {
			t.series.put(slug, next)
			reSeries++
		}
	}
	t.note("re-credited %d work(s), %d of their recording(s) and %d series from %s onto %s",
		reWorks, reRecs, reSeries, joinList(losers), target)

	for _, slug := range losers {
		t.people.remove(slug)
		t.retire(pack.FamilyPeople, slug)
		// A person slug is public API (meta.audiosilo.app/people/<slug>, and every
		// credit an older artifact or a client recorded), so it keeps resolving.
		t.redirect(model.RedirectPeople, slug, target)
	}
	t.refold = append(t.refold, personFold{target: target, losers: losers})
	t.note("retired %d person slug(s) with a redirect onto %s: %s", len(losers), target, joinList(losers))
	return nil
}

// mergePersonFields folds one loser's person-level facts into merged (see the file
// comment for the rule), noting every value chosen away.
func mergePersonFields(t *txn, merged, le entry, loser string) {
	merged.Set("sources", rawentry.UnionSources(merged.Sources(), le.Sources()))
	t.noteLost(fillStrings(merged, le, "name", "sort_name", "kind", "description"), loser)
	for _, field := range []string{"birth_year", "death_year"} {
		if rawentry.FillAbsentRaw(merged, le, field) || !le.Has(field) || sameRawMember(merged, le, field) {
			continue
		}
		t.noteLost([]mergedFacts{{field: field, kept: factValue(merged[field]), dropped: factValue(le[field])}}, loser)
	}
	t.noteLost(fillXref(merged, le), loser)
	for _, ns := range []struct{ id, aliases string }{
		{"librivox_reader_id", "librivox_reader_aliases"},
		{"librivox_author_id", "librivox_author_aliases"},
	} {
		aliases := rawentry.DecodeOr[[]int](le[ns.aliases])
		if id, ok := le.IntAt(ns.id); ok && id > 0 {
			if cur, held := merged.IntAt(ns.id); !held || cur <= 0 {
				merged.Set(ns.id, id)
			} else if cur != id {
				aliases = append(aliases, id)
				t.note("  %s: kept %d, recorded %s's %d as an alias", ns.id, cur, loser, id)
			}
		}
		if len(aliases) == 0 {
			continue
		}
		own, _ := merged.IntAt(ns.id)
		all := slices.DeleteFunc(slices.Concat(rawentry.DecodeOr[[]int](merged[ns.aliases]), aliases), func(v int) bool { return v == own })
		slices.Sort(all)
		rawentry.SetListOrDrop(merged, ns.aliases, slices.Compact(all))
	}
}

// repointList rewrites every member of e's field naming a retiring person to target,
// dropping the repeat that leaves, and reports whether anything changed.
func repointList(e entry, field string, retiring map[string]bool, target string) bool {
	list := e.Strs(field)
	if !slices.ContainsFunc(list, func(s string) bool { return retiring[s] }) {
		return false
	}
	out := make([]string, 0, len(list))
	for _, s := range list {
		if retiring[s] {
			s = target
		}
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	rawentry.SetListOrDrop(e, field, out)
	return true
}

// repointPeople re-points e's slug list field and its credits, reporting whether
// either changed.
func repointPeople(e entry, field string, retiring map[string]bool, target string) bool {
	list := repointList(e, field, retiring, target)
	credits := repointCredits(e, retiring, target)
	return list || credits
}

// repointCredits rewrites e's credits naming a retiring person to target, dropping a
// (person, role) pair that repeats, in canonical order.
func repointCredits(e entry, retiring map[string]bool, target string) bool {
	credits := e.Credits()
	if !slices.ContainsFunc(credits, func(c model.Credit) bool { return retiring[c.Person] }) {
		return false
	}
	for i := range credits {
		if retiring[credits[i].Person] {
			credits[i].Person = target
		}
	}
	rawentry.SetListOrDrop(e, "credits", rawentry.UnionCredits(nil, credits))
	return true
}

// repointChapterReaders rewrites the readers of rec's chapters naming a retiring
// person, as repointList does a list, and reports whether any changed. Only the
// readers member of a chapter is re-rendered; every other member, the timeline's
// numbers included, is carried as the bytes it was.
func repointChapterReaders(rec entry, retiring map[string]bool, target string) (bool, error) {
	raw := rec["chapters"]
	// Most folds name no chapter reader: skip decoding every chapter when no retiring
	// slug appears in the bytes at all.
	if !namesAny(raw, retiring) {
		return false, nil
	}
	var chapters []entry
	if err := json.Unmarshal(raw, &chapters); err != nil {
		return false, err
	}
	touched := false
	for _, ch := range chapters {
		if repointList(ch, "readers", retiring, target) {
			touched = true
		}
	}
	if touched {
		rec.Set("chapters", chapters)
	}
	return touched, nil
}

// dropSoleNarratorReaders removes the readers of every chapter of rec when the fold
// left it one narrator and every chapter lists readers, and reports whether it did.
// pkg/check refuses that shape (checkLibrivox: no readers on a one-narrator recording
// already says they read every chapter), and a LibriVox recording read by two records
// of one reader is exactly how a person fold reaches it.
func dropSoleNarratorReaders(rec entry) (bool, error) {
	raw, ok := rec["chapters"]
	if !ok || len(rec.Strs("narrators")) != 1 {
		return false, nil
	}
	var chapters []entry
	if err := json.Unmarshal(raw, &chapters); err != nil {
		return false, err
	}
	if len(chapters) == 0 || slices.ContainsFunc(chapters, func(ch entry) bool { return len(ch.Strs("readers")) == 0 }) {
		return false, nil
	}
	for _, ch := range chapters {
		delete(ch, "readers")
	}
	rec.Set("chapters", chapters)
	return true, nil
}

// namesAny reports whether raw holds any of slugs as a JSON string.
func namesAny(raw []byte, slugs map[string]bool) bool {
	for slug := range slugs {
		if bytes.Contains(raw, []byte(strconv.Quote(slug))) {
			return true
		}
	}
	return false
}

// personFold is a committed person fold, kept on the txn so the plan's person index
// learns it at commit (and not before: a refused txn must leave the index alone).
type personFold struct {
	target string
	losers []string
}

// personIndex maps a person slug to the work and series slugs that name it, from the
// catalogue at load. A fold the run commits folds the losers' sets into the target's,
// so a later fold of that target reaches every record an earlier one re-credited.
type personIndex struct {
	works, series map[string]map[string]bool
}

// addRef records that slug names person in m.
func addRef(m map[string]map[string]bool, person, slug string) {
	if m[person] == nil {
		m[person] = map[string]bool{}
	}
	m[person][slug] = true
}

func newPersonIndex(cat *model.Catalog) *personIndex {
	ix := &personIndex{works: map[string]map[string]bool{}, series: map[string]map[string]bool{}}
	for _, w := range cat.Works {
		for _, a := range w.Authors {
			addRef(ix.works, a, w.ID)
		}
		for _, c := range w.Credits {
			addRef(ix.works, c.Person, w.ID)
		}
		for _, r := range w.Recordings {
			for _, n := range r.Narrators {
				addRef(ix.works, n, w.ID)
			}
			for _, c := range r.Credits {
				addRef(ix.works, c.Person, w.ID)
			}
			for _, ch := range r.Chapters {
				for _, rd := range ch.Readers {
					addRef(ix.works, rd, w.ID)
				}
			}
		}
	}
	for _, s := range cat.Series {
		for _, a := range s.Authors {
			addRef(ix.series, a, s.ID)
		}
	}
	return ix
}

// recordsNaming returns, sorted, the works and series naming any of people, each read
// at its survivor when an earlier proposal of the run retired it.
func (ix *personIndex) recordsNaming(people []string, reds model.Redirects) (works, series []string) {
	collect := func(m map[string]map[string]bool, kind model.RedirectKind) []string {
		set := map[string]bool{}
		for _, p := range people {
			for slug := range m[p] {
				set[reds.Live(kind, slug)] = true
			}
		}
		return rawentry.SortedKeys(set)
	}
	return collect(ix.works, model.RedirectWorks), collect(ix.series, model.RedirectSeries)
}

// fold records a committed fold.
func (ix *personIndex) fold(f personFold) {
	for _, m := range []map[string]map[string]bool{ix.works, ix.series} {
		for _, l := range f.losers {
			for slug := range m[l] {
				addRef(m, f.target, slug)
			}
		}
	}
}
