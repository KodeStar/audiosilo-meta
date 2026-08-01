package importer

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// write.go is the importer's storage layer: every record a run produces goes
// through a pack.Store instead of a file per entity.
//
// The shapes follow PACK-SPEC.md's families. A work and its recordings are ONE
// works-family entry (the composite: the work's own fields plus a "recordings"
// map keyed by recording slug), people and series are entries of their own.
// That makes the composite the unit of a write, so an edit to a single
// recording is a read-modify-write of its work's entry - putWorkEntry is
// therefore how every recording edit finishes.
//
// Nothing reaches disk until flush. The store answers reads queued-write-first,
// which is what preserves the per-file writer's compose-within-a-run semantics:
// a re-release ASIN merged into a recording an earlier row already wrote, or two
// enrichment rows filling different absent fields, see each other's edits.

// writeFamilies are the families the importer writes. works-community is not
// among them: the importer never touches the CC BY-SA sidecars.
var writeFamilies = []pack.Family{pack.FamilyWorks, pack.FamilyPeople, pack.FamilySeries}

// openStore opens dataDir for the families an import writes, refusing a tree
// whose target families are still file-per-entity (pack.OpenFor owns that rule).
//
// Opening also READS the tree, so it can now fail for reasons that have nothing
// to do with the layout - an unreadable directory, a data root that is not one -
// and it fails BEFORE anything is written rather than after. Only the layout
// refusal gets the clause naming who refused; an I/O error is reported as it
// came, since "metaimport writes the pack layout only" would be a false
// explanation of it.
func openStore(dataDir string) (*pack.Store, error) {
	s, err := pack.OpenFor(dataDir, writeFamilies...)
	if err != nil {
		if errors.Is(err, pack.ErrLegacyLayout) {
			return nil, fmt.Errorf("%w (metaimport writes the pack layout only)", err)
		}
		return nil, err
	}
	return s, nil
}

// putEntry marshals v and queues it as family f's entry for slug. It is a no-op
// once the run is fatal, so a caller never has to check first.
func (p *planner) putEntry(f pack.Family, slug string, v any) {
	if p.fatal != nil {
		return
	}
	data, err := json.Marshal(v)
	if err != nil {
		p.fatal = fmt.Errorf("marshal %s entry %q: %w", f.Root(), slug, err)
		return
	}
	if err := p.store.Upsert(f, slug, data); err != nil {
		p.fatal = fmt.Errorf("queue %s entry %q: %w", f.Root(), slug, err)
	}
}

// putNewEntry queues a record the run is CREATING, refusing to write over an
// entry that is already there.
//
// The guard is not belt-and-braces: the planner's identity maps come from
// check.Load's Catalog, which is best-effort. An entry the loader could not
// decode is reported as a problem but is ABSENT from the Catalog, so the planner
// believes its slug is free and the create branch would replace the whole entry
// - for a work, destroying every recording it held - while the run exited 0,
// because what it wrote back is itself valid. The write layer is the only place
// that can see what the loader dropped, so the check belongs here.
func (p *planner) putNewEntry(f pack.Family, slug string, v any) {
	if p.fatal != nil {
		return
	}
	taken, err := p.store.Has(f, slug)
	if err != nil {
		p.fatal = fmt.Errorf("read %s entry %q: %w", f.Root(), slug, err)
		return
	}
	if taken {
		p.fatal = fmt.Errorf("%s: refusing to create over an entry that is already there; "+
			"it is missing from the loaded catalogue, so run metacheck and fix what it reports",
			p.entryLocation(f, slug))
		return
	}
	p.putEntry(f, slug, v)
}

// entryLocation names an entry by the pack file holding it plus its key. It is
// the MAINTAINER-facing address form: a message that asks someone to go and fix
// a record has to say which file to open. Per-row warnings use the entity form
// instead (see recLabel), because a contributor triaging a warning has no file
// to open.
func (p *planner) entryLocation(f pack.Family, slug string) string {
	ref, err := p.store.Locate(f, slug)
	if err != nil {
		// Only reachable for a family the store may not touch, where naming the
		// entry is still more use than naming nothing.
		return fmt.Sprintf("%s entry %q", f.Root(), slug)
	}
	return fmt.Sprintf("%s: entry %q", ref.Path(), slug)
}

// entryRaw reads family f's entry for slug into a mutable map, queued-write
// first. A missing entry is fatal: every caller has already established that the
// record exists (from the catalogue load or from this run's own writes), so its
// absence means the store and the planner's identity maps disagree.
func (p *planner) entryRaw(f pack.Family, slug string) map[string]any {
	if p.fatal != nil {
		return nil
	}
	raw, ok, err := p.store.Get(f, slug)
	if err != nil {
		p.fatal = fmt.Errorf("read %s entry %q: %w", f.Root(), slug, err)
		return nil
	}
	if !ok {
		p.fatal = fmt.Errorf("read %s entry %q: no such entry", f.Root(), slug)
		return nil
	}
	m, err := pack.DecodeEntry(raw)
	if err != nil {
		p.fatal = fmt.Errorf("parse %s entry %q: %w", f.Root(), slug, err)
		return nil
	}
	return m
}

// workEntryRaw reads a work's composite entry: its own fields plus its
// "recordings" map.
func (p *planner) workEntryRaw(workSlug string) map[string]any {
	return p.entryRaw(pack.FamilyWorks, workSlug)
}

// putWorkEntry queues a work's whole composite entry. Every edit inside a
// composite - a work field or one recording - finishes here, because the entry
// is what the store stores.
func (p *planner) putWorkEntry(workSlug string, entry map[string]any) {
	p.putEntry(pack.FamilyWorks, workSlug, entry)
}

// putRecording splices rec into its work's composite entry under recSlug. rec is
// either a typed outRecording (a new recording) or an already-decoded raw map
// (an edited one).
func (p *planner) putRecording(workSlug, recSlug string, rec any) {
	entry := p.workEntryRaw(workSlug)
	if entry == nil {
		return
	}
	if err := pack.SetRecording(entry, recSlug, rec); err != nil {
		p.fatal = fmt.Errorf("works entry %q: %w", workSlug, err)
		return
	}
	p.putWorkEntry(workSlug, entry)
}

// recordingRaw returns a recording's own fields from inside its work's composite
// entry, together with that entry. The recording map is a live sub-map of the
// entry, so editing it edits the entry; the caller finishes with putWorkEntry.
func (p *planner) recordingRaw(workSlug, recSlug string) (entry, rec map[string]any) {
	entry = p.workEntryRaw(workSlug)
	if entry == nil {
		return nil, nil
	}
	recs, _ := entry["recordings"].(map[string]any)
	rec, _ = recs[recSlug].(map[string]any)
	if rec == nil {
		p.fatal = fmt.Errorf("read works entry %q: no recording %q", workSlug, recSlug)
		return nil, nil
	}
	return entry, rec
}

// recLabel names a recording for a per-row WARNING. Warnings are read as a run
// report, by someone triaging which books came out odd, so they name the entity
// rather than a location: there is nothing to open, and the pack a slug sits in
// changes on a split while the work/recording pair does not. Messages that ask
// someone to go and fix a record use the pack-path form instead (see
// entryLocation, and issueform's entryLocation for the intake side).
func recLabel(workSlug, recSlug string) string {
	return fmt.Sprintf("work %q recording %q", workSlug, recSlug)
}

// flush writes every queued entry, performing any due pack or directory splits.
// The store rewrites only the packs whose bytes actually change, so a run that
// queued nothing new leaves the tree untouched.
func (p *planner) flush() error {
	if _, err := p.store.Flush(); err != nil {
		return fmt.Errorf("write packs: %w", err)
	}
	return nil
}
