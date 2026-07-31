// Package format is metafmt's business logic: canonical formatting of the data
// tree plus, for families already in pack layout, the self-healing placement
// pass (relocating misplaced entries and performing due pack and directory
// splits).
//
// It is the glue between two packages that deliberately do not know about each
// other: pkg/canonical owns per-file canonical form and is layout-agnostic,
// pkg/pack owns bound math, relocation and splitting. Sequencing them (format
// first, then heal the families the store reports as packed) is metafmt's
// concern, so it lives here rather than in either public package - cmd/metafmt
// stays flag wiring, per the repo's thin-CLI convention.
package format

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kodestar/audiosilo-meta/pkg/canonical"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// Report is what one metafmt run found (Check) or did (Write).
type Report struct {
	// Dir is the data directory the run was made against. Pack paths are
	// data-relative, so the reported lines are prefixed with it to match the
	// file paths pkg/canonical reports.
	Dir string
	// NonCanonical holds the files Check found not in canonical form.
	NonCanonical []string
	// Formatted holds the files Write rewrote into canonical form.
	Formatted []string
	// Invalid holds files that do not parse as JSON. They are left untouched,
	// and they suppress the placement pass: a pack that cannot be read cannot
	// be healed.
	Invalid []string
	// Misplaced holds the entries sitting outside their bound-correct pack -
	// reported by Check, moved by Write.
	Misplaced []pack.Misplaced
	// Splits holds the packs over a hard cap, and Dirs the directories over the
	// pack cap. Both are measured before the flush, so a split the relocation
	// itself caused is not listed here; Wrote and Deleted are the exact file
	// effects.
	Splits []pack.DueSplit
	Dirs   []pack.DueDirSplit
	// Wrote and Deleted hold the pack files the placement pass created,
	// rewrote, or removed, data-relative.
	Wrote   []string
	Deleted []string
}

// Clean reports whether the tree needs no work at all.
func (r Report) Clean() bool {
	return len(r.NonCanonical) == 0 && len(r.Formatted) == 0 && len(r.Invalid) == 0 &&
		len(r.Misplaced) == 0 && len(r.Splits) == 0 && len(r.Dirs) == 0
}

// path renders a data-relative pack path the way pkg/canonical reports file
// paths, so one run's output speaks in one convention.
func (r Report) path(rel string) string {
	return filepath.Join(r.Dir, filepath.FromSlash(rel))
}

// CheckLines returns one line per problem, in the order metafmt --check prints
// them: non-canonical files first, then the placement work with the canonical
// location named.
func (r Report) CheckLines() []string {
	out := make([]string, 0, len(r.NonCanonical)+len(r.Misplaced)+len(r.Splits)+len(r.Dirs))
	out = append(out, r.NonCanonical...)
	for _, m := range r.Misplaced {
		out = append(out, fmt.Sprintf("entry %q belongs in %s, not %s",
			m.Slug, r.path(m.To.Path()), r.path(m.From.Path())))
	}
	for _, s := range r.Splits {
		out = append(out, fmt.Sprintf("pack %s is over its hard %s cap (%d entries, %d bytes), split due",
			r.path(s.Pack.Path()), s.Reason, s.Entries, s.Size))
	}
	for _, d := range r.Dirs {
		if d.Dir == "" {
			out = append(out, fmt.Sprintf("family %s holds %d packs and has to gain a directory level, split due",
				r.path(d.Family.Root()), d.Packs))
			continue
		}
		out = append(out, fmt.Sprintf("directory %s holds %d packs, over the pack cap, split due",
			r.path(d.Family.Root()+"/"+d.Dir), d.Packs))
	}
	return out
}

// WriteLines returns one line per change metafmt --write made.
func (r Report) WriteLines() []string {
	out := make([]string, 0, len(r.Formatted)+len(r.Misplaced)+len(r.Splits)+len(r.Dirs)+len(r.Wrote)+len(r.Deleted))
	for _, f := range r.Formatted {
		out = append(out, "formatted "+f)
	}
	for _, m := range r.Misplaced {
		out = append(out, fmt.Sprintf("moved entry %q to %s (from %s)",
			m.Slug, r.path(m.To.Path()), r.path(m.From.Path())))
	}
	for _, s := range r.Splits {
		out = append(out, fmt.Sprintf("split pack %s (%d entries, %d bytes, over its hard %s cap)",
			r.path(s.Pack.Path()), s.Entries, s.Size, s.Reason))
	}
	for _, d := range r.Dirs {
		if d.Dir == "" {
			out = append(out, fmt.Sprintf("split family %s into directories (%d packs)",
				r.path(d.Family.Root()), d.Packs))
			continue
		}
		out = append(out, fmt.Sprintf("split directory %s (%d packs)",
			r.path(d.Family.Root()+"/"+d.Dir), d.Packs))
	}
	for _, f := range r.Wrote {
		out = append(out, "wrote "+r.path(f))
	}
	for _, f := range r.Deleted {
		out = append(out, "deleted "+r.path(f))
	}
	return out
}

// Summary is the one-line count of outstanding work, for the CI failure
// message. It is empty for a clean tree.
func (r Report) Summary() string {
	var parts []string
	add := func(n int, one, many string) {
		switch {
		case n == 1:
			parts = append(parts, "1 "+one)
		case n > 1:
			parts = append(parts, fmt.Sprintf("%d %s", n, many))
		}
	}
	add(len(r.NonCanonical), "file not canonical", "files not canonical")
	add(len(r.Formatted), "file formatted", "files formatted")
	add(len(r.Invalid), "file with invalid JSON", "files with invalid JSON")
	add(len(r.Misplaced), "misplaced entry", "misplaced entries")
	add(len(r.Splits), "pack split due", "packs split due")
	add(len(r.Dirs), "directory split due", "directories split due")
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += ", " + p
	}
	return out
}

// Check reports the tree's outstanding formatting and placement work without
// writing anything.
func Check(dataDir string) (Report, error) {
	rep := Report{Dir: dataDir}
	bad, err := canonical.CheckTree(dataDir)
	if err != nil {
		return Report{}, err
	}
	rep.NonCanonical = bad
	rep.Invalid, err = unparseable(bad)
	if err != nil {
		return Report{}, err
	}
	if len(rep.Invalid) > 0 {
		return rep, nil
	}
	err = withPackedFamilies(dataDir, func(s *pack.Store, families []pack.Family) error {
		for _, f := range families {
			p, perr := s.Pending(f)
			if perr != nil {
				return perr
			}
			rep.Misplaced = append(rep.Misplaced, p.Misplaced...)
			rep.Splits = append(rep.Splits, p.Packs...)
			rep.Dirs = append(rep.Dirs, p.Dirs...)
		}
		return nil
	})
	if err != nil {
		return Report{}, err
	}
	return rep, nil
}

// Write formats every file canonically and, for families in pack layout, moves
// misplaced entries into their bound-correct pack and performs the due pack and
// directory splits. An already well-formed tree is left byte-identical.
func Write(dataDir string) (Report, error) {
	rep := Report{Dir: dataDir}
	// Formatting runs first: Store.Flush judges a pack no writer touched by its
	// on-disk size, which is only the pack's canonical size once the file is in
	// canonical form.
	changed, failed, err := canonical.WriteTree(dataDir)
	if err != nil {
		return Report{}, err
	}
	rep.Formatted, rep.Invalid = changed, failed
	if len(failed) > 0 {
		return rep, nil
	}
	err = withPackedFamilies(dataDir, func(s *pack.Store, families []pack.Family) error {
		for _, f := range families {
			// Pending runs only to report what is about to be fixed: Heal
			// queues the relocations and marks the due splits, and Heal + Flush
			// is what leaves the family Pending-clean.
			p, perr := s.Pending(f)
			if perr != nil {
				return perr
			}
			rep.Splits = append(rep.Splits, p.Packs...)
			rep.Dirs = append(rep.Dirs, p.Dirs...)
			moved, herr := s.Heal(f)
			if herr != nil {
				return herr
			}
			rep.Misplaced = append(rep.Misplaced, moved...)
		}
		w, ferr := s.Flush()
		if ferr != nil {
			return ferr
		}
		rep.Wrote, rep.Deleted = w.Wrote, w.Deleted
		return nil
	})
	if err != nil {
		return Report{}, err
	}
	return rep, nil
}

// unparseable returns the files among paths that are not valid JSON.
func unparseable(paths []string) ([]string, error) {
	var out []string
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		if !json.Valid(raw) {
			out = append(out, p)
		}
	}
	return out, nil
}

// withPackedFamilies runs fn against the tree's store and the families in pack
// layout, in family order. fn is not called at all when no family is packed,
// which is the whole tree until the migration lands. The store is layout-aware,
// so a family still in the legacy layout is simply not passed to fn and Flush
// never touches it.
func withPackedFamilies(dataDir string, fn func(*pack.Store, []pack.Family) error) error {
	s, err := pack.Open(dataDir)
	if err != nil {
		return err
	}
	var packed []pack.Family
	for _, d := range pack.Families() {
		if s.Layout(d.Family) == pack.LayoutPack {
			packed = append(packed, d.Family)
		}
	}
	if len(packed) == 0 {
		return nil
	}
	return fn(s, packed)
}
