package check

// The pack-layout walker and the storage invariants that come with it. Every
// rule here is about the tree's SHAPE - where an entry sits, how big a pack is,
// what a file is named, whether a key agrees with the id inside it. What an
// entity says is still the schemas' job, and how entities relate is still
// rules.go's job on the assembled Catalog.
//
// The invariants are PACK-SPEC.md's, in its order: placement, caps, bound
// validity, license lock (schema-enforced, not restated here).

import (
	"bytes"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// entryPath renders the Path of a problem about one pack entry.
func entryPath(packPath, slug string) string {
	return model.PackLocation{Pack: packPath, Slug: slug}.String()
}

// recPath renders the Path of a problem about a recording nested in a works
// entry.
func recPath(packPath, slug, recSlug string) string {
	return model.PackLocation{Pack: packPath, Slug: slug, RecSlug: recSlug}.String()
}

// memberPath renders the Path of a problem about one member ("characters" or
// "recaps") of a works-community entry.
func memberPath(packPath, slug, member string) string {
	return entryPath(packPath, slug) + ": " + member
}

// rangeStr renders a half-open bound range for a message. An empty hi is
// unbounded above.
func rangeStr(lo, hi string) string {
	if hi == "" {
		return "[" + lo + ", end)"
	}
	return "[" + lo + ", " + hi + ")"
}

// loadPackFamily reads one pack-layout family: it checks the family's bound and
// directory invariants, then every pack's placement, caps, and entries. rels is
// the family's JSON files as the tree walk found them, used only to spot files
// the pack listing does not account for.
//
// The recordings it returns are already attached to their works (a works entry
// is a composite), so Load only needs their reporting paths.
func (l *loader) loadPackFamily(dir string, def pack.FamilyDef, rels []string) []recordWithPath {
	root := def.Family.Root()
	tree, err := pack.ReadTree(dir, def.Family)
	if err != nil {
		l.add(root, "read: %v", err)
		return nil
	}

	listed := map[string]bool{}
	for _, ref := range tree.Packs() {
		listed[ref.Path()] = true
	}
	for _, rel := range rels {
		if !listed[rel] {
			l.add(rel, "unrecognized location (a %s pack is %s/<bound>.json or %s/<dir-bound>/<bound>.json)", root, root, root)
		}
	}

	l.checkPackBounds(def, tree)

	var recs []recordWithPath
	for i, ref := range tree.Packs() {
		p := ref.Path()
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
		if err != nil {
			l.add(p, "read: %v", err)
			continue
		}
		file, err := pack.Parse(raw)
		if err != nil {
			l.add(p, "invalid pack: %s", collapse(err.Error()))
			continue
		}
		if file.Len() == 0 {
			l.add(p, "pack holds no entries (an empty pack is a file with no range to cover)")
			continue
		}
		total, per, err := file.Sizes()
		if err != nil {
			l.add(p, "canonical form: %s", collapse(err.Error()))
			continue
		}
		l.checkPackCaps(def, p, file.Len(), total)

		lo, hi, _ := tree.Range(i)
		for _, slug := range file.Slugs() {
			if !pack.Covers(lo, hi, slug) {
				l.add(entryPath(p, slug), "entry is outside the pack's range %s", rangeStr(lo, hi))
			}
			// An entry over the target size cannot be split out of its pack
			// (splitting divides entries, never one entry), so this is an
			// advisory to look at an outlier, not a violation.
			if per[slug] > pack.TargetSize {
				l.warn(entryPath(p, slug), "entry is %d bytes, over the %d-byte pack target: an oversized entry cannot be split and keeps its pack over target",
					per[slug], pack.TargetSize)
			}
			entry, _ := file.Get(slug)
			recs = append(recs, l.readPackEntry(def, p, slug, entry)...)
		}
	}
	return recs
}

// checkPackCaps enforces the hard caps a pack may not exceed. The size cap is
// waived for a single-entry pack: an entry larger than the cap cannot be split
// out, so enforcing it would fail on data nobody can fix (pack.Caps.Exceeds
// owns that exemption, so metacheck and metafmt can never disagree about it).
func (l *loader) checkPackCaps(def pack.FamilyDef, packPath string, entries, size int) {
	over, reason := def.Caps.Exceeds(entries, size)
	if !over {
		return
	}
	switch reason {
	case "entry count":
		l.add(packPath, "pack holds %d entries, over the %s entry cap of %d", entries, def.Family.Root(), def.Caps.Entries)
	default:
		l.add(packPath, "pack is %d bytes, over the hard cap of %d", size, def.Caps.HardSize)
	}
}

// checkPackBounds enforces a family's naming and ordering invariants: bounds are
// valid slugs, strictly increasing family-wide, the family's first pack and
// first directory carry the reserved minimum bound, a directory's bound equals
// its first pack's bound, every pack sits in its directory's range, and no
// directory holds more packs than the cap.
func (l *loader) checkPackBounds(def pack.FamilyDef, tree *pack.Tree) {
	packs := tree.Packs()
	if len(packs) == 0 {
		return
	}
	root := def.Family.Root()

	withDir, flat := 0, 0
	for _, ref := range packs {
		if !model.ValidSlug(ref.Bound) {
			l.add(ref.Path(), "pack name %q is not a valid slug bound", ref.Bound)
		}
		if ref.Dir == "" {
			flat++
			if def.Dirs {
				l.add(ref.Path(), "pack sits directly under %s/, but this family carries a directory level", root)
			}
			continue
		}
		withDir++
		if !model.ValidSlug(ref.Dir) {
			l.add(path.Join(root, ref.Dir), "directory name %q is not a valid slug bound", ref.Dir)
		}
	}
	if withDir > 0 && flat > 0 {
		l.add(root, "family mixes packs directly under its root (%d) with packs in directories (%d): it is either flat or one level deep, never both", flat, withDir)
	}

	// "0" is the lowest string any slug can start with, so a bound of MinBound
	// always sorts first: naming a later pack "0" shows up as a duplicate bound
	// or an out-of-range pack rather than needing its own rule.
	if packs[0].Bound != pack.MinBound {
		l.add(packs[0].Path(), "the family's first pack must be named %q (the reserved minimum bound), not %q", pack.MinBound, packs[0].Bound)
	}
	for i := 1; i < len(packs); i++ {
		if packs[i].Bound <= packs[i-1].Bound {
			l.add(packs[i].Path(), "bound %q does not increase on %s", packs[i].Bound, packs[i-1].Path())
		}
	}

	if withDir == 0 {
		// A flat family has no directory invariants, but it still has to gain
		// the directory level once it outgrows the per-directory pack cap.
		if len(packs) > def.Caps.DirPacks {
			l.add(root, "family holds %d packs, over the per-directory cap of %d: it has to gain a directory level", len(packs), def.Caps.DirPacks)
		}
		return
	}

	dirs := tree.Dirs()
	sort.Strings(dirs)
	if dirs[0] != pack.MinBound {
		l.add(path.Join(root, dirs[0]), "the family's first directory must be named %q (the reserved minimum bound), not %q", pack.MinBound, dirs[0])
	}
	for i, d := range dirs {
		dp := tree.DirPacks(d)
		if len(dp) == 0 {
			continue
		}
		if dp[0].Bound != d {
			l.add(path.Join(root, d), "directory bound %q must equal its first pack's bound %q", d, dp[0].Bound)
		}
		if len(dp) > def.Caps.DirPacks {
			l.add(path.Join(root, d), "directory holds %d packs, over the cap of %d", len(dp), def.Caps.DirPacks)
		}
		var next string
		if i+1 < len(dirs) {
			next = dirs[i+1]
		}
		for _, ref := range dp {
			if !pack.Covers(d, next, ref.Bound) {
				l.add(ref.Path(), "pack is outside its directory's range %s", rangeStr(d, next))
			}
		}
	}
}

// readPackEntry validates one entry and folds it into the catalog.
func (l *loader) readPackEntry(def pack.FamilyDef, packPath, slug string, raw json.RawMessage) []recordWithPath {
	if !model.ValidSlug(slug) {
		l.add(entryPath(packPath, slug), "entry key %q is not a valid slug", slug)
	}
	switch def.Family {
	case pack.FamilyWorks:
		return l.readWorkEntry(packPath, slug, raw)
	case pack.FamilyWorksCommunity:
		l.readCommunityEntry(packPath, slug, raw)
	case pack.FamilyPeople:
		l.readPersonEntry(packPath, slug, raw)
	case pack.FamilySeries:
		l.readSeriesEntry(packPath, slug, raw)
	}
	return nil
}

// instance parses an entry (or one of its members) into the shape the schema
// validator consumes. ok is false when it is not JSON at all, which is only
// reachable for a member: pack.Parse already proved the entry itself parses.
func (l *loader) instance(reportPath string, raw json.RawMessage) (any, bool) {
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		l.add(reportPath, "invalid JSON: %s", collapse(err.Error()))
		return nil, false
	}
	return inst, true
}

// readWorkEntry reads a works-family composite: a work's own fields plus its
// recordings keyed by recording slug.
func (l *loader) readWorkEntry(packPath, slug string, raw json.RawMessage) []recordWithPath {
	ep := entryPath(packPath, slug)
	inst, ok := l.instance(ep, raw)
	if !ok {
		return nil
	}
	// The work schema carries the whole composite - it defines "recordings" and
	// $refs the recording schema - so one validation covers both layers. A
	// violation that landed inside the recordings map is re-reported against
	// the recording it belongs to, so its path stays precise.
	for _, v := range l.schemas.violations(model.KindWork, inst) {
		p, msg := routeRecordingViolation(packPath, slug, v)
		l.add(p, "%s", msg)
	}

	e, ok := decodeWorkEntry(raw)
	if !ok {
		return nil
	}
	w := e.Compose()
	if w.ID != slug {
		l.add(ep, "id %q does not match its entry key %q", w.ID, slug)
	}
	l.cat.Works = append(l.cat.Works, w)
	l.idx.work[w] = ep

	recSlugs := make([]string, 0, len(e.Recordings))
	for k := range e.Recordings {
		recSlugs = append(recSlugs, k)
	}
	sort.Strings(recSlugs)

	out := make([]recordWithPath, 0, len(recSlugs))
	for _, rs := range recSlugs {
		r := e.Recordings[rs]
		if r == nil {
			continue
		}
		rp := recPath(packPath, slug, rs)
		if r.ID != rs {
			l.add(rp, "id %q does not match its recordings map key %q", r.ID, rs)
		}
		if r.Work != slug {
			l.add(rp, "work %q must equal the parent entry key %q", r.Work, slug)
		}
		l.idx.rec[r] = rp
		// Compose already hung the recordings off the work, in this same order.
		out = append(out, recordWithPath{rec: r, workSlug: slug, path: rp, attached: true})
	}
	return out
}

// decodeWorkEntry reads a composite one part at a time rather than through
// pack.WorkEntry's own all-or-nothing UnmarshalJSON, so a recording whose type
// the schema already rejected is dropped on its own instead of taking its work
// (and everything that references it) out of the catalog with it.
func decodeWorkEntry(raw json.RawMessage) (pack.WorkEntry, bool) {
	var w model.Work
	if json.Unmarshal(raw, &w) != nil {
		return pack.WorkEntry{}, false
	}
	e := pack.WorkEntry{Work: &w}
	var aux struct {
		Recordings map[string]json.RawMessage `json:"recordings"`
	}
	if json.Unmarshal(raw, &aux) != nil || len(aux.Recordings) == 0 {
		return e, true
	}
	e.Recordings = make(map[string]*model.Recording, len(aux.Recordings))
	for slug, rr := range aux.Recordings {
		var r model.Recording
		if json.Unmarshal(rr, &r) != nil {
			continue
		}
		e.Recordings[slug] = &r
	}
	return e, true
}

// routeRecordingViolation re-points a composite violation that landed inside
// the recordings map at the recording itself, and rebases its instance location
// so the message reads relative to that recording.
func routeRecordingViolation(packPath, slug string, v violation) (reportPath, msg string) {
	const prefix = "/recordings/"
	if !strings.HasPrefix(v.Loc, prefix) {
		return entryPath(packPath, slug), v.String()
	}
	recSlug, tail, _ := strings.Cut(strings.TrimPrefix(v.Loc, prefix), "/")
	inner := violation{Loc: "(root)", Msg: v.Msg}
	if tail != "" {
		inner.Loc = "/" + tail
	}
	return recPath(packPath, slug, recSlug), inner.String()
}

// communityMembers are the only members a works-community entry may hold. Both
// are optional; an entry with neither is not an entry.
var communityMembers = map[string]model.Kind{
	"characters": model.KindCharacters,
	"recaps":     model.KindRecaps,
}

// readCommunityEntry reads a works-community entry: the CC BY-SA sidecars for
// one work, keyed by that work's slug. Each member is validated against its own
// schema, which is where the share-alike license lock lives.
func (l *loader) readCommunityEntry(packPath, slug string, raw json.RawMessage) {
	ep := entryPath(packPath, slug)
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		l.add(ep, "invalid entry: %s", collapse(err.Error()))
		return
	}
	if len(members) == 0 {
		l.add(ep, "entry holds neither characters nor recaps")
		return
	}
	names := make([]string, 0, len(members))
	for n := range members {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		kind, known := communityMembers[name]
		if !known {
			l.add(ep, "unknown member %q (a works-community entry holds only characters and recaps)", name)
			continue
		}
		mp := memberPath(packPath, slug, name)
		inst, ok := l.instance(mp, members[name])
		if !ok {
			continue
		}
		for _, m := range l.schemas.validate(kind, inst) {
			l.add(mp, "%s", m)
		}
		switch kind {
		case model.KindCharacters:
			var c model.Characters
			if json.Unmarshal(members[name], &c) != nil {
				continue
			}
			if c.Work != slug {
				l.add(mp, "work %q must equal the entry key %q", c.Work, slug)
			}
			l.cat.Characters = append(l.cat.Characters, &c)
			l.idx.characters[&c] = mp
		case model.KindRecaps:
			var rc model.Recaps
			if json.Unmarshal(members[name], &rc) != nil {
				continue
			}
			if rc.Work != slug {
				l.add(mp, "work %q must equal the entry key %q", rc.Work, slug)
			}
			l.cat.Recaps = append(l.cat.Recaps, &rc)
			l.idx.recaps[&rc] = mp
		}
	}
}

func (l *loader) readPersonEntry(packPath, slug string, raw json.RawMessage) {
	ep := entryPath(packPath, slug)
	inst, ok := l.instance(ep, raw)
	if !ok {
		return
	}
	for _, m := range l.schemas.validate(model.KindPerson, inst) {
		l.add(ep, "%s", m)
	}
	var p model.Person
	if json.Unmarshal(raw, &p) != nil {
		return
	}
	if p.ID != slug {
		l.add(ep, "id %q does not match its entry key %q", p.ID, slug)
	}
	l.cat.People = append(l.cat.People, &p)
	l.idx.person[&p] = ep
}

func (l *loader) readSeriesEntry(packPath, slug string, raw json.RawMessage) {
	ep := entryPath(packPath, slug)
	inst, ok := l.instance(ep, raw)
	if !ok {
		return
	}
	for _, m := range l.schemas.validate(model.KindSeries, inst) {
		l.add(ep, "%s", m)
	}
	var s model.Series
	if json.Unmarshal(raw, &s) != nil {
		return
	}
	if s.ID != slug {
		l.add(ep, "id %q does not match its entry key %q", s.ID, slug)
	}
	l.cat.Series = append(l.cat.Series, &s)
	l.idx.series[&s] = ep
}
