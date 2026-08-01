// Package migrate converts a file-per-entity data tree into the range-packed
// layout PACK-SPEC.md defines, and backfills the added_at field the pack layout
// makes necessary.
//
// It is a ONE-OFF: the repository's tree is converted once, in a single commit,
// and every writer afterwards speaks packs only (pkg/pack refuses a legacy
// family with ErrLegacyLayout). The tool survives the flag day because the same
// conversion is what a fork, a scratch spike tree, or a restored old backup
// needs, and because it is the only place that still knows how to read the old
// layout - see legacy.go, which carries its own path parsing for exactly that
// reason.
//
// What it does, in order:
//
//  1. Read the whole legacy tree into memory (48MB of JSON at today's scale).
//  2. Walk git history once for every work's and recording's first-add date,
//     with release.yml's retired added.tsv semantics (added.go).
//  3. Compose pack entries from the RAW legacy bytes, splicing in added_at and
//     nothing else: a work's composite gains its recordings, the two community
//     sidecars pair up into one works-community entry, people and series are
//     carried through untouched.
//  4. Plan packs and directories (plan.go), render every pack's bytes.
//  5. Delete the old files, then write the new ones.
//
// Step 5's order is load-bearing: a directory bound is a work slug, so a
// two-character work slug ("it") names a directory that the legacy shard "it"
// already occupies. Everything is in memory by then, so a failed render cannot
// have deleted anything.
//
// It is NOT crash-safe on its own, and does not pretend to be: a run killed
// between the delete and the write leaves a half-converted tree, and a re-run
// over what survived would convert that subset perfectly happily. The safety net
// is git, and Run insists on it - an in-place conversion is refused unless the
// data tree is committed and clean, so the recovery from any interruption is
// `git checkout -- data` (or `git status`, which shows the damage immediately).
// A conversion that found no works at all is refused for the same reason.
package migrate

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// Options configure one conversion.
type Options struct {
	// DataDir is the legacy data root to read. It is also where the git-history
	// backfill is anchored, so it must sit inside the repository whose history
	// dates the records.
	DataDir string
	// OutDir is where the pack tree is written. Empty means in place: the legacy
	// files are deleted and the packs take their place. A different directory
	// leaves DataDir untouched, which is how a conversion is rehearsed.
	OutDir string
	// RepoDir overrides the repository the backfill walks. Empty resolves it
	// from DataDir.
	RepoDir string
	// Backfill runs the git-history added_at walk. Off is only for a tree with
	// no history to read (a fixture, a spike); the real conversion needs it, and
	// the artifact equivalence proof depends on it.
	Backfill bool
}

// Summary is what a conversion did.
type Summary struct {
	// InPlace reports that the legacy files were deleted and replaced.
	InPlace bool
	// Works, Recordings, People, Series and Community count the entities
	// converted (Community is works carrying at least one sidecar).
	Works      int
	Recordings int
	People     int
	Series     int
	Community  int
	// DatedWorks and DatedRecordings count the records the git walk dated.
	DatedWorks      int
	DatedRecordings int
	// Packs and Dirs count the files and directories written, per family.
	Packs map[pack.Family]int
	Dirs  map[pack.Family]int
	// Removed is the number of legacy files deleted.
	Removed int
}

// TotalPacks returns the number of pack files written.
func (s Summary) TotalPacks() int {
	n := 0
	for _, c := range s.Packs {
		n += c
	}
	return n
}

// Run converts the tree described by opts.
func Run(opts Options) (Summary, error) {
	sum := Summary{
		InPlace: opts.OutDir == "" || opts.OutDir == opts.DataDir,
		Packs:   map[pack.Family]int{},
		Dirs:    map[pack.Family]int{},
	}
	outDir := opts.OutDir
	if outDir == "" {
		outDir = opts.DataDir
	}

	if err := refuseConverted(opts.DataDir); err != nil {
		return Summary{}, err
	}
	if sum.InPlace {
		if err := refuseUncommitted(opts.DataDir); err != nil {
			return Summary{}, err
		}
	} else if err := refuseOccupied(outDir); err != nil {
		return Summary{}, err
	}

	tree, err := scan(opts.DataDir)
	if err != nil {
		return Summary{}, err
	}
	// A conversion that found no works is not a conversion. It is the shape an
	// interrupted in-place run leaves behind (and the shape a wrong --data
	// leaves), and reporting success over it would be this tool destroying a
	// database and saying it went well.
	if len(tree.works) == 0 {
		return Summary{}, fmt.Errorf("%s holds no works: nothing to convert (is this the data directory, "+
			"and is it the tree you meant?)", opts.DataDir)
	}

	dates := newAddedDates()
	if opts.Backfill {
		repo, spec, rerr := repoTarget(opts.DataDir, opts.RepoDir)
		if rerr != nil {
			return Summary{}, rerr
		}
		if dates, err = gitAddedDates(repo, spec); err != nil {
			return Summary{}, err
		}
		if dates.empty() {
			return Summary{}, fmt.Errorf("the git-history walk of %s in %s dated nothing: "+
				"the records cannot be backfilled from a history that does not describe them", spec, repo)
		}
	}

	entries, err := composeAll(tree, dates, &sum)
	if err != nil {
		return Summary{}, err
	}

	// Render everything BEFORE anything is deleted: a failed render must leave
	// the tree exactly as it was.
	files := map[string][]byte{}
	for _, def := range pack.Families() {
		overhead, merr := measure(entries[def.Family])
		if merr != nil {
			return Summary{}, fmt.Errorf("%s: %w", def.Family.Root(), merr)
		}
		packs := planFamily(def, entries[def.Family], overhead)
		dirs := map[string]bool{}
		for _, p := range packs {
			data, rerr := render(p)
			if rerr != nil {
				return Summary{}, fmt.Errorf("%s: %w", p.ref(def.Family), rerr)
			}
			files[p.ref(def.Family).Path()] = data
			dirs[p.dir] = true
		}
		sum.Packs[def.Family] = len(packs)
		if len(packs) > 0 && packs[0].dir != "" {
			sum.Dirs[def.Family] = len(dirs)
		}
	}

	if sum.InPlace {
		if err := removeAll(opts.DataDir, tree.files); err != nil {
			return Summary{}, err
		}
		sum.Removed = len(tree.files)
	}

	written := make([]string, 0, len(files))
	for rel := range files {
		written = append(written, rel)
	}
	sort.Strings(written)
	for _, rel := range written {
		abs := filepath.Join(outDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return Summary{}, err
		}
		if err := os.WriteFile(abs, files[rel], 0o644); err != nil {
			return Summary{}, err
		}
	}
	return sum, nil
}

// refuseConverted rejects a tree that is already (even partly) in pack layout,
// so a second run cannot re-pack packs into packs.
//
// It names the file that made the family read as converted. A family in the old
// layout classifies as pack the moment ONE pack-shaped file lands in it, and
// then every legacy record under it is invisible to this check - so the message
// has to point at the file rather than assert that there is nothing to do.
func refuseConverted(dataDir string) error {
	layouts, err := pack.Detect(dataDir)
	if err != nil {
		return err
	}
	for _, def := range pack.Families() {
		if layouts[def.Family] != pack.LayoutPack {
			continue
		}
		culprit, ferr := firstPackFile(dataDir, def.Family)
		if ferr != nil {
			return ferr
		}
		return fmt.Errorf("%s/%s already reads as the pack layout (%s is a pack file): "+
			"nothing to migrate. If that file does not belong there, remove it - while it is there, "+
			"every file-per-record file in that family is ignored",
			dataDir, def.Family.Root(), culprit)
	}
	return nil
}

// firstPackFile returns the data-relative path of the lexically first file under
// the family root that parses as a pack.
func firstPackFile(dataDir string, f pack.Family) (string, error) {
	var found string
	root := filepath.Join(dataDir, f.Root())
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) && p == root {
				return fs.SkipAll
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".json") {
			return nil
		}
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		if _, perr := pack.Parse(raw); perr != nil {
			return nil
		}
		rel, rerr := filepath.Rel(dataDir, p)
		if rerr != nil {
			return rerr
		}
		found = filepath.ToSlash(rel)
		return fs.SkipAll
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return path.Join(f.Root(), "(a file the pack reader accepted)"), nil
	}
	return found, nil
}

// refuseUncommitted rejects an in-place conversion of a data tree that is not
// committed and clean.
//
// The conversion deletes the whole old tree and writes the new one; it is not
// atomic, and it cannot be. What makes that acceptable is that the old tree is
// one `git checkout -- data` away at every moment - which is only true if it was
// committed when the run started. It is also the check that stops the worst
// re-run: an interrupted conversion leaves a half-deleted tree that a second run
// would convert, happily and completely, into a fraction of the database.
//
// A tree that is not in a git repository at all has no such net and no such
// signal; it is allowed, because fixtures and scratch trees are exactly that,
// and the package doc says what the model is.
func refuseUncommitted(dataDir string) error {
	if _, err := gitOutput(dataDir, "rev-parse", "--is-inside-work-tree"); err != nil {
		return nil // not a repository: nothing to check, and nothing to restore from
	}
	status, err := gitOutput(dataDir, "status", "--porcelain", "--", ".")
	if err != nil {
		return err
	}
	if status == "" {
		return nil
	}
	return fmt.Errorf("%s has uncommitted changes:\n%s\n"+
		"commit or stash them first. The conversion deletes the whole tree before writing the new one, "+
		"so a committed tree is what makes `git checkout -- %s` a complete recovery - and an already "+
		"half-converted tree (an interrupted run) shows up here rather than being converted again",
		dataDir, status, dataDir)
}

// refuseOccupied rejects an output directory that already holds one of the
// families, so a rehearsal run cannot half-overwrite an earlier one.
func refuseOccupied(outDir string) error {
	for _, def := range pack.Families() {
		root := filepath.Join(outDir, def.Family.Root())
		ents, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if len(ents) > 0 {
			return fmt.Errorf("%s is not empty: refusing to write into an occupied output tree", root)
		}
	}
	return nil
}

// composeAll builds every family's entries, in slug order.
func composeAll(t *legacyTree, dates addedDates, sum *Summary) (map[pack.Family][]entry, error) {
	out := map[pack.Family][]entry{}

	for _, slug := range t.workSlugs() {
		w := t.works[slug]
		raw, dated, err := workEntry(w, dates)
		if err != nil {
			return nil, fmt.Errorf("works entry %q: %w", slug, err)
		}
		e, err := newEntry(slug, raw)
		if err != nil {
			return nil, err
		}
		out[pack.FamilyWorks] = append(out[pack.FamilyWorks], e)
		sum.Works++
		sum.Recordings += len(w.recs)
		sum.DatedWorks += dated.works
		sum.DatedRecordings += dated.recs

		if w.characters == nil && w.recaps == nil {
			continue
		}
		ce, err := newEntry(slug, communityEntry(w))
		if err != nil {
			return nil, err
		}
		out[pack.FamilyWorksCommunity] = append(out[pack.FamilyWorksCommunity], ce)
		sum.Community++
	}

	for _, slug := range sortedKeys(t.people) {
		e, err := newEntry(slug, t.people[slug])
		if err != nil {
			return nil, err
		}
		out[pack.FamilyPeople] = append(out[pack.FamilyPeople], e)
		sum.People++
	}
	for _, slug := range sortedKeys(t.series) {
		e, err := newEntry(slug, t.series[slug])
		if err != nil {
			return nil, err
		}
		out[pack.FamilySeries] = append(out[pack.FamilySeries], e)
		sum.Series++
	}
	return out, nil
}

// datedCount counts the added_at values one work's composite received.
type datedCount struct{ works, recs int }

// workEntry composes a works-family entry: the work's own bytes plus its
// recordings keyed by recording slug, with added_at spliced in.
//
// The entry is built from the record's RAW bytes, decoded into a map with
// numbers held exactly (pack.DecodeEntry), never from a re-marshalled typed
// struct: a struct round-trip would emit every zero-valued field the source
// never stated - an "abridged": false on a recording nobody said was abridged -
// and would drop any field the model does not know about. added_at is the only
// value this function adds.
//
// A git-derived date OVERWRITES an added_at the record already carries. The walk
// is the authority for the one-time backfill: it is what the retired release.yml
// step fed into the artifact, so it is what keeps the artifact's bytes identical
// across the migration. Records the walk did not date (never committed) keep
// whatever they had.
func workEntry(w *legacyWork, dates addedDates) (json.RawMessage, datedCount, error) {
	var dated datedCount
	m, err := pack.DecodeEntry(w.work)
	if err != nil {
		return nil, dated, err
	}
	if d := dates.works[w.slug]; d != "" {
		m["added_at"] = d
		dated.works++
	}
	if len(w.recs) > 0 {
		recs := make(map[string]any, len(w.recs))
		for _, rs := range sortedKeys(w.recs) {
			rm, rerr := pack.DecodeEntry(w.recs[rs])
			if rerr != nil {
				return nil, dated, fmt.Errorf("recording %q: %w", rs, rerr)
			}
			if d := dates.rec(w.slug, rs); d != "" {
				rm["added_at"] = d
				dated.recs++
			}
			recs[rs] = rm
		}
		m["recordings"] = recs
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, dated, err
	}
	return raw, dated, nil
}

// communityEntry pairs a work's two CC BY-SA sidecars into one
// works-community entry. Either member may be absent; the bytes of the ones
// present are carried through verbatim.
func communityEntry(w *legacyWork) json.RawMessage {
	var buf []byte
	buf = append(buf, '{')
	if w.characters != nil {
		buf = append(buf, `"characters":`...)
		buf = append(buf, w.characters...)
	}
	if w.recaps != nil {
		if w.characters != nil {
			buf = append(buf, ',')
		}
		buf = append(buf, `"recaps":`...)
		buf = append(buf, w.recaps...)
	}
	return append(buf, '}')
}
