// Package redirects reads, edits and writes the slug tombstone table
// (data/redirects.json) - see model.Redirects for what the table is for and what
// must hold of it.
//
// It is the WRITER's side of that table: Load, Add, Write is the whole flow a
// repair pass follows, and Add is where the one property every reader depends on
// is maintained - no target is itself a source, so resolution is one lookup.
// Reading the table back is pkg/check's job (it validates the file against
// schema/redirects.schema.json and leaves the table on the Catalog), so nothing
// here validates against the catalogue: this package knows the file, not the
// database.
//
// ITS ONE CALLER is internal/repair, the duplicate-merge pass this mechanism was
// built ahead of: retiring a slug without a tombstone is what had to be made
// impossible before any such pass ran, and every slug a merge retires is recorded
// here in the same change. The chain collapse is what that caller actually needs -
// waves land over each other, so today's target is tomorrow's source. The one
// repair that predates the mechanism, internal/remediate's GraphicAudio fold, was
// not retrofitted - it is a documented one-off that has done its work, so
// rewriting it would change nothing on disk.
//
// TWO GAPS ARE KNOWN AND DELIBERATELY LEFT OPEN, recorded here so neither is
// rediscovered as a surprise:
//
//   - The file has NO merge driver of its own. git's line merge is sound-or-loud
//     on it (unlike on a pack file, where it can duplicate a key - see
//     scripts/pack-union-merge.sh, which refuses this file), but it conflicts on a
//     large share of realistic two-PR append combinations, so the intake rebase
//     sweep can stall on it once redirects are being added regularly. A union
//     driver for this shape - a map of maps, no deletions to reason about - is the
//     fix when that starts happening.
//   - No minter steps OFF a tombstoned slug the way the work chain steps off a
//     reserved one (internal/importer's workCandidates). A later import that
//     re-creates a merged duplicate therefore lands on the retired slug and fails
//     metacheck's live-source rule: loud and safe, but not resolvable
//     mechanically. Teaching the importer and the issue forms to read the table is
//     the fix.
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
func Load(dataDir string) (model.Redirects, error) {
	raw, err := os.ReadFile(Path(dataDir))
	if err != nil {
		if os.IsNotExist(err) {
			return model.NewRedirects(), nil
		}
		return nil, err
	}
	// A repeated key is checked BEFORE decoding, for the reason pack.Parse checks
	// it: encoding/json keeps the last one, so a namespace or a retired slug
	// written twice would drop redirects here and lose them permanently the next
	// time Write rendered the file.
	if err := pack.CheckNoDuplicateKeys(raw); err != nil {
		return nil, fmt.Errorf("%s: %w", pack.RedirectsFile, err)
	}
	var got model.Redirects
	if err := json.Unmarshal(raw, &got); err != nil {
		return nil, fmt.Errorf("%s: %w", pack.RedirectsFile, err)
	}
	return normalize(got)
}

// Write renders r canonically to the redirects file under dataDir, creating the
// data root if it is not there.
//
// Every namespace is written, empty or not: the schema requires all three keys,
// so the file's shape is the same before and after a namespace gains its first
// entry and a diff only ever shows the entry.
func Write(dataDir string, r model.Redirects) error {
	full, err := normalize(r)
	if err != nil {
		return err
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

// normalize returns r with every namespace present and none that is not one of
// model's three. Both doors go through it: a table Load hands out must be
// indexable, a table Write renders must satisfy the schema's required keys, and
// an unknown namespace must fail rather than be silently dropped - a writer may
// never rewrite a file it did not fully understand.
func normalize(r model.Redirects) (model.Redirects, error) {
	out := model.NewRedirects()
	for kind, table := range r {
		if !model.ValidRedirectKind(kind) {
			return nil, fmt.Errorf("%s: unknown namespace %q (want one of %v)",
				pack.RedirectsFile, kind, model.RedirectKinds())
		}
		for from, to := range table {
			out[kind][from] = to
		}
	}
	return out, nil
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
// Either way every entry ends up naming a live record directly - the property
// model.Redirects documents and pkg/check enforces.
//
// It refuses rather than guesses: an unknown namespace, a slug that may not
// appear in the table (model.ValidRedirectSlug), a redirect from a slug that
// already points somewhere else, and a target that collapses back onto from
// (which would be a self-redirect, i.e. a cycle). Re-adding a redirect that is
// already recorded is a no-op, so a repair pass is idempotent.
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
	if err := model.ValidRedirectSlug(from); err != nil {
		return fmt.Errorf("%s redirect: source %q %w", kind, from, err)
	}
	if err := model.ValidRedirectSlug(to); err != nil {
		return fmt.Errorf("%s redirect %s: target %q %w", kind, from, to, err)
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
		return fmt.Errorf("%s redirect %s: the table already sends it to %q, and this would send it to %q: "+
			"retargeting a tombstone is a decision, not a merge", kind, from, cur, final)
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
// live slug at the end of that walk. It refuses a walk that arrives back at from
// (a cycle: from would redirect to itself), and one longer than the table, which
// is only reachable from a table that is already cyclic - i.e. one no writer here
// produced.
func collapse(table map[string]string, from, to string) (string, error) {
	for range len(table) + 1 {
		next, ok := table[to]
		if !ok {
			return to, nil
		}
		if next == from {
			return "", fmt.Errorf("target %q already redirects back to it, which would be a cycle", to)
		}
		to = next
	}
	return "", fmt.Errorf("target chain does not end: the table already holds a cycle")
}
