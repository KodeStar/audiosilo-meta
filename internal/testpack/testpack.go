// Package testpack builds and reads pack-layout data trees for tests.
//
// Both writers' suites (internal/importer, internal/issueform) need the same two
// things: seed a catalogue, then read one record back out of it. Entities are
// addressed by the familiar per-entity path ("works/th/the-thing/work.json",
// "people/ja/jane-doe.json") because that is what a seed literal reads as - the
// address is a REFERENCE syntax, and this package resolves it to the pack family
// and entry key the record actually lives in. Everything a seed writes lands in
// its family's first pack, which is where the writers would put it at test
// scale.
//
// Raw and Read return a record in CANONICAL form (pkg/canonical), whatever
// family it came from and whether or not extracting it needed a re-marshal.
// That is one view, not two: a test comparing a record against the literal that
// seeded it is comparing values, and a canonical rendering is the only form
// where "unchanged" means the same bytes.
//
// It is test support only: nothing outside a _test.go file imports it.
package testpack

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"sort"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/canonical"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// addr is a per-entity address resolved onto the pack layout: the family and
// entry key holding the record, plus the path INSIDE that entry when the record
// is nested (a recording under "recordings", a sidecar under its member name).
type addr struct {
	family pack.Family
	slug   string
	// member is the entry member holding the record: "" for an entry that IS the
	// record, "characters"/"recaps" for a sidecar, "recordings" for a recording.
	member string
	// key is the recordings-map key, set only for a recording.
	key string
}

// resolve maps a per-entity address onto its pack address.
func resolve(t testing.TB, address string) addr {
	t.Helper()
	loc, ok := model.ParseLocation(address)
	if !ok {
		t.Fatalf("testpack: %q is not a recognized entity address", address)
	}
	switch loc.Kind {
	case model.KindWork:
		return addr{family: pack.FamilyWorks, slug: loc.Slug}
	case model.KindRecording:
		return addr{family: pack.FamilyWorks, slug: loc.WorkSlug, member: "recordings", key: loc.Slug}
	case model.KindCharacters:
		return addr{family: pack.FamilyWorksCommunity, slug: loc.WorkSlug, member: "characters"}
	case model.KindRecaps:
		return addr{family: pack.FamilyWorksCommunity, slug: loc.WorkSlug, member: "recaps"}
	case model.KindPerson:
		return addr{family: pack.FamilyPeople, slug: loc.Slug}
	case model.KindSeries:
		return addr{family: pack.FamilySeries, slug: loc.Slug}
	}
	t.Fatalf("testpack: address %q has no pack family", address)
	return addr{}
}

// packPath is the data-relative path of a family's first (and, for a seed, only)
// pack.
func packPath(f pack.Family) string {
	def, _ := pack.Def(f)
	ref := pack.PackRef{Family: f, Bound: pack.MinBound}
	if def.Dirs {
		ref.Dir = pack.MinBound
	}
	return ref.Path()
}

// Seed writes a catalogue into dataDir's pack tree. Keys are per-entity
// addresses, values the record's JSON; records that share an entry (a work and
// its recordings, a work's two sidecars) are merged into it.
func Seed(t testing.TB, dataDir string, files map[string]string) {
	t.Helper()
	entries := map[pack.Family]map[string]map[string]any{}
	for _, address := range sortedKeys(files) {
		a := resolve(t, address)
		// pack.DecodeEntry, so a seed's numbers reach the pack exactly as written
		// (a float round-trip would reformat them, and several tests compare a
		// record against the literal that seeded it) and a seed with a duplicate
		// key is rejected rather than silently halved.
		obj, err := pack.DecodeEntry(json.RawMessage(files[address]))
		if err != nil {
			t.Fatalf("testpack: %s is not a usable JSON object: %v", address, err)
		}
		if entries[a.family] == nil {
			entries[a.family] = map[string]map[string]any{}
		}
		entry := entries[a.family][a.slug]
		if entry == nil {
			entry = map[string]any{}
			entries[a.family][a.slug] = entry
		}
		switch {
		case a.member == "":
			// The entry IS the record; merge so a work seeded after its own
			// recording keeps that recording.
			for k, v := range obj {
				entry[k] = v
			}
		case a.key != "":
			if err := pack.SetRecording(entry, a.key, obj); err != nil {
				t.Fatalf("testpack: %s: %v", address, err)
			}
		default:
			entry[a.member] = obj
		}
	}

	for _, def := range pack.Families() {
		byslug := entries[def.Family]
		if len(byslug) == 0 {
			continue
		}
		// Seeding is additive: a test that layers a second seed onto a tree it
		// already seeded must extend the pack, not replace it.
		file := existingPack(t, dataDir, def.Family)
		for slug, entry := range byslug {
			raw, err := json.Marshal(entry)
			if err != nil {
				t.Fatalf("testpack: marshal %s entry %q: %v", def.Family.Root(), slug, err)
			}
			file.Set(slug, raw)
		}
		data, err := file.Bytes()
		if err != nil {
			t.Fatalf("testpack: render %s pack: %v", def.Family.Root(), err)
		}
		full := filepath.Join(dataDir, filepath.FromSlash(packPath(def.Family)))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("testpack: mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, data, 0o644); err != nil {
			t.Fatalf("testpack: write %s: %v", full, err)
		}
	}
}

// SeedLegacyPerson writes ONE file-per-entity person record into dataDir,
// putting the people family in the legacy layout without laying out a whole
// legacy tree. It is what the writers' legacy-refusal tests refuse: pack layout
// is detected per family from the shape of a file under its root, so a single
// record is enough to make the family legacy.
//
// It returns the record's bytes so a test can prove a refused run left the tree
// exactly as it found it (see AssertUntouched).
func SeedLegacyPerson(t testing.TB, dataDir, slug, name string) string {
	t.Helper()
	body := `{"id":"` + slug + `","license":"CC0-1.0","name":"` + name + `","sources":[{"type":"user"}]}` + "\n"
	full := filepath.Join(dataDir, "people", model.Shard(slug), slug+".json")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("testpack: mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("testpack: write %s: %v", full, err)
	}
	return body
}

// AssertUntouched checks that a refused run wrote nothing: the legacy record
// still holds want, and no family root the run would have written was created.
func AssertUntouched(t testing.TB, dataDir, slug, want string) {
	t.Helper()
	full := filepath.Join(dataDir, "people", model.Shard(slug), slug+".json")
	got, err := os.ReadFile(full)
	if err != nil || string(got) != want {
		t.Errorf("testpack: the legacy record was touched (err=%v): %s", err, got)
	}
	for _, f := range []pack.Family{pack.FamilyWorks, pack.FamilyWorksCommunity, pack.FamilySeries} {
		if _, err := os.Stat(filepath.Join(dataDir, f.Root())); !os.IsNotExist(err) {
			t.Errorf("testpack: the refused run created %s/ (stat err = %v)", f.Root(), err)
		}
	}
}

// existingPack reads a family's seed pack, or an empty one when there is none.
func existingPack(t testing.TB, dataDir string, f pack.Family) *pack.File {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dataDir, filepath.FromSlash(packPath(f))))
	if err != nil {
		if os.IsNotExist(err) {
			return pack.NewFile()
		}
		t.Fatalf("testpack: read %s: %v", packPath(f), err)
	}
	file, err := pack.Parse(raw)
	if err != nil {
		t.Fatalf("testpack: parse %s: %v", packPath(f), err)
	}
	return file
}

// Recordings returns a work's recording slugs, sorted: the recordings map inside
// its composite entry, which is the pack layout's recordings directory.
func Recordings(t testing.TB, dataDir, workSlug string) []string {
	t.Helper()
	store, err := pack.Open(dataDir)
	if err != nil {
		t.Fatalf("testpack: open %s: %v", dataDir, err)
	}
	entry, ok, err := store.Get(pack.FamilyWorks, workSlug)
	if err != nil {
		t.Fatalf("testpack: read works entry %q: %v", workSlug, err)
	}
	if !ok {
		t.Fatalf("testpack: no work %q", workSlug)
	}
	return recordingSlugs(t, workSlug, entry)
}

// Raw returns a record's CANONICAL JSON from the pack tree, asserting on the way
// that the pack holding it is in canonical form. found is false when the entry
// (or the nested record inside it) is absent.
//
// A WORK address returns the work's own fields, without the "recordings" map
// spliced into its entry - the record a work.json used to hold. Use Recordings
// for the map.
func Raw(t testing.TB, dataDir, address string) (raw json.RawMessage, found bool) {
	t.Helper()
	a := resolve(t, address)
	store, err := pack.Open(dataDir)
	if err != nil {
		t.Fatalf("testpack: open %s: %v", dataDir, err)
	}
	entry, ok, err := store.Get(a.family, a.slug)
	if err != nil {
		t.Fatalf("testpack: read %s entry %q: %v", a.family.Root(), a.slug, err)
	}
	if !ok {
		return nil, false
	}
	assertCanonical(t, dataDir, store, a)

	record, found := extract(t, a, entry)
	if !found {
		return nil, false
	}
	// One view for every family: extracting a work needs a re-marshal (the
	// recordings map comes off) and the others do not, so canonicalizing here is
	// what stops Raw returning two different renderings of the same thing.
	formatted, err := canonical.Format(record)
	if err != nil {
		t.Fatalf("testpack: canonicalize %s: %v", address, err)
	}
	return formatted, true
}

// extract pulls the addressed record out of its entry.
func extract(t testing.TB, a addr, entry json.RawMessage) (json.RawMessage, bool) {
	t.Helper()
	if a.member == "" {
		if a.family == pack.FamilyWorks {
			return withoutRecordings(t, entry), true
		}
		return entry, true
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(entry, &obj); err != nil {
		t.Fatalf("testpack: parse %s entry %q: %v", a.family.Root(), a.slug, err)
	}
	member, ok := obj[a.member]
	if !ok {
		return nil, false
	}
	if a.key == "" {
		return member, true
	}
	var nested map[string]json.RawMessage
	if err := json.Unmarshal(member, &nested); err != nil {
		t.Fatalf("testpack: parse %s entry %q member %q: %v", a.family.Root(), a.slug, a.member, err)
	}
	rec, ok := nested[a.key]
	return rec, ok
}

// Read decodes a record from the pack tree into v, failing the test when it is
// absent.
func Read(t testing.TB, dataDir, address string, v any) {
	t.Helper()
	raw, ok := Raw(t, dataDir, address)
	if !ok {
		t.Fatalf("testpack: no record at %s", address)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("testpack: unmarshal %s: %v", address, err)
	}
}

// Exists reports whether the tree holds a record at the address.
func Exists(t testing.TB, dataDir, address string) bool {
	t.Helper()
	_, ok := Raw(t, dataDir, address)
	return ok
}

// Addresses returns a per-entity address for every record in the tree, sorted.
// It is what lets a test iterate what a run actually composed instead of
// hard-coding a list of record kinds - Result.Files names packs, not records.
func Addresses(t testing.TB, dataDir string) []string {
	t.Helper()
	var out []string
	eachEntry(t, dataDir, func(f pack.Family, slug string, entry json.RawMessage) {
		out = append(out, entryAddresses(t, f, slug, entry)...)
	})
	sort.Strings(out)
	return out
}

// Slugs returns a family's entry keys, sorted.
func Slugs(t testing.TB, dataDir string, f pack.Family) []string {
	t.Helper()
	var out []string
	eachEntry(t, dataDir, func(got pack.Family, slug string, _ json.RawMessage) {
		if got == f {
			out = append(out, slug)
		}
	})
	sort.Strings(out)
	return out
}

// eachEntry walks every family's packs in bound order and calls fn for each
// entry. It is the one tree read the package makes, so a caller only decides
// what to do with an entry, never how to find one.
func eachEntry(t testing.TB, dataDir string, fn func(f pack.Family, slug string, entry json.RawMessage)) {
	t.Helper()
	for _, def := range pack.Families() {
		tree, err := pack.ReadTree(dataDir, def.Family)
		if err != nil {
			t.Fatalf("testpack: read %s tree: %v", def.Family.Root(), err)
		}
		for _, ref := range tree.Packs() {
			raw, err := os.ReadFile(filepath.Join(dataDir, filepath.FromSlash(ref.Path())))
			if err != nil {
				t.Fatalf("testpack: read %s: %v", ref.Path(), err)
			}
			file, err := pack.Parse(raw)
			if err != nil {
				t.Fatalf("testpack: parse %s: %v", ref.Path(), err)
			}
			for _, slug := range file.Slugs() {
				entry, _ := file.Get(slug)
				fn(def.Family, slug, entry)
			}
		}
	}
}

// entryAddresses renders the per-entity addresses one pack entry holds.
func entryAddresses(t testing.TB, f pack.Family, slug string, entry json.RawMessage) []string {
	t.Helper()
	// Every address a works or works-community entry yields sits under the
	// work's directory, so the shard is computed once for all of them.
	dir := path.Join(f.Root(), model.Shard(slug), slug)
	switch f {
	case pack.FamilyPeople, pack.FamilySeries:
		return []string{path.Join(f.Root(), model.Shard(slug), slug+".json")}
	case pack.FamilyWorks:
		out := []string{path.Join(dir, "work.json")}
		for _, rec := range recordingSlugs(t, slug, entry) {
			out = append(out, path.Join(dir, "recordings", rec+".json"))
		}
		return out
	case pack.FamilyWorksCommunity:
		var members map[string]json.RawMessage
		if err := json.Unmarshal(entry, &members); err != nil {
			t.Fatalf("testpack: parse works-community entry %q: %v", slug, err)
		}
		var out []string
		for _, name := range []string{"characters", "recaps"} {
			if _, ok := members[name]; ok {
				// A sidecar's address still names the WORK's directory: the
				// works-community family is a storage split, not a rename.
				out = append(out, path.Join("works", model.Shard(slug), slug, name+".json"))
			}
		}
		return out
	}
	return nil
}

// recordingSlugs returns the recordings-map keys of a works entry, sorted.
func recordingSlugs(t testing.TB, slug string, entry json.RawMessage) []string {
	t.Helper()
	var aux struct {
		Recordings map[string]json.RawMessage `json:"recordings"`
	}
	if err := json.Unmarshal(entry, &aux); err != nil {
		t.Fatalf("testpack: parse works entry %q: %v", slug, err)
	}
	out := make([]string, 0, len(aux.Recordings))
	for rec := range aux.Recordings {
		out = append(out, rec)
	}
	sort.Strings(out)
	return out
}

// assertCanonical checks the pack holding an entry is canonically formatted -
// the assertion the per-file readJSON used to make about the record's own file.
func assertCanonical(t testing.TB, dataDir string, store *pack.Store, a addr) {
	t.Helper()
	ref, err := store.Locate(a.family, a.slug)
	if err != nil {
		t.Fatalf("testpack: locate %s %q: %v", a.family.Root(), a.slug, err)
	}
	raw, err := os.ReadFile(filepath.Join(dataDir, filepath.FromSlash(ref.Path())))
	if err != nil {
		t.Fatalf("testpack: read %s: %v", ref.Path(), err)
	}
	if ok, ferr := canonical.IsCanonical(raw); ferr != nil || !ok {
		t.Errorf("%s is not canonical (err=%v)", ref.Path(), ferr)
	}
}

// withoutRecordings strips the recordings map from a works entry, leaving the
// work's own record. Members are kept raw, so numbers survive; Raw canonicalizes
// what comes back, so the re-marshal's rendering never leaks out.
func withoutRecordings(t testing.TB, entry json.RawMessage) json.RawMessage {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(entry, &obj); err != nil {
		t.Fatalf("testpack: parse works entry: %v", err)
	}
	if _, ok := obj["recordings"]; !ok {
		return entry
	}
	delete(obj, "recordings")
	raw, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("testpack: re-marshal works entry: %v", err)
	}
	return raw
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
