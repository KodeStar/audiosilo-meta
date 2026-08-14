package check

// The slug tombstone table (data/redirects.json): how it is read, and the rules
// that make it a tombstone table rather than a second addressing scheme.
//
// A slug is public API - a meta.audiosilo.app URL, a books.work_id in every
// audiosilo-sidecars install, a contributed sidecar's work reference,
// audiosilo-server's community-metadata seam - so a repair that merges duplicate
// records records where the retired id went instead of dropping it. What a
// consumer needs from that record is narrow and absolute: one lookup resolves,
// and it resolves to something that is there. Everything below exists to keep
// those two sentences true.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// redirectPath renders the Path of a problem about one redirect. It reads like
// every other Problem: the file, then the smallest thing in it that is wrong.
func redirectPath(kind model.RedirectKind, from string) string {
	return pack.RedirectsFile + ": " + string(kind) + " " + from
}

// loadRedirects reads and schema-validates the tombstone table under dir. It
// returns nil when there is nothing to check with: no file (a tree that has
// never retired a slug - which is most fixture trees, and was every tree before
// the mechanism existed), an unreadable one, or one the schema rejected.
//
// A schema-rejected file returns nil DELIBERATELY, so the cross-record rules do
// not also fire on a document whose shape is already the reported problem: an
// unknown namespace or a key that is not a slug says nothing about whether its
// target exists. The load has failed either way.
func (l *loader) loadRedirects(dir string) model.Redirects {
	p := pack.RedirectsFile
	raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		l.add(p, "read: %v", err)
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
//     chains (pkg/redirects.Add repoints both directions), so a chain here is a
//     hand edit or a bad merge, and refusing it is what lets every resolver -
//     this artifact's serve path included - do exactly one lookup;
//   - neither side may be a reserved route literal (model.ReservedSlugs): a
//     word no record may be stored under is no target either, and as a source
//     it names a route rather than a retired record.
//
// The slug PATTERN is the schema's job, exactly as it is for every id in the
// tree, so it is deliberately not restated here.
func checkRedirects(cat *model.Catalog, reds model.Redirects, add addFunc) {
	if len(reds) == 0 {
		return
	}
	live := liveIDs(cat)
	for _, kind := range model.RedirectKinds() {
		table := reds[kind]
		for _, from := range sortedKeys(table) {
			to := table[from]
			at := redirectPath(kind, from)

			for _, s := range []struct{ what, slug string }{{"source", from}, {"target", to}} {
				if model.IsReservedSlug(s.slug) {
					add(at, "redirect %s %q is a reserved slug: it names an API route segment, not a record", s.what, s.slug)
				}
			}
			if from == to {
				add(at, "redirect points at itself: a tombstone names the record that REPLACED the retired slug")
				continue
			}
			if live[kind][from] {
				add(at, "redirect source is a live %s id: a slug the database still holds is addressed by its record, "+
					"so this tombstone can never be read", kind)
			}
			if !live[kind][to] {
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

// liveIDs collects the ids the catalogue actually holds, per redirect namespace.
// It is what both the "source is retired" and the "target exists" rules are
// written against, so the two can never disagree about what "live" means.
func liveIDs(cat *model.Catalog) map[model.RedirectKind]map[string]bool {
	out := map[model.RedirectKind]map[string]bool{
		model.RedirectWorks:  make(map[string]bool, len(cat.Works)),
		model.RedirectPeople: make(map[string]bool, len(cat.People)),
		model.RedirectSeries: make(map[string]bool, len(cat.Series)),
	}
	for _, w := range cat.Works {
		out[model.RedirectWorks][w.ID] = true
	}
	for _, p := range cat.People {
		out[model.RedirectPeople][p.ID] = true
	}
	for _, s := range cat.Series {
		out[model.RedirectSeries][s.ID] = true
	}
	return out
}

// sortedKeys returns a table's keys in slug order, so a run's problems come out
// in the same order every time (the load is deterministic by construction and a
// map walk is not).
func sortedKeys(table map[string]string) []string {
	out := make([]string, 0, len(table))
	for k := range table {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
