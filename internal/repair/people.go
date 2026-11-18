package repair

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/kodestar/audiosilo-meta/internal/audit"
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
// The referencing records are found through the plan's person index (plan.personRefs),
// which is the catalogue at load with every earlier fold of the run folded into it; a
// work or series an earlier proposal retired is read at its survivor.

// personListFields are the members of a work, recording or series entry that list
// person slugs, by the family the entry lives in.
var (
	workPersonLists   = []string{"authors"}
	seriesPersonLists = []string{"authors"}
	recPersonLists    = []string{"narrators"}
)

// mergePeople plans one person fold into t.
func (rn *runner) mergePeople(t *txn, fd audit.Finding) error {
	target := fd.Propose.Target
	te, losers, loserEntries, err := t.loadCluster(pack.FamilyPeople, "person", fd.Propose)
	if err != nil {
		return err
	}
	merged := te.Clone()
	retiring := map[string]bool{}
	for i, slug := range losers {
		mergePersonFields(t, merged, loserEntries[i], slug)
		retiring[slug] = true
	}
	t.people.put(target, merged)

	works, series := t.p.personRefs.recordsNaming(losers, t.p.redirects)
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
		changed := repointLists(next, workPersonLists, retiring, target)
		changed = repointCredits(next, retiring, target) || changed
		recs, err := next.Recordings()
		if err != nil {
			return fmt.Errorf("work %q: %w", slug, err)
		}
		for _, key := range rawentry.SortedKeys(recs) {
			rec := recs[key].Clone()
			changedRec := repointLists(rec, recPersonLists, retiring, target)
			changedRec = repointCredits(rec, retiring, target) || changedRec
			if changedRec, err = repointChapterReaders(rec, retiring, target, changedRec); err != nil {
				return fmt.Errorf("work %q recording %q: %w", slug, key, err)
			}
			if changedRec {
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
		if repointLists(next, seriesPersonLists, retiring, target) {
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
	if name := le.Str("name"); name != merged.Str("name") {
		t.note("  name: kept %s, dropped %s from %s (the slug redirects)", quoteFact(merged.Str("name")), quoteFact(name), loser)
	}
	merged.Set("sources", rawentry.UnionSources(merged.Sources(), le.Sources()))
	t.noteLost(fillStrings(merged, le, "sort_name", "kind", "description"), loser)
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

// repointLists rewrites every listed member of e naming a retiring person to target,
// dropping the repeat that leaves, and reports whether anything changed.
func repointLists(e entry, fields []string, retiring map[string]bool, target string) bool {
	changed := false
	for _, field := range fields {
		list := e.Strs(field)
		if !slices.ContainsFunc(list, func(s string) bool { return retiring[s] }) {
			continue
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
		changed = true
	}
	return changed
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
// person, as repointLists does a list. Only the readers member of a chapter is
// re-rendered; every other member, the timeline's numbers included, is carried as the
// bytes it was. changed is passed through when nothing here changes.
func repointChapterReaders(rec entry, retiring map[string]bool, target string, changed bool) (bool, error) {
	if !rec.Has("chapters") {
		return changed, nil
	}
	var chapters []entry
	if err := json.Unmarshal(rec["chapters"], &chapters); err != nil {
		return changed, err
	}
	touched := false
	for _, ch := range chapters {
		if repointLists(ch, []string{"readers"}, retiring, target) {
			touched = true
		}
	}
	if !touched {
		return changed, nil
	}
	rec.Set("chapters", chapters)
	return true, nil
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

func newPersonIndex(cat *model.Catalog) *personIndex {
	ix := &personIndex{works: map[string]map[string]bool{}, series: map[string]map[string]bool{}}
	add := func(m map[string]map[string]bool, person, slug string) {
		if m[person] == nil {
			m[person] = map[string]bool{}
		}
		m[person][slug] = true
	}
	for _, w := range cat.Works {
		for _, a := range w.Authors {
			add(ix.works, a, w.ID)
		}
		for _, c := range w.Credits {
			add(ix.works, c.Person, w.ID)
		}
		for _, r := range w.Recordings {
			for _, n := range r.Narrators {
				add(ix.works, n, w.ID)
			}
			for _, c := range r.Credits {
				add(ix.works, c.Person, w.ID)
			}
			for _, ch := range r.Chapters {
				for _, rd := range ch.Readers {
					add(ix.works, rd, w.ID)
				}
			}
		}
	}
	for _, s := range cat.Series {
		for _, a := range s.Authors {
			add(ix.series, a, s.ID)
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
				if m[f.target] == nil {
					m[f.target] = map[string]bool{}
				}
				m[f.target][slug] = true
			}
		}
	}
}
