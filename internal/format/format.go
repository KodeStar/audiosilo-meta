// Package format is metafmt's business logic: canonical formatting of the data
// tree plus, for families already in pack layout, the self-healing structural
// pass (salvaging files that are not packs, relocating misplaced entries,
// resolving duplicate slugs, and performing due splits and rebinds).
//
// It is the glue between two packages that deliberately do not know about each
// other: pkg/canonical owns per-file canonical form and is layout-agnostic,
// pkg/pack owns the layout - what is wrong with a family, and how one Heal plus
// one Flush makes it right. This package sequences them and renders the result;
// it decides nothing about the layout itself, and it does not restate pkg/pack's
// phrasing, so metafmt's model of "wrong" cannot drift from metacheck's.
// cmd/metafmt stays flag wiring, per the repo's thin-CLI convention.
package format

import (
	"fmt"
	"path/filepath"

	"github.com/kodestar/audiosilo-meta/pkg/canonical"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// Report is what one metafmt run found (Check) or did (Write).
type Report struct {
	// Dir is the data directory the run was made against. Pack paths are
	// data-relative, so the reported lines are rendered beneath it to match the
	// file paths pkg/canonical reports.
	Dir string
	// NonCanonical holds the files Check found not in canonical form, and
	// Formatted the ones Write rewrote. Only one is ever set.
	NonCanonical []string
	Formatted    []string
	// Invalid holds files that are not valid JSON, left untouched. A file under
	// a packed family root is reported by Pending.Unreadable instead, which says
	// more, so it appears here only when no packed family owns it.
	Invalid []string
	// Pending is every structural problem the packed families have, merged
	// across them. It is pkg/pack's own report: Check names it, Write performs
	// all of it but the unreadable files.
	Pending pack.Pending
	// Wrote and Deleted hold the pack files the structural pass created,
	// rewrote, or removed, data-relative.
	Wrote   []string
	Deleted []string
}

// Clean reports whether the tree needs no work at all.
func (r Report) Clean() bool {
	return len(r.NonCanonical) == 0 && len(r.Formatted) == 0 && len(r.Invalid) == 0 &&
		r.Pending.Empty()
}

// NeedsHuman reports whether something outstanding is beyond what --write can
// fix: a file the tooling cannot read is never rewritten or deleted for the
// contributor, it is only named.
func (r Report) NeedsHuman() bool {
	return len(r.Invalid) > 0 || !r.Pending.Healable()
}

// fixable reports whether anything outstanding is something --write performs.
// An unparseable file is not: it is listed as non-canonical too, so that count
// only means work when it exceeds the files nothing can parse.
func (r Report) fixable() bool {
	p := r.Pending
	return len(r.NonCanonical) > len(r.Invalid) || len(r.Formatted) > 0 ||
		len(p.Salvage) > 0 || len(p.Misplaced) > 0 || len(p.Conflicts) > 0 ||
		len(p.Rebinds) > 0 || len(p.Packs) > 0 || len(p.Dirs) > 0
}

// Advice is what to do about the report, for the failure line. Telling a
// contributor to run --write against a tree only a human can fix would send
// them in a circle.
func (r Report) Advice() string {
	switch {
	case !r.NeedsHuman():
		return "run metafmt --write to fix"
	case !r.fixable():
		return "metafmt cannot fix these, they need a human"
	default:
		return "run metafmt --write to fix the rest; the unreadable files need a human"
	}
}

// path renders a data-relative pack path the way pkg/canonical reports file
// paths, so one run's output speaks in one convention.
func (r Report) path(rel string) string {
	return filepath.Join(r.Dir, filepath.FromSlash(rel))
}

// Lines returns one line per problem (Check) or change (Write). The structural
// sentences are pkg/pack's, rendered beneath the data directory: it owns the
// vocabulary of what is wrong and what the fix is, and metafmt only frames it
// with the files it formatted and the files the flush touched.
func (r Report) Lines() []string {
	pending := r.Pending.LinesUnder(r.Dir)
	out := make([]string, 0, len(r.NonCanonical)+len(r.Formatted)+len(pending)+len(r.Wrote)+len(r.Deleted))
	out = append(out, r.NonCanonical...)
	for _, f := range r.Formatted {
		out = append(out, "formatted "+f)
	}
	out = append(out, pending...)
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
	add(len(r.Pending.Unreadable), "file that is not a readable pack", "files that are not readable packs")
	add(len(r.Pending.Salvage), "file that is not a pack", "files that are not packs")
	add(len(r.Pending.Conflicts), "duplicate entry", "duplicate entries")
	add(len(r.Pending.Misplaced), "misplaced entry", "misplaced entries")
	add(len(r.Pending.Rebinds), "pack rebind due", "pack rebinds due")
	add(len(r.Pending.Packs), "pack split due", "packs split due")
	add(len(r.Pending.Dirs), "directory split due", "directory splits due")
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += ", " + p
	}
	return out
}

// Check reports the tree's outstanding formatting and structural work without
// writing anything.
func Check(dataDir string) (Report, error) {
	rep := Report{Dir: dataDir}
	nonCanonical, invalid, err := canonical.CheckTree(dataDir)
	if err != nil {
		return Report{}, err
	}
	rep.NonCanonical, rep.Invalid = nonCanonical, invalid
	err = withPackedFamilies(dataDir, func(s *pack.Store, families []pack.Family) error {
		for _, f := range families {
			p, perr := s.Pending(f)
			if perr != nil {
				return perr
			}
			mergePending(&rep.Pending, p)
		}
		return nil
	})
	if err != nil {
		return Report{}, err
	}
	rep.dropUnreadableDuplicates()
	return rep, nil
}

// Write formats every file canonically and makes every packed family
// well-formed: files that are not packs are salvaged and deleted, entries move
// to their bound-correct pack, duplicate slugs resolve to the correctly-placed
// copy, and due splits and rebinds are performed. One pass converges - a second
// run is a byte-level no-op - and a file the tooling cannot read is only
// reported, never touched.
func Write(dataDir string) (Report, error) {
	rep := Report{Dir: dataDir}
	// Formatting runs first: Flush judges a pack no write reached by its
	// on-disk size, which is only the pack's canonical size once the file is in
	// canonical form.
	changed, failed, err := canonical.WriteTree(dataDir)
	if err != nil {
		return Report{}, err
	}
	rep.Formatted, rep.Invalid = changed, failed
	err = withPackedFamilies(dataDir, func(s *pack.Store, families []pack.Family) error {
		healed := false
		for _, f := range families {
			// One Pending per family, for the report. A family that is already
			// well-formed is then left entirely alone: no heal, and no flush at
			// all if that holds for every family, which is what makes a clean
			// tree cost one pass over it and nothing more.
			p, perr := s.Pending(f)
			if perr != nil {
				return perr
			}
			mergePending(&rep.Pending, p)
			if p.Empty() {
				continue
			}
			if _, herr := s.Heal(f); herr != nil {
				return herr
			}
			healed = true
		}
		if !healed {
			return nil
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
	rep.dropUnreadableDuplicates()
	return rep, nil
}

// dropUnreadableDuplicates removes the files pkg/pack already reported as
// unreadable from the plain JSON lists. Both are true of the same file - it does
// not parse, and it is not a pack - but the structural sentence says what
// happens to it, so it is the one that prints.
func (r *Report) dropUnreadableDuplicates() {
	if len(r.Pending.Unreadable) == 0 {
		return
	}
	seen := make(map[string]bool, len(r.Pending.Unreadable))
	for _, u := range r.Pending.Unreadable {
		seen[r.path(u.Path)] = true
	}
	drop := func(in []string) []string {
		out := in[:0:0]
		for _, p := range in {
			if !seen[p] {
				out = append(out, p)
			}
		}
		return out
	}
	r.NonCanonical = drop(r.NonCanonical)
	r.Formatted = drop(r.Formatted)
	r.Invalid = drop(r.Invalid)
}

// mergePending folds one family's report into the run's. Every item names its
// own family, so the merge loses nothing and the rendered order stays
// category-first.
func mergePending(dst *pack.Pending, p pack.Pending) {
	dst.Salvage = append(dst.Salvage, p.Salvage...)
	dst.Unreadable = append(dst.Unreadable, p.Unreadable...)
	dst.Misplaced = append(dst.Misplaced, p.Misplaced...)
	dst.Conflicts = append(dst.Conflicts, p.Conflicts...)
	dst.Rebinds = append(dst.Rebinds, p.Rebinds...)
	dst.Packs = append(dst.Packs, p.Packs...)
	dst.Dirs = append(dst.Dirs, p.Dirs...)
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
