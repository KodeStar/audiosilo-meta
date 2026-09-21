// Package recorddiff computes an ENTRY-LEVEL diff of the data tree between two
// versions of it and renders that diff as a compact summary.
//
// WHY IT EXISTS. The data tree is range-packed (PACK-SPEC.md): every file is a
// pack holding many unrelated entries, and a write RE-RENDERS the whole pack and
// may SPLIT it, moving entries that nothing touched into differently-named files.
// A textual `git diff` of such a change is therefore mostly noise about storage:
// a 100-work sync pull request is 136 files and tens of thousands of lines, of
// which the interesting part - the records that were actually added, removed or
// edited - is a few hundred lines scattered through it. Anything with a bounded
// input (the ai-verify reviewer, a maintainer skimming a pull request) sees a
// truncated tenth of that and cannot tell which tenth.
//
// WHAT IT PRODUCES INSTEAD. Entries are keyed by FAMILY + SLUG across all changed
// files at once, so storage churn cancels out: an entry that merely moved from
// one pack to another on a split is byte-identical on both sides and is counted,
// not printed. What is left is the four classes a reviewer cares about - added,
// removed, modified, moved-only - rendered as one line per added or removed
// record and a structural field-level diff per modified one.
//
// THE COMPARISON IS OVER CANONICAL BYTES of each entry (pkg/canonical), not over
// the file's text, so a re-indentation, a key reorder or a change of enclosing
// pack is not a change. The parse is pkg/pack's own (Parse, which rejects a
// duplicate key), so this package never grows a second reading of the pack format;
// data/redirects.json, the one non-pack file the tree holds, is diffed as the two
// maps it is.
//
// The two halves are deliberately separate: Compute takes any pair of Sources, so
// the unit tests diff two fixture trees on disk while the command diffs two git
// revisions.
package recorddiff

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/canonical"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
	"github.com/kodestar/audiosilo-meta/pkg/redirects"
)

// Entry is one added or removed record: where it lives and a one-line
// human-readable descriptor of it (see summary.go).
type Entry struct {
	Family  pack.Family `json:"family"`
	Slug    string      `json:"slug"`
	Summary string      `json:"summary"`
}

// Change is one modified record: the same key, plus the field-level differences
// between the two versions of it, already rendered (see fields.go).
type Change struct {
	Family pack.Family `json:"family"`
	Slug   string      `json:"slug"`
	Fields []string    `json:"fields"`
}

// Counts is one family's tally.
//
// Moved counts the entries that are byte-identical on both sides but sit in a
// different pack file - the split/rebind churn this package exists to collapse.
// They are counted and never printed: a moved entry is not a data change.
type Counts struct {
	Added    int `json:"added"`
	Removed  int `json:"removed"`
	Modified int `json:"modified"`
	Moved    int `json:"moved"`
}

// RedirectPair is one row of the slug tombstone table (data/redirects.json).
type RedirectPair struct {
	Kind string `json:"kind"`
	Old  string `json:"old"`
	New  string `json:"new"`
}

// RedirectDiff is the tombstone table's added and removed rows. A row whose
// target changed appears in both, which is what it is: one tombstone retired and
// another written.
type RedirectDiff struct {
	Added   []RedirectPair `json:"added"`
	Removed []RedirectPair `json:"removed"`
}

// Len reports the number of rows the diff holds.
func (r RedirectDiff) Len() int { return len(r.Added) + len(r.Removed) }

// Diff is the whole entry-level comparison of two versions of a data tree.
type Diff struct {
	Base      string                 `json:"base,omitempty"`
	Head      string                 `json:"head,omitempty"`
	Files     int                    `json:"files"`
	Counts    map[pack.Family]Counts `json:"counts"`
	Added     []Entry                `json:"added"`
	Removed   []Entry                `json:"removed"`
	Modified  []Change               `json:"modified"`
	Redirects RedirectDiff           `json:"redirects"`
	// Warnings names what the comparison could not read or believe: a file under
	// data/ that belongs to no family, a pack that would not parse, one slug in
	// two packs on the same side. They are rendered with the counts rather than
	// with the entries, because they qualify the whole summary.
	Warnings []string `json:"warnings,omitempty"`
}

// Source is one side of the comparison: the content of the data tree's files at
// one version, addressed by DATA-RELATIVE path ("works/0/0.json").
//
// found is false for a path this side does not hold, which is an ordinary answer
// rather than an error: a file created by the change is absent on the base side
// and a file deleted by it is absent on the head side.
type Source interface {
	Read(rel string) (content []byte, found bool, err error)
}

// entryKey identifies a record across the whole tranche, independently of which
// pack file happens to hold it. It is the reason a split is invisible here.
type entryKey struct {
	family pack.Family
	slug   string
}

// side is one version of one entry: the bytes the pack held it as, and the pack
// that held them - which is what distinguishes "moved" from "untouched
// neighbour".
//
// The bytes are the pack's OWN, not a canonical re-render. Every writer renders
// a pack through pkg/canonical, so two sides of an entry nobody edited are
// already byte-identical and the comparison is a memcmp; canonicalizing is
// reserved for the pair that does differ, where it answers the question a
// hand-edited pull request raises (a re-indentation is not a change). Over a
// real intake tranche that is a few dozen re-renders instead of a hundred
// thousand.
type side struct {
	path string
	raw  json.RawMessage
}

// collected is everything one version of the changed files holds.
type collected struct {
	entries   map[entryKey]side
	redirects model.Redirects
}

// Compute diffs the changed files' two versions entry by entry.
//
// paths are DATA-RELATIVE and name every file that differs between the two
// versions; a caller that hands over more than that gets the same answer, only
// slower, because an entry identical on both sides is classified as unchanged
// either way.
func Compute(paths []string, base, head Source) (*Diff, error) {
	rels := slices.Clone(paths)
	slices.Sort(rels)

	d := &Diff{Files: len(rels), Counts: map[pack.Family]Counts{}}

	// The stray-file warning is about a PATH, so it is the same on both sides:
	// only the base pass raises it, and a stray file is named once rather than
	// twice.
	b, err := collect(rels, base, "base", d, warnStray)
	if err != nil {
		return nil, err
	}
	h, err := collect(rels, head, "head", d, quietStray)
	if err != nil {
		return nil, err
	}

	for _, k := range unionEntryKeys(b.entries, h.entries) {
		bs, inBase := b.entries[k]
		hs, inHead := h.entries[k]
		c := d.Counts[k.family]
		switch {
		case !inBase:
			d.Added = append(d.Added, Entry{Family: k.family, Slug: k.slug, Summary: addedSummary(k.family, k.slug, hs.raw)})
			c.Added++
		case !inHead:
			d.Removed = append(d.Removed, Entry{Family: k.family, Slug: k.slug, Summary: removedSummary(k.family, k.slug, bs.raw)})
			c.Removed++
		case d.differs(k, bs, hs):
			d.Modified = append(d.Modified, Change{Family: k.family, Slug: k.slug, Fields: diffFields(bs.raw, hs.raw)})
			c.Modified++
		case bs.path != hs.path:
			// Identical bytes in a differently-named pack: a split or a rebind
			// relocated it. Counted, never printed.
			c.Moved++
		default:
			// Identical bytes in the same pack: a neighbour of the entry that
			// really changed. Not counted at all - it is not part of the change.
		}
		d.Counts[k.family] = c
	}

	d.Redirects = diffRedirects(b.redirects, h.redirects)
	return d, nil
}

// differs reports whether the two versions of one entry are a real change to the
// record, as opposed to the same record re-rendered.
//
// The cheap answer first: a pack is always written through pkg/canonical, so an
// entry nobody edited is byte-identical on both sides and a memcmp settles it.
// Only a pair that really differs is canonicalized, and only then to ask the
// expensive question - whether the difference is nothing but layout, which is
// what a hand-written pull request can produce. An entry that will not
// canonicalize is REPORTED as changed (its bytes differ, which is a fact) with a
// warning saying the comparison could not go the second mile.
func (d *Diff) differs(k entryKey, base, head side) bool {
	if bytes.Equal(base.raw, head.raw) {
		return false
	}
	bc, berr := canonical.Format(base.raw)
	hc, herr := canonical.Format(head.raw)
	if berr != nil || herr != nil {
		d.warn("entry %s/%s could not be canonicalized (%v); reported as changed on its bytes alone",
			k.family.Root(), k.slug, cmp.Or(berr, herr))
		return true
	}
	return !bytes.Equal(bc, hc)
}

// Empty reports whether the comparison found nothing to say about the records.
// Moved-only churn and warnings do not make a diff non-empty: neither is a data
// change.
func (d *Diff) Empty() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Modified) == 0 && d.Redirects.Len() == 0
}

// collect reads one version of every changed path and indexes its entries by
// family and slug. Anything it cannot use is recorded as a warning on d rather
// than failing the run: a summary that names what it could not read is worth more
// than no summary at all, and the mechanical `check` workflow is what refuses a
// tree this cannot parse.
// strayPolicy says whether this pass raises the "sits under no pack family"
// warning. It is a path-level observation, true of both sides at once, so
// exactly one pass reports it.
type strayPolicy bool

const (
	warnStray  strayPolicy = true
	quietStray strayPolicy = false
)

func collect(rels []string, src Source, which string, d *Diff, stray strayPolicy) (collected, error) {
	out := collected{entries: map[entryKey]side{}}
	for _, rel := range rels {
		if rel == pack.RedirectsFile {
			raw, found, err := src.Read(rel)
			if err != nil {
				return collected{}, fmt.Errorf("%s: %s: %w", which, rel, err)
			}
			if !found {
				continue
			}
			red, err := redirects.Parse(raw)
			if err != nil {
				d.warn("%s: %s: %v", which, rel, err)
				continue
			}
			out.redirects = red
			continue
		}
		family, ok := familyOf(rel)
		if !ok {
			if stray == warnStray {
				d.warn("%s sits under no pack family and was not summarized", rel)
			}
			continue
		}
		raw, found, err := src.Read(rel)
		if err != nil {
			return collected{}, fmt.Errorf("%s: %s: %w", which, rel, err)
		}
		if !found {
			continue
		}
		file, err := pack.Parse(raw)
		if err != nil {
			d.warn("%s: %s is not a readable pack (%v); its entries are missing from this summary", which, rel, err)
			continue
		}
		for _, slug := range file.Slugs() {
			entry, _ := file.Get(slug)
			k := entryKey{family: family, slug: slug}
			if prev, dup := out.entries[k]; dup {
				// Two packs on ONE side holding the same slug is a tree defect
				// (pkg/check refuses it). Keep the first, in sorted path order, so
				// the summary stays deterministic, and say so.
				d.warn("%s: entry %s/%s appears in both %s and %s; only the first is summarized", which, family.Root(), slug, prev.path, rel)
				continue
			}
			out.entries[k] = side{path: rel, raw: entry}
		}
	}
	return out, nil
}

// warn appends a formatted warning.
func (d *Diff) warn(format string, args ...any) {
	d.Warnings = append(d.Warnings, fmt.Sprintf(format, args...))
}

// familyOf names the pack family a data-relative path sits under. It reads the
// first path segment through pack's own family table, so a directory this
// repository does not hold (works-community, since the community-repo split) is
// still recognized when the tool is pointed at a tree that does.
func familyOf(rel string) (pack.Family, bool) {
	root, _, ok := strings.Cut(rel, "/")
	if !ok {
		return "", false
	}
	def, known := pack.Def(pack.Family(root))
	if !known {
		return "", false
	}
	return def.Family, true
}

// diffRedirects compares the two versions of the tombstone table row by row.
func diffRedirects(base, head model.Redirects) RedirectDiff {
	var out RedirectDiff
	for _, kind := range model.RedirectKinds() {
		b, h := base[kind], head[kind]
		for _, old := range unionSortedKeys(b, h) {
			bt, inBase := b[old]
			ht, inHead := h[old]
			switch {
			case !inBase:
				out.Added = append(out.Added, RedirectPair{Kind: string(kind), Old: old, New: ht})
			case !inHead:
				out.Removed = append(out.Removed, RedirectPair{Kind: string(kind), Old: old, New: bt})
			case bt != ht:
				out.Removed = append(out.Removed, RedirectPair{Kind: string(kind), Old: old, New: bt})
				out.Added = append(out.Added, RedirectPair{Kind: string(kind), Old: old, New: ht})
			}
		}
	}
	return out
}

// unionKeys returns every key either map holds, ONCE. The order is the map
// iteration's, so every caller sorts it - which is also why there is one of
// these rather than one per key type.
func unionKeys[K comparable, V any](a, b map[K]V) []K {
	out := make([]K, 0, len(a)+len(b))
	for k := range a {
		out = append(out, k)
	}
	// a is its own dedupe set, so the union needs no second map - which over a
	// large tranche is a few hundred thousand entries not allocated twice.
	for k := range b {
		if _, dup := a[k]; !dup {
			out = append(out, k)
		}
	}
	return out
}

// unionEntryKeys returns every key either side holds, in the ONE order this
// package prints things: family name, then slug.
func unionEntryKeys(a, b map[entryKey]side) []entryKey {
	out := unionKeys(a, b)
	slices.SortFunc(out, func(x, y entryKey) int {
		if x.family != y.family {
			if familyLess(x.family, y.family) {
				return -1
			}
			return 1
		}
		return strings.Compare(x.slug, y.slug)
	})
	return out
}

// unionSortedKeys returns every key of either map, sorted.
func unionSortedKeys[V any](a, b map[string]V) []string {
	out := unionKeys(a, b)
	slices.Sort(out)
	return out
}
