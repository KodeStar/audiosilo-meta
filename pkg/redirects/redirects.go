// Package redirects reads, edits and writes the slug tombstone table
// (data/redirects.json, model.Redirects): which retired slug now stands for
// which live record.
//
// It exists for the WRITER's side of that table. A data-quality repair that
// merges two duplicate records retires a slug, and a slug is public API - a
// meta.audiosilo.app URL, a books.work_id in every audiosilo-sidecars install, a
// contributed sidecar's work reference, audiosilo-server's community-metadata
// seam - so the repair has to record where the retired id went. Load, Add, Write
// is that whole flow, and Add is where the one property a resolver depends on is
// maintained: no target is itself a source, so resolution is ONE lookup and can
// never loop. pkg/check refuses a tree where that does not hold, so a hand edit
// is policed by the same rule this enforces mechanically.
//
// Reading it back is pkg/check's job (it validates the file against
// schema/redirects.schema.json and leaves the table on the Catalog), so nothing
// here validates against the catalogue: this package knows the file, not the
// database.
//
// This package is PUBLIC API, like the pkg/* packages around it: the repair
// tooling that consumes it may live in a sibling module.
package redirects

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kodestar/audiosilo-meta/pkg/canonical"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// Path returns the redirects file's path under the data root dataDir.
func Path(dataDir string) string {
	return filepath.Join(dataDir, filepath.FromSlash(pack.RedirectsFile))
}

// Load reads the table under the data root dataDir. A tree that does not carry
// the file yet reads as an EMPTY table rather than an error, so a writer's flow
// is the same whether or not anything has ever been retired; every namespace is
// present in what comes back, so a caller may index it directly.
//
// It refuses a file naming a namespace that is not one of model's three: a
// writer must never rewrite a file it did not fully understand, and dropping the
// unknown key silently is exactly that.
func Load(dataDir string) (model.Redirects, error) {
	raw, err := os.ReadFile(Path(dataDir))
	if err != nil {
		if os.IsNotExist(err) {
			return model.NewRedirects(), nil
		}
		return nil, err
	}
	var got model.Redirects
	if err := json.Unmarshal(raw, &got); err != nil {
		return nil, fmt.Errorf("%s: %w", pack.RedirectsFile, err)
	}
	out := model.NewRedirects()
	for kind, table := range got {
		if !model.ValidRedirectKind(kind) {
			return nil, fmt.Errorf("%s: unknown namespace %q (want one of %v)", pack.RedirectsFile, kind, model.RedirectKinds())
		}
		for from, to := range table {
			out[kind][from] = to
		}
	}
	return out, nil
}

// Write renders r canonically to the redirects file under dataDir, creating the
// data root if it is not there.
//
// Every namespace is written, empty or not: the schema requires all three keys,
// so the file's shape is the same before and after a namespace gains its first
// entry and a diff only ever shows the entry.
func Write(dataDir string, r model.Redirects) error {
	full := model.NewRedirects()
	for kind, table := range r {
		if !model.ValidRedirectKind(kind) {
			return fmt.Errorf("%s: unknown namespace %q (want one of %v)", pack.RedirectsFile, kind, model.RedirectKinds())
		}
		for from, to := range table {
			full[kind][from] = to
		}
	}
	raw, err := json.Marshal(full)
	if err != nil {
		return err
	}
	// The one definition of canonical form, as for every other file in the tree:
	// sorted keys (so the namespaces and every entry are in slug order), 2-space
	// indent, one trailing newline. metafmt --check judges this file by the same
	// rule, so anything else here would be reported as non-canonical.
	out, err := canonical.Format(raw)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(Path(dataDir), out, 0o644)
}

// Add records that the retired slug from now stands for to, in namespace kind.
// It edits r in place; write it back with Write.
//
// CHAINS ARE COLLAPSED, in both directions, which is the whole reason this is a
// function rather than a map assignment. A repair pass merges records over time,
// so the two shapes it produces are:
//
//   - the target has since been retired itself (to -> newer is already
//     recorded), and from must point at the FINAL target, not at another
//     tombstone;
//   - something already points at from (older -> from), and those tombstones
//     have to be repointed at to, or yesterday's redirect becomes a chain today.
//
// Either way every entry ends up naming a live record directly, which is what
// lets a resolver do one lookup and what pkg/check enforces (checkRedirects).
//
// It refuses rather than guesses: an unknown namespace, a slug that is not a
// slug, a reserved route literal (model.IsReservedSlug - an id no record may
// carry is no target either), a redirect from a slug that already points
// somewhere else, and a target that collapses back onto from (which would be a
// self-redirect, i.e. a cycle). Re-adding a redirect that is already recorded is
// a no-op, so a repair pass is idempotent.
//
// Whether from is really retired and to really exists are questions about the
// CATALOGUE, which this package deliberately does not read; pkg/check answers
// both, over the tree the writer has just produced.
func Add(r model.Redirects, kind model.RedirectKind, from, to string) error {
	if !model.ValidRedirectKind(kind) {
		return fmt.Errorf("unknown namespace %q (want one of %v)", kind, model.RedirectKinds())
	}
	if r == nil {
		return fmt.Errorf("%s redirect %s: nil table (start from model.NewRedirects or redirects.Load)", kind, from)
	}
	for _, s := range []struct{ what, slug string }{{"source", from}, {"target", to}} {
		if !model.ValidSlug(s.slug) {
			return fmt.Errorf("%s redirect %s: %s %q is not a valid slug", kind, from, s.what, s.slug)
		}
		if model.IsReservedSlug(s.slug) {
			return fmt.Errorf("%s redirect %s: %s %q is a reserved slug", kind, from, s.what, s.slug)
		}
	}
	if from == to {
		return fmt.Errorf("%s redirect %s: redirects to itself", kind, from)
	}
	if r[kind] == nil {
		r[kind] = map[string]string{}
	}
	table := r[kind]

	final, err := collapse(table, from, to)
	if err != nil {
		return fmt.Errorf("%s redirect %s: %w", kind, from, err)
	}
	if cur, ok := table[from]; ok && cur != final {
		return fmt.Errorf("%s redirect %s: already redirects to %q, not %q: retargeting a tombstone is a decision, not a merge", kind, from, cur, final)
	}
	// Repoint what already pointed HERE before recording the new hop, so the
	// table is never momentarily two hops deep.
	for src, dst := range table {
		if dst == from {
			table[src] = final
		}
	}
	table[from] = final
	return nil
}

// collapse follows to through the tombstones already recorded and returns the
// live slug at the end of that walk. It refuses a walk that arrives back at
// from (a cycle: from would redirect to itself) and one longer than the table
// (only reachable from a table that is already cyclic, i.e. one no writer here
// produced).
func collapse(table map[string]string, from, to string) (string, error) {
	seen := 0
	for {
		next, ok := table[to]
		if !ok {
			return to, nil
		}
		if next == from {
			return "", fmt.Errorf("target %q already redirects back to it, which would be a cycle", to)
		}
		to = next
		if seen++; seen > len(table) {
			return "", fmt.Errorf("target chain does not end: the table already holds a cycle")
		}
	}
}
