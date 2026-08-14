package check

// The slug tombstone table (data/redirects.json): how it is read, and the rules
// that keep it resolvable. What the table is for and what must hold of it is
// stated once, on model.Redirects; this file enforces it.

import (
	"encoding/json"
	"maps"
	"os"
	"slices"

	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
	"github.com/kodestar/audiosilo-meta/pkg/redirects"
)

// redirectPath renders the Path of a problem about one redirect. It reads like
// every other Problem: the file, then the smallest thing in it that is wrong.
func redirectPath(kind model.RedirectKind, from string) string {
	return pack.RedirectsFile + ": " + string(kind) + " " + from
}

// loadRedirects reads and schema-validates the tombstone table, which aux names
// if the tree holds one.
//
// The file is reached through the LISTING rather than by probing the disk, like
// every other file this package reads: the walk has already classified it
// (pack.Listing.Aux), and a load that is as-of-open by construction - LoadStore,
// where a writer validates the tree it is planning against - must not answer from
// a file that appeared after that walk.
//
// It returns nil when there is nothing to check with: no file (a tree that has
// never retired a slug, which is most fixture trees and was every tree before the
// mechanism existed), an unreadable one, or one the schema rejected. A
// schema-rejected file returns nil DELIBERATELY, so the cross-record rules do not
// also fire on a document whose shape is already the reported problem: an unknown
// namespace or a key that is not a slug says nothing about whether its target
// exists. The load has failed either way.
func (l *loader) loadRedirects(aux []string) model.Redirects {
	p := pack.RedirectsFile
	if !slices.Contains(aux, p) {
		return nil
	}
	raw, err := os.ReadFile(redirects.Path(l.dir))
	if err != nil {
		l.add(p, "read: %v", err)
		return nil
	}
	// A repeated key is checked FIRST, and stops the load of this file: neither
	// encoding/json nor the schema validator can see the shadowed one, so a
	// namespace or a retired slug written twice would silently drop redirects with
	// the gate green - the same failure pack.Parse refuses for a pack, through the
	// same scan.
	if err := pack.CheckNoDuplicateKeys(raw); err != nil {
		l.add(p, "%s", collapse(err.Error()))
		return nil
	}
	inst, ok := l.instance(p, raw)
	if !ok {
		return nil
	}
	clean := true
	for _, v := range l.schemas.redirectViolations(inst) {
		l.add(p, "%s", v.String())
		clean = false
	}
	var reds model.Redirects
	if err := json.Unmarshal(raw, &reds); err != nil {
		l.reportUndecodable(p, clean, err)
		return nil
	}
	if !clean {
		return nil
	}
	return reds
}

// checkRedirects enforces what a redirect must be: a TOMBSTONE for a slug the
// database no longer holds, pointing directly at one it does.
//
// Five rules, each closing a way the table could stop resolving:
//
//   - no self-redirect, which resolves to nothing and reads as a loop;
//   - the source must NOT be a live id, or the record and its tombstone
//     disagree about what that slug means and which one answers depends on the
//     reader (this API prefers the record, so the redirect would be dead
//     weight nobody notices is wrong);
//   - the target MUST be a live id, or the redirect hands a consumer a second
//     404 - worse than the first, because it looks like an answer;
//   - no chain: a target may not itself be a source. The writer collapses
//     chains, so a chain here is a hand edit or a bad merge, and refusing it is
//     what lets every resolver do exactly one lookup;
//   - neither side may be what model.ValidRedirectSlug refuses, which for a
//     schema-clean file means a reserved route literal.
//
// The slug PATTERN is the schema's job, exactly as it is for every id in the
// tree, so by the time these rules run it already holds.
//
// works and people are the id sets checkIntegrity has already built; series is
// the one this needs of its own, and it is built HERE, after the early return, so
// a catalogue with no redirects (every load until the first merge lands) pays
// nothing at all.
func checkRedirects(cat *model.Catalog, works map[string]*model.Work, people map[string]bool, add addFunc) {
	reds := cat.Redirects
	if reds.Len() == 0 {
		return
	}
	series := idSet(cat.Series, func(s *model.Series) string { return s.ID })
	// "Live" is presence, so the two shapes agree: workByID keeps the FIRST
	// record of a duplicated id where idSet marks every one, and for "does the
	// database hold this slug" that is the same answer.
	live := func(kind model.RedirectKind, id string) bool {
		switch kind {
		case model.RedirectWorks:
			return works[id] != nil
		case model.RedirectPeople:
			return people[id]
		case model.RedirectSeries:
			return series[id]
		}
		return false
	}

	for _, kind := range model.RedirectKinds() {
		table := reds[kind]
		for _, from := range slices.Sorted(maps.Keys(table)) {
			to := table[from]
			at := redirectPath(kind, from)

			if err := model.ValidRedirectSlug(from); err != nil {
				add(at, "redirect source %q %s", from, err)
			}
			if err := model.ValidRedirectSlug(to); err != nil {
				add(at, "redirect target %q %s", to, err)
			}
			if from == to {
				add(at, "redirect points at itself: a tombstone names the record that REPLACED the retired slug")
				continue
			}
			if live(kind, from) {
				add(at, "redirect source is a live %s id: a slug the database still holds is addressed by its record, "+
					"so this tombstone can never be read", kind)
			}
			if !live(kind, to) {
				add(at, "redirect target %q is no live %s id: a tombstone must name a record that exists, "+
					"or it answers a lost id with a second dead end", to, kind)
			}
			if next, chained := table[to]; chained {
				add(at, "redirect target %q is itself redirected to %q: chains are collapsed when they are written "+
					"(pkg/redirects.Add), so point this at the final target - a resolver does one lookup", to, next)
			}
		}
	}
}

// idSet collects the ids of one entity slice, for the rules that ask "does the
// catalogue hold this id". One builder, so no rule walks a family the load has
// already walked for another rule.
func idSet[T any](xs []T, id func(T) string) map[string]bool {
	out := make(map[string]bool, len(xs))
	for _, x := range xs {
		out[id(x)] = true
	}
	return out
}
