package importer

import (
	"bytes"
	"encoding/json"
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

// openStore opens dataDir's pack store and refuses a tree whose target families
// are still file-per-entity. The migration is a flag day (PACK-SPEC.md), so a
// legacy family is an error to report rather than a second write path to
// maintain - and failing here, before anything is planned, means a refused run
// has written nothing.
func openStore(dataDir string) (*pack.Store, error) {
	s, err := pack.Open(dataDir)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dataDir, err)
	}
	for _, f := range writeFamilies {
		if s.Layout(f) == pack.LayoutLegacy {
			return nil, fmt.Errorf("data/%s: %w; convert the tree to the pack layout before importing",
				f.Root(), pack.ErrLegacyLayout)
		}
	}
	return s, nil
}

// decodeEntry decodes a raw entry into a mutable map, preserving numbers exactly
// (json.Number). Exactness matters because an entry is read, edited in one
// field, and written back whole: a float round-trip would reformat every number
// the edit never touched, and a second identical run would stop being a
// byte-level no-op.
func decodeEntry(raw json.RawMessage) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, fmt.Errorf("entry is not a JSON object")
	}
	return m, nil
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
	m, err := decodeEntry(raw)
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
	recs, _ := entry["recordings"].(map[string]any)
	if recs == nil {
		recs = map[string]any{}
		entry["recordings"] = recs
	}
	recs[recSlug] = rec
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

// recLabel names a recording for a human-facing message. A pack path would be a
// worse answer here than the identity: the pack a slug sits in can change on a
// split, while the work/recording pair is what the message is actually about.
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
