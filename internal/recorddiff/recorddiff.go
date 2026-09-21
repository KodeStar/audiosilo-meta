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
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/canonical"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
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

// side is one version of one entry: its canonical bytes and the pack that held
// them, which is what distinguishes "moved" from "untouched neighbour".
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
	rels := append([]string(nil), paths...)
	sort.Strings(rels)

	d := &Diff{Files: len(rels), Counts: map[pack.Family]Counts{}}

	b, err := collect(rels, base, "base", d)
	if err != nil {
		return nil, err
	}
	h, err := collect(rels, head, "head", d)
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
		case !bytesEqual(bs.raw, hs.raw):
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
func collect(rels []string, src Source, which string, d *Diff) (collected, error) {
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
			red, err := parseRedirects(raw)
			if err != nil {
				d.warn("%s: %s: %v", which, rel, err)
				continue
			}
			out.redirects = red
			continue
		}
		family, ok := familyOf(rel)
		if !ok {
			// Warned once, on the base pass, so a stray file is named rather than
			// named twice.
			if which == "base" {
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
			canon, err := canonical.Format(entry)
			if err != nil {
				d.warn("%s: %s: entry %q could not be canonicalized (%v)", which, rel, slug, err)
				continue
			}
			k := entryKey{family: family, slug: slug}
			if prev, dup := out.entries[k]; dup {
				// Two packs on ONE side holding the same slug is a tree defect
				// (pkg/check refuses it). Keep the first, in sorted path order, so
				// the summary stays deterministic, and say so.
				d.warn("%s: entry %s/%s appears in both %s and %s; only the first is summarized", which, family.Root(), slug, prev.path, rel)
				continue
			}
			out.entries[k] = side{path: rel, raw: canon}
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

// parseRedirects reads the tombstone table, refusing a duplicate key the same way
// every other reader of the data tree does (pack.CheckNoDuplicateKeys): last-wins
// would drop a redirect invisibly.
func parseRedirects(raw []byte) (model.Redirects, error) {
	if err := pack.CheckNoDuplicateKeys(raw); err != nil {
		return nil, err
	}
	var red model.Redirects
	if err := json.Unmarshal(raw, &red); err != nil {
		return nil, err
	}
	return red, nil
}

// diffRedirects compares the two versions of the tombstone table row by row.
func diffRedirects(base, head model.Redirects) RedirectDiff {
	var out RedirectDiff
	for _, kind := range model.RedirectKinds() {
		b, h := base[kind], head[kind]
		for _, old := range unionStringKeys(b, h) {
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

// unionEntryKeys returns every key either side holds, in the ONE order this
// package prints things: family name, then slug.
func unionEntryKeys(a, b map[entryKey]side) []entryKey {
	seen := make(map[entryKey]bool, len(a)+len(b))
	out := make([]entryKey, 0, len(a)+len(b))
	for _, m := range []map[entryKey]side{a, b} {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].family != out[j].family {
			return familyLess(out[i].family, out[j].family)
		}
		return out[i].slug < out[j].slug
	})
	return out
}

// unionStringKeys returns every key of either map, sorted.
func unionStringKeys(a, b map[string]string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, m := range []map[string]string{a, b} {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	return out
}

// bytesEqual compares two canonical entries.
func bytesEqual(a, b json.RawMessage) bool { return string(a) == string(b) }
