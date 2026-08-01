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
// directory invariants, then every pack's placement, caps, and entries. tree is
// the family's pack listing and rels is its JSON files, both as the ONE tree
// walk found them; rels is used only to spot files the pack listing does not
// account for.
//
// The recordings it returns are already attached to their works (a works entry
// is a composite), so Load only needs their reporting paths.
func (l *loader) loadPackFamily(def pack.FamilyDef, tree *pack.Tree, rels []string) []recordWithPath {
	root := def.Family.Root()

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
		file, ok := l.readPack(p)
		if !ok {
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

// readPack returns the parsed pack at the data-relative path p, reporting a
// read or parse failure against the file itself. ok is false when there is
// nothing to validate.
//
// It goes through the loader's shared parse cache: a pack a writer's store has
// already read is taken from there, and one this load reads is left there for
// the store. The reading is still done here rather than through Reader.Read
// because a missing file is a PROBLEM to this package (a listed pack that has
// gone away) where the store reads it as an empty pack to write into, and
// because the two failures are reported differently.
func (l *loader) readPack(p string) (*pack.File, bool) {
	if file, ok := l.rdr.Cached(p); ok {
		return file, true
	}
	raw, err := os.ReadFile(filepath.Join(l.dir, filepath.FromSlash(p)))
	if err != nil {
		l.add(p, "read: %v", err)
		return nil, false
	}
	file, err := pack.Parse(raw)
	if err != nil {
		l.add(p, "invalid pack: %s", collapse(err.Error()))
		return nil, false
	}
	l.rdr.Hold(p, file)
	return file, true
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
		// Once per directory, not once per pack in it: a badly named directory
		// is one fact, however many packs happen to sit under it.
		if !model.ValidSlug(d) {
			l.add(path.Join(root, d), "directory name %q is not a valid slug bound", d)
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

// readPackEntry validates one entry and folds it into the catalog. The entry
// key is validated inside each reader, through the family wrapper's own
// propertyNames rule.
func (l *loader) readPackEntry(def pack.FamilyDef, packPath, slug string, raw json.RawMessage) []recordWithPath {
	switch def.Family {
	case pack.FamilyWorks:
		return l.readWorkEntry(def.Family, packPath, slug, raw)
	case pack.FamilyWorksCommunity:
		l.readCommunityEntry(def.Family, packPath, slug, raw)
	case pack.FamilyPeople:
		l.readPersonEntry(def.Family, packPath, slug, raw)
	case pack.FamilySeries:
		l.readSeriesEntry(def.Family, packPath, slug, raw)
	}
	return nil
}

// instance parses an entry into the shape the schema validator consumes. ok is
// false when it is not JSON at all, which pack.Parse has already ruled out for
// an entry read from a pack file.
func (l *loader) instance(reportPath string, raw json.RawMessage) (any, bool) {
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		l.add(reportPath, "invalid JSON: %s", collapse(err.Error()))
		return nil, false
	}
	return inst, true
}

// validateEntry checks one entry, and its key, against the family wrapper's own
// declaration of what an entry is. route re-points a violation that landed
// inside a named sub-value (a nested recording, a sidecar member) at that
// sub-value, so a problem names the smallest thing that is wrong.
//
// It reports whether the entry is schema-clean, which is what makes a later Go
// decode failure meaningful: a value the schema already rejected explains
// itself, but one it accepted and Go cannot represent would otherwise vanish.
func (l *loader) validateEntry(family pack.Family, packPath, slug string, raw json.RawMessage,
	route func(violation) (string, violation),
) bool {
	ep := entryPath(packPath, slug)
	clean := true
	for _, v := range l.schemas.keyViolations(family, slug) {
		l.add(ep, "entry key %q is not a valid slug: %s", slug, v.Msg)
		clean = false
	}
	inst, ok := l.instance(ep, raw)
	if !ok {
		return false
	}
	for _, v := range l.schemas.entryViolations(family, inst) {
		clean = false
		p, rebased := ep, v
		if route != nil {
			p, rebased = route(v)
		}
		l.add(p, "%s", rebased.String())
	}
	return clean
}

// readWorkEntry reads a works-family composite: a work's own fields plus its
// recordings keyed by recording slug.
func (l *loader) readWorkEntry(family pack.Family, packPath, slug string, raw json.RawMessage) []recordWithPath {
	ep := entryPath(packPath, slug)
	// The works wrapper says an entry is a work, and the work schema defines
	// "recordings" and $refs the recording schema, so one validation covers both
	// layers; a violation inside the recordings map is re-reported against the
	// recording it belongs to.
	clean := l.validateEntry(family, packPath, slug, raw, func(v violation) (string, violation) {
		if rec, inner, ok := rebaseInMap(v, "recordings"); ok {
			return recPath(packPath, slug, rec), inner
		}
		return ep, v
	})

	e, err := decodeWorkEntry(raw)
	if err != nil {
		l.reportUndecodable(ep, clean, err)
		return nil
	}
	w := e.entry.Compose()
	if w.ID != slug {
		l.add(ep, "id %q does not match its entry key %q", w.ID, slug)
	}
	l.cat.Works = append(l.cat.Works, w)
	l.idx.work[w] = ep

	for _, rs := range e.badRecordings {
		l.reportUndecodable(recPath(packPath, slug, rs.slug), clean, rs.err)
	}

	recSlugs := make([]string, 0, len(e.entry.Recordings))
	for k := range e.entry.Recordings {
		recSlugs = append(recSlugs, k)
	}
	sort.Strings(recSlugs)

	out := make([]recordWithPath, 0, len(recSlugs))
	for _, rs := range recSlugs {
		r := e.entry.Recordings[rs]
		rp := recPath(packPath, slug, rs)
		if r.ID != rs {
			l.add(rp, "id %q does not match its recordings map key %q", r.ID, rs)
		}
		if r.Work != slug {
			l.add(rp, "work %q must equal the parent entry key %q", r.Work, slug)
		}
		l.idx.rec[r] = rp
		// Compose already hung the recordings off the work, in this same order.
		out = append(out, recordWithPath{rec: r, workSlug: slug, path: rp})
	}
	return out
}

// reportUndecodable reports a record the schema accepted but Go cannot
// represent - "runtime_min": 1e3 is a valid JSON-Schema integer and not an int.
// Skipping it silently would drop it from the catalog and the artifact with the
// gate still green, so it is a problem, always. When the schema already
// rejected the record the decode failure is a consequence of that and adds
// nothing, so it is left to the schema message.
func (l *loader) reportUndecodable(reportPath string, schemaClean bool, err error) {
	if !schemaClean {
		return
	}
	l.add(reportPath, "passes its schema but cannot be decoded into the model, so it would be silently lost: %s",
		collapse(err.Error()))
}

// decodedWork is a composite decoded one part at a time.
type decodedWork struct {
	entry pack.WorkEntry
	// badRecordings holds the recordings that would not decode. They are kept
	// out of the catalog but never dropped silently - one unrepresentable
	// recording must not take its work, or everything referencing that work,
	// down with it.
	badRecordings []badRecording
}

type badRecording struct {
	slug string
	err  error
}

// decodeWorkEntry reads a composite in a single pass: the work's own fields
// through model.Work, the recordings left raw so each can be decoded (and
// failed) on its own. pack.WorkEntry is a plain struct with no JSON methods, so
// the composite's shape is spelled out here, once.
func decodeWorkEntry(raw json.RawMessage) (decodedWork, error) {
	var doc struct {
		model.Work
		Recordings map[string]json.RawMessage `json:"recordings"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return decodedWork{}, err
	}
	work := doc.Work
	out := decodedWork{entry: pack.WorkEntry{Work: &work}}
	if len(doc.Recordings) == 0 {
		return out, nil
	}
	out.entry.Recordings = make(map[string]*model.Recording, len(doc.Recordings))
	slugs := make([]string, 0, len(doc.Recordings))
	for slug := range doc.Recordings {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	for _, slug := range slugs {
		var r model.Recording
		if err := json.Unmarshal(doc.Recordings[slug], &r); err != nil {
			out.badRecordings = append(out.badRecordings, badRecording{slug: slug, err: err})
			continue
		}
		out.entry.Recordings[slug] = &r
	}
	return out, nil
}

// rebaseInMap re-points a violation that landed inside a named map at the map's
// member, returning that member's key and the violation rebased onto it.
func rebaseInMap(v violation, member string) (key string, inner violation, ok bool) {
	rest, ok := v.rebase("/" + member)
	if !ok || rest.Loc == "" {
		return "", v, false
	}
	key, tail, _ := strings.Cut(strings.TrimPrefix(rest.Loc, "/"), "/")
	inner = violation{Msg: v.Msg}
	if tail != "" {
		inner.Loc = "/" + tail
	}
	return unescapePointerToken(key), inner, true
}

// unescapePointerToken decodes RFC 6901's escapes. A slug never needs them, but
// an invalid key that does must still be named as it is written.
func unescapePointerToken(tok string) string {
	tok = strings.ReplaceAll(tok, "~1", "/")
	return strings.ReplaceAll(tok, "~0", "~")
}

// communityMembers are the members a works-community entry may hold. The
// wrapper schema is what REJECTS anything else (and an entry holding neither);
// this map only says how to fold an accepted member into the catalog.
var communityMembers = map[string]model.Kind{
	"characters": model.KindCharacters,
	"recaps":     model.KindRecaps,
}

// readCommunityEntry reads a works-community entry: the CC BY-SA sidecars for
// one work, keyed by that work's slug. The wrapper's entry schema carries both
// members, so one validation covers the member set, the "at least one" rule and
// each sidecar's own schema - including the share-alike license lock.
func (l *loader) readCommunityEntry(family pack.Family, packPath, slug string, raw json.RawMessage) {
	ep := entryPath(packPath, slug)
	clean := l.validateEntry(family, packPath, slug, raw, func(v violation) (string, violation) {
		for name := range communityMembers {
			if inner, ok := v.rebase("/" + name); ok {
				return memberPath(packPath, slug, name), inner
			}
		}
		return ep, v
	})

	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		l.reportUndecodable(ep, clean, err)
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
			continue // the wrapper schema already rejected it
		}
		mp := memberPath(packPath, slug, name)
		switch kind {
		case model.KindCharacters:
			var c model.Characters
			if err := json.Unmarshal(members[name], &c); err != nil {
				l.reportUndecodable(mp, clean, err)
				continue
			}
			if c.Work != slug {
				l.add(mp, "work %q must equal the entry key %q", c.Work, slug)
			}
			l.cat.Characters = append(l.cat.Characters, &c)
			l.idx.characters[&c] = mp
		case model.KindRecaps:
			var rc model.Recaps
			if err := json.Unmarshal(members[name], &rc); err != nil {
				l.reportUndecodable(mp, clean, err)
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

func (l *loader) readPersonEntry(family pack.Family, packPath, slug string, raw json.RawMessage) {
	ep := entryPath(packPath, slug)
	clean := l.validateEntry(family, packPath, slug, raw, nil)
	var p model.Person
	if err := json.Unmarshal(raw, &p); err != nil {
		l.reportUndecodable(ep, clean, err)
		return
	}
	if p.ID != slug {
		l.add(ep, "id %q does not match its entry key %q", p.ID, slug)
	}
	l.cat.People = append(l.cat.People, &p)
	l.idx.person[&p] = ep
}

func (l *loader) readSeriesEntry(family pack.Family, packPath, slug string, raw json.RawMessage) {
	ep := entryPath(packPath, slug)
	clean := l.validateEntry(family, packPath, slug, raw, nil)
	var s model.Series
	if err := json.Unmarshal(raw, &s); err != nil {
		l.reportUndecodable(ep, clean, err)
		return
	}
	if s.ID != slug {
		l.add(ep, "id %q does not match its entry key %q", s.ID, slug)
	}
	l.cat.Series = append(l.cat.Series, &s)
	l.idx.series[&s] = ep
}
