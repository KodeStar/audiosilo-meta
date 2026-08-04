package issueform

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// Field labels for correct-data.yml.
const (
	fCorrectRecord    = "Record"
	fCorrectField     = "Field"
	fCorrectCorrected = "Corrected value"
	fCorrectEvidence  = "Evidence / source"
)

// fieldKind classifies a correctable scalar field so the corrector coerces the
// value to the right JSON type before writing it.
type fieldKind int

const (
	kindString fieldKind = iota
	kindInt
	kindBool
	kindLanguage
	kindDateYear
	kindDateFlex
	kindHTTPSURL
	kindPersonKind
)

// personKinds is the schema's person.kind enum, taken from pkg/model's
// constants rather than re-spelled here so the form can never drift from the
// values the schema accepts.
var personKinds = map[string]bool{
	model.KindEntityPerson:    true,
	model.KindEntityGroup:     true,
	model.KindEntityPublisher: true,
}

// correctOp is what a correction DOES to one field. Exactly one of the two is
// set: kind names the coercion for a scalar the correction REPLACES, and add
// names the routine for a field the correction CONTRIBUTES ONE ENTRY TO.
//
// Two shapes, one table, one lookup: a field named here is always applied by the
// op recorded beside it, so a name can never be reachable with nothing to do -
// which would stamp the correction's provenance onto a record it left otherwise
// unchanged, a silent write with no fact in it.
type correctOp struct {
	kind fieldKind
	add  func(*composer, entryAddr, map[string]any, string) (applied string, ok bool)
}

// correctableFields is the allowlist of fields a correction may touch per entity
// kind. Anything not listed (the other arrays, xref objects, sources, series
// works) needs a human. Keys are the schema field names; synonyms are resolved
// by normalizeFieldName.
//
// The two ADD ops are recordings-only, and the outer key is what says so: a
// work's xref.isbn is a different field with a different shape (flat print
// ISBNs) and keeps its existing needs-human verdict.
var correctableFields = map[model.Kind]map[string]correctOp{
	model.KindWork: {
		"title": {kind: kindString}, "subtitle": {kind: kindString},
		"language": {kind: kindLanguage}, "first_published": {kind: kindDateYear},
	},
	model.KindRecording: {
		"publisher": {kind: kindString}, "runtime_min": {kind: kindInt},
		"release_date": {kind: kindDateFlex}, "cover_url": {kind: kindHTTPSURL},
		"abridged": {kind: kindBool}, "language": {kind: kindLanguage},
		"isbn":       {add: (*composer).correctISBN},
		"publishers": {add: (*composer).correctPublishers},
	},
	model.KindPerson: {
		"name": {kind: kindString}, "sort_name": {kind: kindString},
		"description": {kind: kindString}, "kind": {kind: kindPersonKind},
	},
	model.KindSeries: {
		"name": {kind: kindString},
	},
}

// fieldSynonyms maps a few common field-name spellings onto their schema name.
var fieldSynonyms = map[string]string{
	"runtime": "runtime_min", "runtime_minutes": "runtime_min",
	"cover": "cover_url", "cover_image": "cover_url", "cover_image_url": "cover_url",
	"released": "release_date", "publication_year": "first_published",
	"first_published_year": "first_published",
	"isbns":                "isbn",
	"audiobook_isbn":       "isbn", "audiobook_isbns": "isbn",
	"regional_publisher": "publishers", "regional_publishers": "publishers",
}

// The two additive fields are most likely to be named by copying the FORM LABEL
// across from the add-recording form, so those spellings are synonyms too -
// derived from the label constants rather than hand-spelled, since a label
// rename would otherwise strand the synonym silently (the same drift
// TestFieldLabelsExistInTemplates guards on the other side).
func init() {
	fieldSynonyms[normalizeFieldName(fRecISBNs)] = "isbn"
	fieldSynonyms[normalizeFieldName(fRecPublishers)] = "publishers"
}

// correctData applies a single-field correction to an existing record, or bails
// to needs-human when the field is not a cleanly-mappable scalar.
//
// A recording correction edits the recording INSIDE its work's composite entry
// and writes the whole entry back, which is the granularity the pack store
// stores; every other kind is an entry of its own.
func (c *composer) correctData(s sections) {
	if !s.checked(fCC0) {
		c.fail(StatusInvalid, "the CC0 public-domain dedication checkbox is not ticked")
		return
	}
	ref, ok := resolveRecordRef(s.get(fCorrectRecord))
	if !ok {
		c.fail(StatusNeedsHuman, "could not resolve %q to a record in data/ - a maintainer will locate it", s.get(fCorrectRecord))
		return
	}
	addr, ok := entryAddress(ref)
	if !ok {
		c.fail(StatusNeedsHuman, "corrections to a %s record are not auto-applied - a maintainer will handle it", ref.kind)
		return
	}

	fieldName := normalizeFieldName(s.get(fCorrectField))
	corrected := s.get(fCorrectCorrected)
	evidence := s.get(fCorrectEvidence)
	if fieldName == "" || corrected == "" || evidence == "" {
		c.fail(StatusInvalid, "Field, Corrected value, and Evidence are all required")
		return
	}

	fields, ok := correctableFields[ref.kind]
	if !ok {
		c.fail(StatusNeedsHuman, "corrections to a %s record are not auto-applied - a maintainer will handle it", ref.kind)
		return
	}
	op, ok := fields[fieldName]
	if !ok {
		c.fail(StatusNeedsHuman, "field %q on a %s cannot be auto-corrected (only simple scalar fields are) - a maintainer will apply it", fieldName, ref.kind)
		return
	}
	if op.add != nil {
		c.correctAdditive(addr, op.add, corrected, evidence)
		return
	}

	value, ok := coerceFieldValue(op.kind, corrected)
	if !ok {
		c.fail(StatusInvalid, "corrected value %q is not valid for field %q", corrected, fieldName)
		return
	}

	entry, record, ok := c.correctionTarget(addr)
	if !ok {
		return
	}
	if !c.renameableInPlace(addr, ref.kind, fieldName, value) {
		return
	}
	if !c.publisherFreeOfRegionalImprint(addr, record, fieldName, value) {
		return
	}
	record[fieldName] = value

	// Record the correction's provenance so the source can be audited later.
	record["sources"] = appendSource(record["sources"], map[string]any{
		"type": sourceUser, "ref": evidence, "imported_at": c.date,
	})

	if !c.putEntry(addr.family, addr.slug, entry) {
		return
	}
	c.note("applied %s = %v on %s", fieldName, value, addr.label(c))
}

// correctionTarget reads the record a correction addresses. entry is the unit
// the store writes; record is the object being corrected, which for a recording
// is a live sub-map of entry - so mutating record and writing entry back is one
// edit, at the granularity the pack store stores.
func (c *composer) correctionTarget(addr entryAddr) (entry, record map[string]any, ok bool) {
	entry, found, ok := c.entryRaw(addr.family, addr.slug)
	if !ok {
		return nil, nil, false
	}
	if !found {
		c.fail(StatusNeedsHuman, "record %s does not exist - a maintainer will locate the right record", addr.label(c))
		return nil, nil, false
	}
	record = entry
	if addr.recSlug != "" {
		recs, _ := entry["recordings"].(map[string]any)
		record, _ = recs[addr.recSlug].(map[string]any)
		if record == nil {
			c.fail(StatusNeedsHuman, "record %s does not exist - a maintainer will locate the right record", addr.label(c))
			return nil, nil, false
		}
	}
	return entry, record, true
}

// correctAdditive applies one of the two additive recording ops and writes the
// entry back, stamping the correction's evidence exactly as the scalar path
// does. Each op returns the sentence the verdict reports, or fails the run.
//
// TIER POLICY IS UNCHANGED HERE. Corrections have never carried a trust-tier
// gate of their own - a correction is a correction whoever seeded the record -
// and this change does not add one. The gate that DOES fire is the existing
// duplicate routing: an ISBN already recorded on a different recording goes
// through failDuplicate, so a bulk-mirror-only incumbent still routes to a
// maintainer rather than being closed as a duplicate.
func (c *composer) correctAdditive(addr entryAddr, add func(*composer, entryAddr, map[string]any, string) (string, bool), corrected, evidence string) {
	entry, record, ok := c.correctionTarget(addr)
	if !ok {
		return
	}
	applied, ok := add(c, addr, record, corrected)
	if !ok {
		return
	}
	record["sources"] = appendSource(record["sources"], map[string]any{
		"type": sourceUser, "ref": evidence, "imported_at": c.date,
	})
	if !c.putEntry(addr.family, addr.slug, entry) {
		return
	}
	c.note("%s on %s", applied, addr.label(c))
}

// correctISBN adds one ISBN to a recording's isbn[], in whichever spelling the
// correction stated it.
//
// Three outcomes write, refuse, or escalate, and the split is about what the
// correction ASSERTS. An ISBN the record does not carry is a new fact: appended.
// An ISBN the record carries as a BARE entry, corrected with a region, is the
// same fact plus one more - the bare spelling states no region, so scoping it
// contradicts nothing and the entry is upgraded in place. An ISBN the record
// carries under a DIFFERENT stated region is a genuine disagreement between two
// sources, and a stated region is never rewritten mechanically: that goes to a
// maintainer.
func (c *composer) correctISBN(addr entryAddr, record map[string]any, corrected string) (string, bool) {
	stated, reason, ok := parseISBNLine(corrected)
	if !ok {
		c.fail(StatusInvalid, "corrected value %q is not a usable ISBN: %s", corrected, reason)
		return "", false
	}
	key := isbnKey(stated.ISBN)
	self := addr.recRef()

	// The global gate first, with its existing verdict: an identifier that
	// belongs to another recording is not this record's to gain.
	if other, dup := c.isbnRec[key]; dup && other != self {
		c.failDuplicate(other, "ISBN %s is already recorded on %s", stated.ISBN, c.recLocation(other))
		return "", false
	}

	// Symmetric with readRegionPublishers: a value that is not the array the
	// schema requires is escalated, never appended to. `arr, _ := ...([]any)`
	// alone yields nil for a string-valued isbn, and the append below would then
	// REPLACE the recorded value and report ok - a silent destructive write.
	// Unreachable from a green tree, which is exactly why it must not be silent.
	arr, arrOK := record["isbn"].([]any)
	if record["isbn"] != nil && !arrOK {
		c.fail(StatusNeedsHuman, "the isbn[] already on %s is not in the expected shape - a maintainer will apply this", addr.label(c))
		return "", false
	}
	i, recorded, found := findISBNEntry(arr, key)
	if !found {
		record["isbn"] = append(arr, stated)
		if stated.Region == "" {
			return fmt.Sprintf("added ISBN %s (region unstated)", stated.ISBN), true
		}
		return fmt.Sprintf("added ISBN %s for region %q", stated.ISBN, stated.Region), true
	}

	switch {
	case recorded.Region == stated.Region:
		c.failNoop("ISBN %s is already recorded on %s exactly as stated", stated.ISBN, addr.label(c))
	case recorded.Region == "":
		// The upgrade: bare means "region unstated", so naming one adds a fact
		// rather than replacing one.
		arr[i] = stated
		record["isbn"] = arr
		return fmt.Sprintf("scoped the recorded ISBN %s to region %q", stated.ISBN, stated.Region), true
	case stated.Region == "":
		c.failNoop("ISBN %s is already recorded on %s as a %q release, and this correction states no region", stated.ISBN, addr.label(c), recorded.Region)
	default:
		c.fail(StatusNeedsHuman, "ISBN %s is recorded on %s as a %q release and this correction says %q - a stated region is never changed mechanically, so a maintainer will decide which source is right",
			stated.ISBN, addr.label(c), recorded.Region, stated.Region)
	}
	return "", false
}

// findISBNEntry locates the element of a raw isbn[] carrying the given key,
// reading each through model.ISBNRefOf - the one reader of the two on-disk
// spellings. An element it cannot read is skipped rather than matched: whatever
// it is, it is not this identifier.
func findISBNEntry(arr []any, key string) (index int, entry model.ISBNRef, found bool) {
	for i, v := range arr {
		got, ok := model.ISBNRefOf(v)
		if ok && isbnKey(got.ISBN) == key {
			return i, got, true
		}
	}
	return 0, model.ISBNRef{}, false
}

// correctPublishers adds one regional imprint to a recording's publishers[].
//
// It enforces the same three rules parsePublishers does on the add forms - one
// imprint per region, never a restatement of the publisher of record, and never
// a publishers[] without one - because they are the schema's and pkg/check's
// rules, and asking them here is what turns "your correction was invalid" (a raw
// metacheck line about a record the bot already wrote) into a verdict that names
// what to do instead. The list is rewritten sorted by region.
func (c *composer) correctPublishers(addr entryAddr, record map[string]any, corrected string) (string, bool) {
	stated, reason, ok := parsePublisherLine(corrected)
	if !ok {
		c.fail(StatusInvalid, "corrected value %s", reason)
		return "", false
	}

	ofRecord, _ := record["publisher"].(string)
	if ofRecord == "" {
		c.fail(StatusNeedsHuman, "%s has no publisher, and the schema requires one before publishers[] can list the OTHER regions' imprints - correct \"publisher\" first (that one IS applied automatically), then file this again",
			addr.label(c))
		return "", false
	}
	if stated.Publisher == ofRecord {
		c.fail(StatusInvalid, "%q is already the publisher of %s; publishers[] holds the OTHER regions' imprints of the same recording", stated.Publisher, addr.label(c))
		return "", false
	}

	existing, readOK := readRegionPublishers(record["publishers"])
	if !readOK {
		c.fail(StatusNeedsHuman, "the publishers[] already on %s is not in the expected shape - a maintainer will apply this", addr.label(c))
		return "", false
	}
	for _, p := range existing {
		if p.Region != stated.Region {
			continue
		}
		if p.Publisher == stated.Publisher {
			c.failNoop("region %q on %s already names %q", stated.Region, addr.label(c), stated.Publisher)
			return "", false
		}
		c.fail(StatusNeedsHuman, "region %q on %s already names %q and this correction says %q - a maintainer will decide which source is right", stated.Region, addr.label(c), p.Publisher, stated.Publisher)
		return "", false
	}

	existing = append(existing, stated)
	sortRegionPublishers(existing)
	record["publishers"] = existing
	return fmt.Sprintf("added regional publisher %q for region %q", stated.Publisher, stated.Region), true
}

// readRegionPublishers decodes a raw publishers[] into the model type through a
// JSON round-trip, so the field names come from RegionPublisher's own tags
// rather than from a second hand-written spelling of them here. ok is false when
// the value is not the array of objects the schema requires (or an entry is
// missing a half), so a malformed list is escalated rather than silently
// rewritten without its odd entries.
func readRegionPublishers(v any) ([]model.RegionPublisher, bool) {
	if v == nil {
		return nil, true
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	var out []model.RegionPublisher
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, false
	}
	for _, p := range out {
		if p.Region == "" || p.Publisher == "" {
			return nil, false
		}
	}
	return out, true
}

// publisherFreeOfRegionalImprint is the OTHER half of the publisher/publishers
// pairing, checked from the scalar side.
//
// publishers[] may never restate the publisher of record (pkg/check's
// checkRegionalPublishers), and the compose paths already refuse a submission
// that would break that. But the same pair can be broken from this end: add
// "GB: Gollancz" to publishers[], then correct publisher to "Gollancz". Each
// correction is individually valid, and without this the second one is WRITTEN
// and then bounced by the post-write validation, handing the submitter the raw
// metacheck line these friendly refusals exist to replace.
//
// It is needs-human rather than invalid: the submitter has stated something true
// about the publisher, and which of the two fields should change is a judgement
// nothing mechanical can make.
func (c *composer) publisherFreeOfRegionalImprint(addr entryAddr, record map[string]any, field string, value any) bool {
	if field != "publisher" {
		return true
	}
	name, _ := value.(string)
	existing, ok := readRegionPublishers(record["publishers"])
	if !ok {
		c.fail(StatusNeedsHuman, "the publishers[] already on %s is not in the expected shape - a maintainer will apply this", addr.label(c))
		return false
	}
	for _, p := range existing {
		if p.Publisher != name {
			continue
		}
		c.fail(StatusNeedsHuman, "%s already lists %q as the imprint for region %q, and publishers[] holds the OTHER regions' imprints - so it cannot also be the publisher of record; a maintainer will decide which of the two fields is wrong",
			addr.label(c), name, p.Region)
		return false
	}
	return true
}

// renameableInPlace reports whether a correction can be applied where the record
// already sits, and fails the submission with the reason when it cannot.
//
// A PERSON's id IS the slug of their name (pkg/check's checkPersonSlug), so a
// name correction that slugs differently is not an edit to one field - it is a
// rename, which moves the record to a new address and has to rewrite every work
// that credits it. Nothing here can do that, and the intake bot must not tell a
// submitter their correction was invalid when it is simply beyond automation:
// left to fall through, the write lands and the metacheck rule reports its own
// message under the contributor-fault verdict.
func (c *composer) renameableInPlace(addr entryAddr, kind model.Kind, field string, value any) bool {
	if kind != model.KindPerson || field != "name" {
		return true
	}
	name, _ := value.(string)
	want, _ := model.PersonSlug(name)
	if want == addr.slug {
		return true
	}
	c.fail(StatusNeedsHuman, "renaming %s to %q changes its id to %q - a person's id IS the slug of their name, so the record has to move and every work crediting it has to be rewritten; a maintainer will do it", addr.label(c), name, want)
	return false
}

// entryAddr is an entity's address in the pack layout: the family and entry key
// it lives under, plus the recordings-map key when it is a recording nested in
// its work's composite entry.
type entryAddr struct {
	family  pack.Family
	slug    string
	recSlug string
}

// label renders the address for a human-facing message.
func (a entryAddr) label(c *composer) string {
	return c.entryLocation(a.family, a.slug, a.recSlug)
}

// recRef is the address as the dedup maps key it. Only meaningful for a
// recording address, which is the only kind the additive ops accept.
func (a entryAddr) recRef() recRef { return recRef{Work: a.slug, Rec: a.recSlug} }

// entryAddress maps a parsed record reference onto its pack address. ok is false
// for a kind no correction may touch.
func entryAddress(ref recordRef) (entryAddr, bool) {
	switch ref.kind {
	case model.KindWork:
		return entryAddr{family: pack.FamilyWorks, slug: ref.slug}, true
	case model.KindRecording:
		return entryAddr{family: pack.FamilyWorks, slug: ref.workSlug, recSlug: ref.slug}, true
	case model.KindPerson:
		return entryAddr{family: pack.FamilyPeople, slug: ref.slug}, true
	case model.KindSeries:
		return entryAddr{family: pack.FamilySeries, slug: ref.slug}, true
	default:
		// The CC BY-SA sidecars: community prose, never a scalar correction.
		return entryAddr{}, false
	}
}

// normalizeFieldName lowercases a field name, turns spaces into underscores, and
// resolves a small set of synonyms onto the schema field.
func normalizeFieldName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.ReplaceAll(n, " ", "_")
	n = strings.ReplaceAll(n, "-", "_")
	if syn, ok := fieldSynonyms[n]; ok {
		return syn
	}
	return n
}

// coerceFieldValue converts a form string into the JSON value for its field
// kind, validating against the schema constraints.
func coerceFieldValue(kind fieldKind, raw string) (any, bool) {
	raw = strings.TrimSpace(raw)
	switch kind {
	case kindString:
		if raw == "" {
			return nil, false
		}
		return raw, true
	case kindInt:
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return nil, false
		}
		return n, true
	case kindBool:
		switch strings.ToLower(raw) {
		case "true", "abridged", "yes":
			return true, true
		case "false", "unabridged", "no":
			return false, true
		}
		return nil, false
	case kindLanguage:
		return normalizeLanguage(raw)
	case kindDateYear:
		if dateYearRE.MatchString(raw) {
			return raw, true
		}
		return nil, false
	case kindDateFlex:
		if dateFlexRE.MatchString(raw) {
			return raw, true
		}
		return nil, false
	case kindHTTPSURL:
		if strings.HasPrefix(raw, "https://") {
			return raw, true
		}
		return nil, false
	case kindPersonKind:
		// The enum values are lowercase, and a submitter typing "Publisher"
		// means the same thing, so the input is lowercased before the lookup.
		// Anything still outside the enum is rejected here rather than written
		// as a schema-invalid record.
		k := strings.ToLower(raw)
		if personKinds[k] {
			return k, true
		}
		return nil, false
	}
	return nil, false
}

// appendSource appends a source object to an existing (possibly nil) sources
// array read from a raw record.
func appendSource(existing any, src map[string]any) []any {
	arr, _ := existing.([]any)
	return append(arr, src)
}
